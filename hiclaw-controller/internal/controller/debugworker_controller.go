package controller

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/oss"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	debugFinalizerName = "hiclaw.io/debug-cleanup"
)

// DebugWorkerReconciler manages the lifecycle of DebugWorker resources.
// It delegates to a standard Worker CRD (with debug annotations and skills)
// and synchronizes the child Worker's phase back to the DebugWorker status.
type DebugWorkerReconciler struct {
	client.Client
	OSS            oss.StorageClient
	OSSAdmin       oss.StorageAdminClient // nil in incluster mode
	WorkerAgentDir string                 // source dir for debug-analysis skill files
	SourceRepoURL  string                 // GitHub repo URL for source download, e.g. "https://github.com/higress-group/hiclaw"
}

// debugConfig is pushed to OSS for the DebugWorker's entrypoint and skills.
type debugConfig struct {
	Targets          []string                   `json:"targets"`
	MatrixCredential *v1beta1.MatrixCredential  `json:"matrixCredential,omitempty"`
}

func (r *DebugWorkerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.DebugWorker{}).
		Owns(&v1beta1.Worker{}). // re-reconcile when owned Worker changes
		Complete(r)
}

func (r *DebugWorkerReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx).WithValues("debugworker", req.Name)

	var dw v1beta1.DebugWorker
	if err := r.Get(ctx, req.NamespacedName, &dw); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !dw.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&dw, debugFinalizerName) {
			r.handleDelete(ctx, &dw)
			controllerutil.RemoveFinalizer(&dw, debugFinalizerName)
			if err := r.Update(ctx, &dw); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(&dw, debugFinalizerName) {
		controllerutil.AddFinalizer(&dw, debugFinalizerName)
		if err := r.Update(ctx, &dw); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}

	// Phase routing
	switch dw.Status.Phase {
	case "":
		return r.handleCreate(ctx, &dw)
	case "Pending", "Running":
		return r.syncChildWorkerStatus(ctx, &dw)
	case "Failed":
		// Failed can be retried — handleCreate is idempotent (checks for existing child Worker)
		return r.handleCreate(ctx, &dw)
	default:
		logger.Info("unknown phase, re-syncing", "phase", dw.Status.Phase)
		return r.syncChildWorkerStatus(ctx, &dw)
	}
}

