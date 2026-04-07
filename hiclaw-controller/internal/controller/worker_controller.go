package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/executor"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	finalizerName = "hiclaw.io/cleanup"
)

// WorkerReconciler reconciles Worker resources by calling existing bash scripts.
type WorkerReconciler struct {
	client.Client
	Executor *executor.Shell
	Packages *executor.PackageResolver
	Higress  *HigressClient

	// lastSpec tracks the last-processed spec per worker name (in memory).
	// Used by handleUpdate to detect real spec changes via DeepEqual.
	// Stored in memory (not annotations) to avoid r.Update() calls that
	// would overwrite spec changes made by the file-watcher during long
	// create-worker.sh runs (~30s).
	lastSpecMu sync.Mutex
	lastSpec   map[string]v1beta1.WorkerSpec
}

func (r *WorkerReconciler) getLastSpec(name string) (v1beta1.WorkerSpec, bool) {
	r.lastSpecMu.Lock()
	defer r.lastSpecMu.Unlock()
	if r.lastSpec == nil {
		return v1beta1.WorkerSpec{}, false
	}
	spec, ok := r.lastSpec[name]
	return spec, ok
}

func (r *WorkerReconciler) setLastSpec(name string, spec v1beta1.WorkerSpec) {
	r.lastSpecMu.Lock()
	defer r.lastSpecMu.Unlock()
	if r.lastSpec == nil {
		r.lastSpec = make(map[string]v1beta1.WorkerSpec)
	}
	r.lastSpec[name] = spec
}

func (r *WorkerReconciler) deleteLastSpec(name string) {
	r.lastSpecMu.Lock()
	defer r.lastSpecMu.Unlock()
	if r.lastSpec != nil {
		delete(r.lastSpec, name)
	}
}

func (r *WorkerReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	var worker v1beta1.Worker
	if err := r.Get(ctx, req.NamespacedName, &worker); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion with finalizer
	if !worker.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&worker, finalizerName) {
			if err := r.handleDelete(ctx, &worker); err != nil {
				logger.Error(err, "failed to delete worker", "name", worker.Name)
				return reconcile.Result{RequeueAfter: 30 * time.Second}, err
			}
			controllerutil.RemoveFinalizer(&worker, finalizerName)
			if err := r.Update(ctx, &worker); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&worker, finalizerName) {
		controllerutil.AddFinalizer(&worker, finalizerName)
		if err := r.Update(ctx, &worker); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Reconcile based on current phase
	switch worker.Status.Phase {
	case "", "Failed":
		return r.handleCreate(ctx, &worker)
	case "Pending":
		// Pending with an error message means a previous create attempt failed and
		// the "Failed" status update itself was lost (e.g. conflict). Retry creation.
		if worker.Status.Message != "" {
			return r.handleCreate(ctx, &worker)
		}
		return reconcile.Result{}, nil
	default:
		return r.handleUpdate(ctx, &worker)
	}
}

func (r *WorkerReconciler) handleCreate(ctx context.Context, w *v1beta1.Worker) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("creating worker", "name", w.Name)

	w.Status.Phase = "Pending"
	if err := r.Status().Update(ctx, w); err != nil {
		return reconcile.Result{}, err
	}

	// Resolve and deploy package if specified
	if w.Spec.Package != "" {
		extractedDir, err := r.Packages.ResolveAndExtract(ctx, w.Spec.Package, w.Name)
		if err != nil {
			// Refresh object to avoid conflict on the status update
			_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
			w.Status.Phase = "Failed"
			w.Status.Message = fmt.Sprintf("package resolve/extract failed: %v", err)
			r.Status().Update(ctx, w)
			return reconcile.Result{RequeueAfter: time.Minute}, err
		}
		if extractedDir != "" {
			if err := r.Packages.DeployToMinIO(ctx, extractedDir, w.Name, false); err != nil {
				_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
				w.Status.Phase = "Failed"
				w.Status.Message = fmt.Sprintf("package deploy failed: %v", err)
				r.Status().Update(ctx, w)
				return reconcile.Result{RequeueAfter: time.Minute}, err
			}
			logger.Info("package deployed", "name", w.Name, "dir", extractedDir)
		}
	}

	// Write inline configs (overrides package files if both set)
	if w.Spec.Identity != "" || w.Spec.Soul != "" || w.Spec.Agents != "" {
		agentDir := fmt.Sprintf("/root/hiclaw-fs/agents/%s", w.Name)
		if err := executor.WriteInlineConfigs(agentDir, w.Spec.Runtime, w.Spec.Identity, w.Spec.Soul, w.Spec.Agents); err != nil {
			w.Status.Phase = "Failed"
			w.Status.Message = fmt.Sprintf("write inline configs failed: %v", err)
			r.Status().Update(ctx, w)
			return reconcile.Result{RequeueAfter: time.Minute}, err
		}
		logger.Info("inline configs written", "name", w.Name)
	}

	// Build script arguments
	args := []string{
		"--name", w.Name,
	}
	if w.Spec.Model != "" {
		args = append(args, "--model", w.Spec.Model)
	}
	if w.Spec.Runtime != "" {
		args = append(args, "--runtime", w.Spec.Runtime)
	}
	if w.Spec.Image != "" {
		args = append(args, "--image", w.Spec.Image)
	}
	if len(w.Spec.Skills) > 0 {
		args = append(args, "--skills", joinStrings(w.Spec.Skills))
	}
	if len(w.Spec.McpServers) > 0 {
		args = append(args, "--mcp-servers", joinStrings(w.Spec.McpServers))
	}
	if w.Spec.ChannelPolicy != nil {
		if policyJSON, err := json.Marshal(w.Spec.ChannelPolicy); err == nil {
			args = append(args, "--channel-policy", string(policyJSON))
		}
	}

	// Check for team annotations (set by TeamReconciler)
	if role := w.Annotations["hiclaw.io/role"]; role != "" {
		args = append(args, "--role", role)
	}
	if team := w.Annotations["hiclaw.io/team"]; team != "" {
		args = append(args, "--team", team)
	}
	if leader := w.Annotations["hiclaw.io/team-leader"]; leader != "" {
		args = append(args, "--team-leader", leader)
	}

	result, err := r.Executor.Run(ctx,
		"/opt/hiclaw/agent/skills/worker-management/scripts/create-worker.sh",
		args...,
	)
	if err != nil {
		_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
		w.Status.Phase = "Failed"
		w.Status.Message = fmt.Sprintf("create-worker.sh failed: %v", err)
		r.Status().Update(ctx, w)
		return reconcile.Result{RequeueAfter: time.Minute}, err
	}

	// Record the spec we just processed (in memory, not annotation)
	r.setLastSpec(w.Name, w.Spec)

	// Expose ports via Higress gateway
	var exposedPorts []v1beta1.ExposedPortStatus
	if len(w.Spec.Expose) > 0 {
		var exposeErr error
		exposedPorts, exposeErr = ReconcileExpose(r.Higress, w.Name, w.Spec.Expose, nil)
		if exposeErr != nil {
			logger.Error(exposeErr, "failed to expose ports (non-fatal)", "name", w.Name)
		}
	}

	// Re-read object before status update to avoid stale resourceVersion.
	// The file-watcher may have updated the spec while create-worker.sh
	// was running (~30s), bumping the resourceVersion.
	if err := r.Get(ctx, client.ObjectKeyFromObject(w), w); err != nil {
		return reconcile.Result{}, err
	}
	w.Status.Phase = "Running"
	w.Status.MatrixUserID = result.MatrixUserID
	w.Status.RoomID = result.RoomID
	w.Status.Message = ""
	w.Status.ExposedPorts = exposedPorts
	if err := r.Status().Update(ctx, w); err != nil {
		logger.Error(err, "failed to update status after create (non-fatal)", "name", w.Name)
	}

	logger.Info("worker created", "name", w.Name, "roomID", result.RoomID)
	return reconcile.Result{}, nil
}