func (r *DebugWorkerReconciler) handleCreate(ctx context.Context, dw *v1beta1.DebugWorker) (reconcile.Result, error) {
	logger := log.FromContext(ctx).WithValues("debugworker", dw.Name)
	logger.Info("creating DebugWorker")

	dwName := dw.Name
	agentPrefix := fmt.Sprintf("agents/%s", dwName)

	// 1. Push debug-config.json to OSS
	cfg := debugConfig{
		Targets:          dw.Spec.Targets,
		MatrixCredential: dw.Spec.MatrixCredential,
	}
	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return r.failCreate(ctx, dw, fmt.Sprintf("marshal debug config: %v", err))
	}
	if err := r.OSS.PutObject(ctx, agentPrefix+"/debug-config.json", cfgJSON); err != nil {
		return r.failCreate(ctx, dw, fmt.Sprintf("push debug-config.json: %v", err))
	}
	logger.Info("pushed debug-config.json")

	// 2. Push debug-analysis skill files to OSS
	if err := r.pushDebugAnalysisSkill(ctx, dwName); err != nil {
		logger.Error(err, "failed to push debug-analysis skill (non-fatal)")
	}

	// 2b. Push hiclaw source code to OSS (non-fatal)
	if dw.Spec.HiclawVersion != "" {
		if err := r.pushHiclawSource(ctx, dwName, dw.Spec.HiclawVersion); err != nil {
			logger.Error(err, "failed to push hiclaw source (non-fatal)", "version", dw.Spec.HiclawVersion)
		}
	}

	// 3. Build the child Worker CRD
	soul := r.generateDebugSoul(dw)
	runtime := dw.Spec.Runtime
	if runtime == "" {
		runtime = "openclaw"
	}

	// Build channel policy: allow AccessControl.AllowedUsers to interact
	// with the DebugWorker via both group @mentions and DMs.
	var channelPolicy *v1beta1.ChannelPolicySpec
	if len(dw.Spec.AccessControl.AllowedUsers) > 0 {
		channelPolicy = &v1beta1.ChannelPolicySpec{
			GroupAllowExtra: dw.Spec.AccessControl.AllowedUsers,
			DmAllowExtra:    dw.Spec.AccessControl.AllowedUsers,
		}
	}

	worker := &v1beta1.Worker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dwName,
			Namespace: dw.Namespace,
			Labels: map[string]string{
				"hiclaw.io/debug-worker": "true",
			},
			Annotations: map[string]string{
				"hiclaw.io/debug-worker":  "true",
				"hiclaw.io/debug-targets": strings.Join(dw.Spec.Targets, ","),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         v1beta1.SchemeGroupVersion.String(),
					Kind:               "DebugWorker",
					Name:               dw.Name,
					UID:                dw.UID,
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
			},
		},
		Spec: v1beta1.WorkerSpec{
			Model:         dw.Spec.Model,
			Runtime:       runtime,
			Image:         dw.Spec.Image,
			Soul:          soul,
			ChannelPolicy: channelPolicy,
		},
	}

	// 4. Create the Worker CRD (idempotent: skip if already exists)
	var existingWorker v1beta1.Worker
	if err := r.Get(ctx, client.ObjectKeyFromObject(worker), &existingWorker); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, fmt.Errorf("check existing child Worker: %w", err)
		}
		// Not found — create it
		if err := r.Create(ctx, worker); err != nil {
			return r.failCreate(ctx, dw, fmt.Sprintf("create child Worker: %v", err))
		}
		logger.Info("created child Worker CRD", "worker", dwName)
	} else {
		logger.Info("child Worker already exists, skipping creation", "worker", dwName)
	}

	// 5. Update DebugWorker status to Pending
	dw.Status.Phase = "Pending"
	dw.Status.Message = "Waiting for child Worker to become Running"
	if err := r.Status().Update(ctx, dw); err != nil {
		return reconcile.Result{}, fmt.Errorf("update status to Pending: %w", err)
	}

	return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *DebugWorkerReconciler) syncChildWorkerStatus(ctx context.Context, dw *v1beta1.DebugWorker) (reconcile.Result, error) {
	logger := log.FromContext(ctx).WithValues("debugworker", dw.Name)

	// Get child Worker
	var worker v1beta1.Worker
	key := types.NamespacedName{Name: dw.Name, Namespace: dw.Namespace}
	if err := r.Get(ctx, key, &worker); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Child Worker not found — it might not be created yet or was deleted
			logger.Info("child Worker not found")
			return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get child Worker: %w", err)
	}

	childPhase := worker.Status.Phase

	switch childPhase {
	case "Running":
		if dw.Status.Phase != "Running" {
			// Child is now Running — update OSS policy with read-only targets and mark Running
			r.ensureDebugOSSPolicy(ctx, dw)

			dw.Status.Phase = "Running"
			dw.Status.Message = ""
			if err := r.Status().Update(ctx, dw); err != nil {
				return reconcile.Result{}, fmt.Errorf("update status to Running: %w", err)
			}
			logger.Info("DebugWorker is now Running")
		}
		return reconcile.Result{}, nil

	case "Failed":
		if dw.Status.Phase != "Failed" {
			dw.Status.Phase = "Failed"
			dw.Status.Message = worker.Status.Message
			if err := r.Status().Update(ctx, dw); err != nil {
				return reconcile.Result{}, fmt.Errorf("update status to Failed: %w", err)
			}
			logger.Info("DebugWorker marked Failed", "reason", worker.Status.Message)
		}
		return reconcile.Result{}, nil

	default:
		// Still pending/creating — requeue
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}
}

func (r *DebugWorkerReconciler) handleDelete(ctx context.Context, dw *v1beta1.DebugWorker) {
	logger := log.FromContext(ctx).WithValues("debugworker", dw.Name)
	logger.Info("cleaning up DebugWorker")

	// Worker CR is cascade-deleted via OwnerReference — no explicit delete needed.

	// Best-effort: clean up debug-config.json from OSS
	agentPrefix := fmt.Sprintf("agents/%s/", dw.Name)
	if err := r.OSS.DeleteObject(ctx, agentPrefix+"debug-config.json"); err != nil {
		logger.Error(err, "failed to delete debug-config.json from OSS (non-fatal)")
	}
}

func (r *DebugWorkerReconciler) ensureDebugOSSPolicy(ctx context.Context, dw *v1beta1.DebugWorker) {
	if r.OSSAdmin == nil {
		return // incluster mode — no MinIO admin, policy managed via STS
	}
	logger := log.FromContext(ctx).WithValues("debugworker", dw.Name)

	var readOnlyPrefixes []string
	for _, target := range dw.Spec.Targets {
		readOnlyPrefixes = append(readOnlyPrefixes, "agents/"+target)
	}

	if err := r.OSSAdmin.EnsurePolicy(ctx, oss.PolicyRequest{
		WorkerName:       dw.Name,
		ReadOnlyPrefixes: readOnlyPrefixes,
	}); err != nil {
		logger.Error(err, "failed to update OSS policy with debug read-only targets (non-fatal)")
	} else {
		logger.Info("updated OSS policy with debug read-only targets", "targets", dw.Spec.Targets)
	}
}