func (r *WorkerReconciler) handleUpdate(ctx context.Context, w *v1beta1.Worker) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	// Compare current spec (from informer, always fresh) with last-processed spec
	lastSpec, exists := r.getLastSpec(w.Name)
	if exists && reflect.DeepEqual(w.Spec, lastSpec) {
		return reconcile.Result{}, nil // no spec change
	}

	logger.Info("worker spec changed, updating configuration",
		"name", w.Name,
		"note", "This will overwrite all config (model, openclaw.json, skills, mcpServers). Memory is preserved. Skills are merged (existing updated, new added, old kept).",
	)

	w.Status.Phase = "Updating"
	w.Status.Message = "Updating worker configuration (memory preserved, skills merged)"
	if err := r.Status().Update(ctx, w); err != nil {
		return reconcile.Result{}, err
	}

	// 1. Resolve and deploy package if specified (overwrites SOUL.md, adds custom skills)
	packageDir := ""
	if w.Spec.Package != "" {
		extractedDir, err := r.Packages.ResolveAndExtract(ctx, w.Spec.Package, w.Name)
		if err != nil {
			_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
			w.Status.Phase = "Failed"
			w.Status.Message = fmt.Sprintf("package resolve/extract failed: %v", err)
			r.Status().Update(ctx, w)
			return reconcile.Result{RequeueAfter: time.Minute}, err
		}
		if extractedDir != "" {
			// Deploy package files to local + MinIO atomically (excludes memory to preserve existing state)
			if err := r.Packages.DeployToMinIO(ctx, extractedDir, w.Name, true); err != nil {
				_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
				w.Status.Phase = "Failed"
				w.Status.Message = fmt.Sprintf("package deploy failed: %v", err)
				r.Status().Update(ctx, w)
				return reconcile.Result{RequeueAfter: time.Minute}, err
			}
			packageDir = extractedDir
			logger.Info("package deployed for update", "name", w.Name, "dir", extractedDir)
		}
	}

	// Write inline configs (overrides package files if both set)
	if w.Spec.Identity != "" || w.Spec.Soul != "" || w.Spec.Agents != "" {
		agentDir := fmt.Sprintf("/root/hiclaw-fs/agents/%s", w.Name)
		if err := executor.WriteInlineConfigs(agentDir, w.Spec.Runtime, w.Spec.Identity, w.Spec.Soul, w.Spec.Agents); err != nil {
			_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
			w.Status.Phase = "Failed"
			w.Status.Message = fmt.Sprintf("write inline configs failed: %v", err)
			r.Status().Update(ctx, w)
			return reconcile.Result{RequeueAfter: time.Minute}, err
		}
		logger.Info("inline configs written for update", "name", w.Name)
	}

	// 2. Call update-worker-config.sh (handles credentials, openclaw.json, skills, MinIO sync)
	args := []string{"--name", w.Name}
	if w.Spec.Model != "" {
		args = append(args, "--model", w.Spec.Model)
	}
	if len(w.Spec.Skills) > 0 {
		args = append(args, "--skills", joinStrings(w.Spec.Skills))
	}
	if len(w.Spec.McpServers) > 0 {
		args = append(args, "--mcp-servers", joinStrings(w.Spec.McpServers))
	}
	if packageDir != "" {
		args = append(args, "--package-dir", packageDir)
	}
	if w.Spec.ChannelPolicy != nil {
		if policyJSON, err := json.Marshal(w.Spec.ChannelPolicy); err == nil {
			args = append(args, "--channel-policy", string(policyJSON))
		}
	}

	_, err := r.Executor.Run(ctx,
		"/opt/hiclaw/agent/skills/worker-management/scripts/update-worker-config.sh",
		args...,
	)
	if err != nil {
		w.Status.Phase = "Failed"
		w.Status.Message = fmt.Sprintf("update-worker-config.sh failed: %v", err)
		r.Status().Update(ctx, w)
		return reconcile.Result{RequeueAfter: time.Minute}, err
	}

	// Record the spec we just processed
	r.setLastSpec(w.Name, w.Spec)

	// Reconcile exposed ports
	exposedPorts, exposeErr := ReconcileExpose(r.Higress, w.Name, w.Spec.Expose, w.Status.ExposedPorts)
	if exposeErr != nil {
		logger.Error(exposeErr, "failed to reconcile exposed ports (non-fatal)", "name", w.Name)
	}

	// Re-read before status update
	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.Phase = "Running"
	w.Status.Message = "Configuration updated (memory preserved, skills merged)"
	w.Status.ExposedPorts = exposedPorts
	if err := r.Status().Update(ctx, w); err != nil {
		logger.Error(err, "failed to update status after update (non-fatal)", "name", w.Name)
	}

	logger.Info("worker updated", "name", w.Name)
	return reconcile.Result{}, nil
}

func (r *WorkerReconciler) handleDelete(ctx context.Context, w *v1beta1.Worker) error {
	logger := log.FromContext(ctx)
	logger.Info("deleting worker", "name", w.Name)

	r.deleteLastSpec(w.Name)

	// Clean up exposed ports from Higress
	// Use both status (persisted) and spec (current) to ensure cleanup
	currentExposed := w.Status.ExposedPorts
	if len(currentExposed) == 0 && len(w.Spec.Expose) > 0 {
		// Status wasn't persisted; derive from spec
		for _, ep := range w.Spec.Expose {
			currentExposed = append(currentExposed, v1beta1.ExposedPortStatus{
				Port:   ep.Port,
				Domain: domainForExpose(w.Name, ep.Port),
			})
		}
	}
	if len(currentExposed) > 0 {
		if _, err := ReconcileExpose(r.Higress, w.Name, nil, currentExposed); err != nil {
			logger.Error(err, "failed to clean up exposed ports (non-fatal)", "name", w.Name)
		}
	}

	// Delete container via lifecycle script
	_, err := r.Executor.RunSimple(ctx,
		"/opt/hiclaw/agent/skills/worker-management/scripts/lifecycle-worker.sh",
		"--action", "delete", "--worker", w.Name,
	)
	if err != nil {
		logger.Error(err, "failed to delete worker container (may already be removed)", "name", w.Name)
	}

	return nil
}

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ","
		}
		result += s
	}
	return result
}

func storagePrefix() string {
	prefix := "hiclaw/hiclaw-storage"
	// In production this comes from env, but for the reconciler
	// we use the same default as hiclaw-env.sh
	return prefix
}

// SetupWithManager registers the WorkerReconciler with the controller manager.
func (r *WorkerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Worker{}).
		Complete(r)
}