func (r *DebugWorkerReconciler) pushDebugAnalysisSkill(ctx context.Context, dwName string) error {
	skillSrcDir := filepath.Join(r.WorkerAgentDir, "skills", "debug-analysis")
	if _, err := os.Stat(skillSrcDir); err != nil {
		return fmt.Errorf("debug-analysis skill source dir not found: %w", err)
	}

	dstPrefix := fmt.Sprintf("agents/%s/skills/debug-analysis/", dwName)
	return r.OSS.Mirror(ctx, skillSrcDir+"/", dstPrefix, oss.MirrorOptions{Overwrite: true})
}

func (r *DebugWorkerReconciler) generateDebugSoul(dw *v1beta1.DebugWorker) string {
	targets := strings.Join(dw.Spec.Targets, ", ")
	return fmt.Sprintf(`# %s

You are a DebugWorker created to analyze and diagnose issues with the following target Workers: %s.

Your primary responsibilities:
1. Read target Workers' workspace files (SOUL.md, AGENTS.md, openclaw.json, LLM session logs) from ~/debug-targets/
2. Export and analyze Matrix room messages using the debug-analysis skill
3. Review LLM session logs (.openclaw/agents/main/sessions/*.jsonl) for each target
4. Identify issues, anomalies, or unexpected behaviors
5. Provide clear diagnostic reports with evidence

Always sync target workspaces before reading files to ensure you have the latest data.
Use the debug-analysis skill for workspace sync and Matrix message export.
`, dw.Name, targets)
}

func (r *DebugWorkerReconciler) failCreate(ctx context.Context, dw *v1beta1.DebugWorker, msg string) (reconcile.Result, error) {
	_ = r.Get(ctx, client.ObjectKeyFromObject(dw), dw)
	dw.Status.Phase = "Failed"
	dw.Status.Message = msg
	_ = r.Status().Update(ctx, dw)
	return reconcile.Result{RequeueAfter: time.Minute}, fmt.Errorf("%s", msg)
}

// pushHiclawSource downloads the hiclaw source tarball from GitHub and pushes
// the extracted contents to OSS under agents/{dwName}/hiclaw-source/.
func (r *DebugWorkerReconciler) pushHiclawSource(ctx context.Context, dwName, version string) error {
	if r.SourceRepoURL == "" {
		return fmt.Errorf("SourceRepoURL not configured")
	}

	// GitHub tarball URL: https://github.com/{owner}/{repo}/archive/refs/tags/{version}.tar.gz
	// For branches: https://github.com/{owner}/{repo}/archive/refs/heads/{branch}.tar.gz
	tarURL := fmt.Sprintf("%s/archive/refs/tags/%s.tar.gz", r.SourceRepoURL, version)

	// Download tarball
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download source tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try branch URL as fallback (version might be a branch name like "main")
		tarURL = fmt.Sprintf("%s/archive/refs/heads/%s.tar.gz", r.SourceRepoURL, version)
		req2, err := http.NewRequestWithContext(ctx, http.MethodGet, tarURL, nil)
		if err != nil {
			return fmt.Errorf("create fallback request: %w", err)
		}
		resp.Body.Close()
		resp, err = httpClient.Do(req2)
		if err != nil {
			return fmt.Errorf("download source tarball (branch fallback): %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("source tarball not found (tag or branch %q): HTTP %d", version, resp.StatusCode)
		}
	}

	// Extract to temp dir
	tmpDir, err := os.MkdirTemp("", "hiclaw-source-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := extractTarGz(resp.Body, tmpDir); err != nil {
		return fmt.Errorf("extract tarball: %w", err)
	}

	// GitHub tarballs extract to a single top-level directory like "hiclaw-v1.0.0/"
	// Find it and use it as the source root
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return fmt.Errorf("read temp dir: %w", err)
	}
	srcRoot := tmpDir
	if len(entries) == 1 && entries[0].IsDir() {
		srcRoot = filepath.Join(tmpDir, entries[0].Name())
	}

	// Mirror to OSS
	dstPrefix := fmt.Sprintf("agents/%s/hiclaw-source/", dwName)
	return r.OSS.Mirror(ctx, srcRoot+"/", dstPrefix, oss.MirrorOptions{Overwrite: true})
}

// extractTarGz extracts a .tar.gz stream to dstDir.
func extractTarGz(r io.Reader, dstDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dstDir, hdr.Name)

		// Guard against zip slip
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dstDir)+string(os.PathSeparator)) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			// Limit extracted file size to 10MB to avoid resource exhaustion
			if _, err := io.Copy(f, io.LimitReader(tr, 10<<20)); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }
