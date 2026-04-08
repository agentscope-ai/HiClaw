package controller

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/agentconfig"
	authpkg "github.com/hiclaw/hiclaw-controller/internal/auth"
	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/executor"
	"github.com/hiclaw/hiclaw-controller/internal/gateway"
	"github.com/hiclaw/hiclaw-controller/internal/matrix"
	"github.com/hiclaw/hiclaw-controller/internal/oss"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	finalizerName = "hiclaw.io/cleanup"
)

// WorkerReconciler reconciles Worker resources using pure Go service clients.
type WorkerReconciler struct {
	client.Client

	// Go service clients
	Matrix      matrix.Client
	Gateway     gateway.Client
	OSS         oss.StorageClient
	OSSAdmin    oss.StorageAdminClient // nil in incluster/cloud mode
	AgentConfig *agentconfig.Generator
	Backend     *backend.Registry
	Creds       CredentialStore

	// K8s client for SA lifecycle + TokenRequest
	K8sClient kubernetes.Interface

	// Legacy support (kept during migration, removed in Step 9)
	Executor *executor.Shell
	Packages *executor.PackageResolver

	// Configuration
	KubeMode          string // "embedded" or "incluster"
	Namespace         string // K8s namespace for SAs and Pods
	AuthAudience      string // SA token audience (default: "hiclaw-controller")
	ManagerConfigPath string // embedded: ~/openclaw.json
	AgentFSDir        string // embedded: /root/hiclaw-fs/agents
	WorkerAgentDir    string // source for builtin agent files (AGENTS.md, skills/)
	RegistryPath      string // embedded: ~/workers-registry.json
	StoragePrefix     string // e.g. "hiclaw/hiclaw-storage"
	MatrixDomain      string // e.g. "matrix-local.hiclaw.io:8080"
	AdminUser         string // e.g. "admin"

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

	workerName := w.Name
	consumerName := "worker-" + workerName
	workerMatrixID := r.Matrix.UserID(workerName)
	managerMatrixID := r.Matrix.UserID("manager")
	adminMatrixID := r.Matrix.UserID(r.AdminUser)

	role := w.Annotations["hiclaw.io/role"]
	teamName := w.Annotations["hiclaw.io/team"]
	teamLeaderName := w.Annotations["hiclaw.io/team-leader"]
	isTeamWorker := teamLeaderName != ""

	// --- Step 0: Resolve and deploy package if specified ---
	if w.Spec.Package != "" {
		extractedDir, err := r.Packages.ResolveAndExtract(ctx, w.Spec.Package, workerName)
		if err != nil {
			return r.failCreate(ctx, w, fmt.Sprintf("package resolve/extract failed: %v", err))
		}
		if extractedDir != "" {
			if err := r.Packages.DeployToMinIO(ctx, extractedDir, workerName, false); err != nil {
				return r.failCreate(ctx, w, fmt.Sprintf("package deploy failed: %v", err))
			}
			logger.Info("package deployed", "name", workerName, "dir", extractedDir)
		}
	}

	// Write inline configs (overrides package files if both set)
	if w.Spec.Identity != "" || w.Spec.Soul != "" || w.Spec.Agents != "" {
		agentDir := fmt.Sprintf("%s/%s", r.AgentFSDir, workerName)
		if err := executor.WriteInlineConfigs(agentDir, w.Spec.Runtime, w.Spec.Identity, w.Spec.Soul, w.Spec.Agents); err != nil {
			return r.failCreate(ctx, w, fmt.Sprintf("write inline configs failed: %v", err))
		}
	}

	// --- Step 1: Load or generate credentials ---
	creds, err := r.Creds.Load(ctx, workerName)
	if err != nil {
		return r.failCreate(ctx, w, fmt.Sprintf("load credentials: %v", err))
	}
	if creds == nil {
		creds, err = GenerateCredentials()
		if err != nil {
			return r.failCreate(ctx, w, fmt.Sprintf("generate credentials: %v", err))
		}
	}

	// --- Step 2: Register Matrix account ---
	logger.Info("registering Matrix account", "name", workerName)
	userCreds, err := r.Matrix.EnsureUser(ctx, matrix.EnsureUserRequest{
		Username: workerName,
		Password: creds.MatrixPassword,
	})
	if err != nil {
		return r.failCreate(ctx, w, fmt.Sprintf("Matrix registration failed: %v", err))
	}
	creds.MatrixPassword = userCreds.Password

	// --- Step 3: Create MinIO user (embedded mode only) ---
	if r.OSSAdmin != nil {
		logger.Info("creating MinIO user", "name", workerName)
		if err := r.OSSAdmin.EnsureUser(ctx, workerName, creds.MinIOPassword); err != nil {
			return r.failCreate(ctx, w, fmt.Sprintf("MinIO user creation failed: %v", err))
		}
		if err := r.OSSAdmin.EnsurePolicy(ctx, oss.PolicyRequest{
			WorkerName: workerName,
			Bucket:     "hiclaw-storage",
			TeamName:   teamName,
		}); err != nil {
			return r.failCreate(ctx, w, fmt.Sprintf("MinIO policy creation failed: %v", err))
		}
	}

	// --- Step 4: Create Matrix room ---
	logger.Info("creating Matrix room", "name", workerName)

	var authorityID string
	if isTeamWorker {
		authorityID = r.Matrix.UserID(teamLeaderName)
	} else {
		authorityID = managerMatrixID
	}

	powerLevels := map[string]int{
		managerMatrixID: 100,
		adminMatrixID:   100,
		authorityID:     100,
		workerMatrixID:  0,
	}

	roomInfo, err := r.Matrix.CreateRoom(ctx, matrix.CreateRoomRequest{
		Name:           fmt.Sprintf("Worker: %s", workerName),
		Topic:          fmt.Sprintf("Communication channel for %s", workerName),
		Invite:         []string{adminMatrixID, authorityID, workerMatrixID},
		PowerLevels:    powerLevels,
		CreatorToken:   "",    // uses admin auth
		E2EE:           false, // controlled at config level
		ExistingRoomID: creds.RoomID,
	})
	if err != nil {
		return r.failCreate(ctx, w, fmt.Sprintf("Matrix room creation failed: %v", err))
	}
	creds.RoomID = roomInfo.RoomID
	logger.Info("Matrix room ready", "roomID", creds.RoomID, "created", roomInfo.Created)

	// Manager leaves team worker rooms (delegation boundary)
	if isTeamWorker && roomInfo.Created {
		if err := r.Matrix.LeaveRoom(ctx, creds.RoomID, ""); err != nil {
			logger.Error(err, "failed to leave team worker room (non-fatal)")
		}
	}

	// Persist credentials (including room ID) for retry idempotency
	if err := r.Creds.Save(ctx, workerName, creds); err != nil {
		logger.Error(err, "failed to persist credentials (non-fatal)")
	}

	// --- Step 5: Gateway consumer and authorization ---
	logger.Info("creating gateway consumer", "consumer", consumerName)
	consumerResult, err := r.Gateway.EnsureConsumer(ctx, gateway.ConsumerRequest{
		Name:          consumerName,
		CredentialKey: creds.GatewayKey,
	})
	if err != nil {
		return r.failCreate(ctx, w, fmt.Sprintf("gateway consumer creation failed: %v", err))
	}
	if consumerResult.APIKey != "" && consumerResult.APIKey != creds.GatewayKey {
		creds.GatewayKey = consumerResult.APIKey
		_ = r.Creds.Save(ctx, workerName, creds)
	}

	if err := r.Gateway.AuthorizeAIRoutes(ctx, consumerName); err != nil {
		return r.failCreate(ctx, w, fmt.Sprintf("AI route authorization failed: %v", err))
	}

	var authorizedMCPs []string
	if len(w.Spec.McpServers) > 0 {
		authorizedMCPs, err = r.Gateway.AuthorizeMCPServers(ctx, consumerName, w.Spec.McpServers)
		if err != nil {
			logger.Error(err, "MCP authorization partial failure (non-fatal)")
		}
	}

	// --- Step 6: Generate openclaw.json ---
	logger.Info("generating config", "name", workerName)

	var channelPolicy *agentconfig.ChannelPolicy
	if w.Spec.ChannelPolicy != nil {
		channelPolicy = &agentconfig.ChannelPolicy{
			GroupAllowExtra: w.Spec.ChannelPolicy.GroupAllowExtra,
			GroupDenyExtra:  w.Spec.ChannelPolicy.GroupDenyExtra,
			DMAllowExtra:    w.Spec.ChannelPolicy.DmAllowExtra,
			DMDenyExtra:     w.Spec.ChannelPolicy.DmDenyExtra,
		}
	}

	configJSON, err := r.AgentConfig.GenerateOpenClawConfig(agentconfig.WorkerConfigRequest{
		WorkerName:     workerName,
		MatrixToken:    userCreds.AccessToken,
		GatewayKey:     creds.GatewayKey,
		ModelName:      w.Spec.Model,
		TeamLeaderName: teamLeaderName,
		ChannelPolicy:  channelPolicy,
	})
	if err != nil {
		return r.failCreate(ctx, w, fmt.Sprintf("config generation failed: %v", err))
	}

	agentPrefix := fmt.Sprintf("agents/%s", workerName)
	if err := r.OSS.PutObject(ctx, agentPrefix+"/openclaw.json", configJSON); err != nil {
		return r.failCreate(ctx, w, fmt.Sprintf("config push to storage failed: %v", err))
	}

	// Generate mcporter-servers.json if MCP servers are authorized
	if len(authorizedMCPs) > 0 {
		mcporterJSON, err := r.AgentConfig.GenerateMcporterConfig(creds.GatewayKey, "", authorizedMCPs)
		if err != nil {
			logger.Error(err, "mcporter config generation failed (non-fatal)")
		} else if mcporterJSON != nil {
			if err := r.OSS.PutObject(ctx, agentPrefix+"/mcporter-servers.json", mcporterJSON); err != nil {
				logger.Error(err, "mcporter config push failed (non-fatal)")
			}
		}
	}

	// Write Matrix password to storage for E2EE re-login
	if err := r.OSS.PutObject(ctx, agentPrefix+"/credentials/matrix/password", []byte(creds.MatrixPassword)); err != nil {
		logger.Error(err, "failed to write Matrix password to storage (non-fatal)")
	}

	// --- Step 7: Update Manager groupAllowFrom (standalone workers only) ---
	if !isTeamWorker && r.ManagerConfigPath != "" {
		if err := UpdateManagerGroupAllowFrom(r.ManagerConfigPath, workerMatrixID, true); err != nil {
			logger.Error(err, "failed to update Manager groupAllowFrom (non-fatal)")
		}
	}

	// --- Step 8: Sync local agent files to storage ---
	logger.Info("syncing agent files to storage", "name", workerName)
	localAgentDir := fmt.Sprintf("%s/%s", r.AgentFSDir, workerName)
	if err := r.OSS.Mirror(ctx, localAgentDir+"/", agentPrefix+"/", oss.MirrorOptions{Overwrite: true}); err != nil {
		logger.Error(err, "agent file sync failed (non-fatal)")
	}

	// Merge builtin AGENTS.md section + inject coordination context (single read-write)
	if err := r.prepareAndPushAgentsMD(ctx, workerName, agentPrefix, role, teamName, teamLeaderName); err != nil {
		logger.Error(err, "AGENTS.md prepare failed (non-fatal)")
	}

	// Push builtin skills from worker-agent template
	if err := r.pushBuiltinSkills(ctx, workerName, agentPrefix); err != nil {
		logger.Error(err, "builtin skills push failed (non-fatal)")
	}

	// --- Step 8.5: Update workers-registry.json ---
	if r.RegistryPath != "" {
		if err := UpdateWorkersRegistry(r.RegistryPath, WorkerRegistryEntry{
			Name:         workerName,
			MatrixUserID: workerMatrixID,
			RoomID:       creds.RoomID,
			Runtime:      w.Spec.Runtime,
			Deployment:   "local",
			Skills:       w.Spec.Skills,
			Role:         role,
			TeamID:       nilIfEmpty(teamName),
			Image:        nilIfEmpty(w.Spec.Image),
		}); err != nil {
			logger.Error(err, "registry update failed (non-fatal)")
		}
	}

	// Push on-demand skills
	if len(w.Spec.Skills) > 0 && r.Packages != nil {
		if _, err := r.Executor.RunSimple(ctx,
			"/opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh",
			"--worker", workerName, "--no-notify",
		); err != nil {
			logger.Error(err, "skill push failed (non-fatal)")
		}
	}

	// --- Step 8.7: Ensure ServiceAccount ---
	logger.Info("ensuring service account", "name", workerName)
	if err := r.ensureServiceAccount(ctx, workerName); err != nil {
		return r.failCreate(ctx, w, fmt.Sprintf("ServiceAccount creation failed: %v", err))
	}

	// --- Step 9: Start Worker container ---
	logger.Info("starting worker container", "name", workerName)
	if r.Backend != nil {
		if wb := r.Backend.DetectWorkerBackend(ctx); wb != nil {
			workerEnv := r.buildWorkerEnv(workerName, creds, userCreds.AccessToken)
			saName := authpkg.SAName(authpkg.RoleWorker, workerName)
			createReq := backend.CreateRequest{
				Name:               workerName,
				Image:              w.Spec.Image,
				Runtime:            w.Spec.Runtime,
				Env:                workerEnv,
				ServiceAccountName: saName,
			}

			// Non-K8s backends need an explicit token (K8s uses projected volume).
			if wb.Name() != "k8s" {
				token, err := r.requestSAToken(ctx, workerName)
				if err != nil {
					logger.Error(err, "failed to request SA token (non-fatal, worker auth will fail)")
				}
				createReq.AuthToken = token
			}

			if _, err := wb.Create(ctx, createReq); err != nil {
				logger.Error(err, "worker container creation failed (non-fatal, worker can be started manually)")
			}
		} else {
			logger.Info("no worker backend available, worker needs manual start")
		}
	}

	// Record the spec we just processed (in memory)
	r.setLastSpec(w.Name, w.Spec)

	// Expose ports via gateway
	var exposedPorts []v1beta1.ExposedPortStatus
	if len(w.Spec.Expose) > 0 {
		var exposeErr error
		exposedPorts, exposeErr = ReconcileExpose(ctx, r.Gateway, workerName, w.Spec.Expose, nil)
		if exposeErr != nil {
			logger.Error(exposeErr, "failed to expose ports (non-fatal)")
		}
	}

	// Re-read before status update to avoid stale resourceVersion
	if err := r.Get(ctx, client.ObjectKeyFromObject(w), w); err != nil {
		return reconcile.Result{}, err
	}
	w.Status.Phase = "Running"
	w.Status.MatrixUserID = workerMatrixID
	w.Status.RoomID = creds.RoomID
	w.Status.Message = ""
	w.Status.ExposedPorts = exposedPorts
	if err := r.Status().Update(ctx, w); err != nil {
		logger.Error(err, "failed to update status after create (non-fatal)")
	}

	logger.Info("worker created", "name", workerName, "roomID", creds.RoomID)
	return reconcile.Result{}, nil
}

func (r *WorkerReconciler) failCreate(ctx context.Context, w *v1beta1.Worker, msg string) (reconcile.Result, error) {
	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.Phase = "Failed"
	w.Status.Message = msg
	r.Status().Update(ctx, w)
	return reconcile.Result{RequeueAfter: time.Minute}, fmt.Errorf("%s", msg)
}

// failUpdate records an update error without changing Phase away from "Running",
// so the next reconcile stays in handleUpdate instead of falling back to handleCreate.
func (r *WorkerReconciler) failUpdate(ctx context.Context, w *v1beta1.Worker, msg string) (reconcile.Result, error) {
	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.Phase = "Running"
	w.Status.Message = msg
	r.Status().Update(ctx, w)
	return reconcile.Result{RequeueAfter: time.Minute}, fmt.Errorf("%s", msg)
}

// prepareAndPushAgentsMD merges the builtin AGENTS.md section and injects
// coordination context in a single OSS read-write cycle (2 API calls instead of 4).
func (r *WorkerReconciler) prepareAndPushAgentsMD(ctx context.Context, workerName, agentPrefix, role, teamName, teamLeaderName string) error {
	builtinPath := r.WorkerAgentDir + "/AGENTS.md"
	builtinContent, err := os.ReadFile(builtinPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read builtin AGENTS.md: %w", err)
	}

	existing, _ := r.OSS.GetObject(ctx, agentPrefix+"/AGENTS.md")

	content := string(existing)
	if len(builtinContent) > 0 {
		content = agentconfig.MergeBuiltinSection(content, string(builtinContent))
	}

	coordCtx := agentconfig.CoordinationContext{
		WorkerName:     workerName,
		MatrixDomain:   r.MatrixDomain,
		TeamName:       teamName,
		TeamLeaderName: teamLeaderName,
	}
	if role == "team_leader" {
		coordCtx.Role = "team_leader"
	} else if teamLeaderName != "" {
		coordCtx.Role = "worker"
	} else {
		coordCtx.Role = "standalone"
	}
	content = agentconfig.InjectCoordinationContext(content, coordCtx)

	return r.OSS.PutObject(ctx, agentPrefix+"/AGENTS.md", []byte(content))
}

// Deprecated: use prepareAndPushAgentsMD for combined merge + inject operations.
func (r *WorkerReconciler) mergeAndPushAgentsMD(ctx context.Context, workerName, agentPrefix string) error {
	builtinPath := r.WorkerAgentDir + "/AGENTS.md"
	builtinContent, err := os.ReadFile(builtinPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read builtin AGENTS.md: %w", err)
	}

	existing, err := r.OSS.GetObject(ctx, agentPrefix+"/AGENTS.md")
	if err != nil {
		existing = nil
	}

	merged := agentconfig.MergeBuiltinSection(string(existing), string(builtinContent))
	return r.OSS.PutObject(ctx, agentPrefix+"/AGENTS.md", []byte(merged))
}

func (r *WorkerReconciler) injectCoordinationContext(ctx context.Context, workerName, agentPrefix, role, teamName, teamLeaderName string) error {
	existing, err := r.OSS.GetObject(ctx, agentPrefix+"/AGENTS.md")
	if err != nil {
		return fmt.Errorf("get AGENTS.md for context injection: %w", err)
	}

	coordCtx := agentconfig.CoordinationContext{
		WorkerName:     workerName,
		MatrixDomain:   r.MatrixDomain,
		TeamName:       teamName,
		TeamLeaderName: teamLeaderName,
	}

	if role == "team_leader" {
		coordCtx.Role = "team_leader"
	} else if teamLeaderName != "" {
		coordCtx.Role = "worker"
	} else {
		coordCtx.Role = "standalone"
	}

	result := agentconfig.InjectCoordinationContext(string(existing), coordCtx)
	return r.OSS.PutObject(ctx, agentPrefix+"/AGENTS.md", []byte(result))
}

func (r *WorkerReconciler) pushBuiltinSkills(ctx context.Context, workerName, agentPrefix string) error {
	skillsDir := r.WorkerAgentDir + "/skills"
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillName := entry.Name()
		src := skillsDir + "/" + skillName + "/"
		dst := agentPrefix + "/skills/" + skillName + "/"
		if err := r.OSS.Mirror(ctx, src, dst, oss.MirrorOptions{Overwrite: true}); err != nil {
			return fmt.Errorf("push skill %s: %w", skillName, err)
		}
	}
	return nil
}

func (r *WorkerReconciler) buildWorkerEnv(workerName string, creds *WorkerCredentials, matrixToken string) map[string]string {
	env := map[string]string{
		"HICLAW_WORKER_NAME":         workerName,
		"HICLAW_WORKER_GATEWAY_KEY":  creds.GatewayKey,
		"HICLAW_MATRIX_DOMAIN":       r.MatrixDomain,
		"HICLAW_WORKER_MATRIX_TOKEN": matrixToken,
		"HICLAW_FS_ACCESS_KEY":       workerName,
		"HICLAW_FS_SECRET_KEY":       creds.MinIOPassword,
		"OPENCLAW_DISABLE_BONJOUR":   "1",
		"OPENCLAW_MDNS_HOSTNAME":     "hiclaw-w-" + workerName,
		"HOME":                       "/root/hiclaw-fs/agents/" + workerName,
	}
	return env
}

func (r *WorkerReconciler) handleUpdate(ctx context.Context, w *v1beta1.Worker) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	lastSpec, exists := r.getLastSpec(w.Name)
	if exists && reflect.DeepEqual(w.Spec, lastSpec) {
		return reconcile.Result{}, nil
	}

	logger.Info("worker spec changed, updating configuration", "name", w.Name)
	workerName := w.Name
	consumerName := "worker-" + workerName
	agentPrefix := fmt.Sprintf("agents/%s", workerName)

	teamLeaderName := w.Annotations["hiclaw.io/team-leader"]
	role := w.Annotations["hiclaw.io/role"]
	teamName := w.Annotations["hiclaw.io/team"]

	w.Status.Phase = "Updating"
	w.Status.Message = "Updating worker configuration (memory preserved, skills merged)"
	if err := r.Status().Update(ctx, w); err != nil {
		return reconcile.Result{}, err
	}

	// --- Step 1: Load credentials ---
	creds, err := r.Creds.Load(ctx, workerName)
	if err != nil || creds == nil {
		return r.failUpdate(ctx, w, fmt.Sprintf("credentials not found for %s", workerName))
	}

	// Get fresh Matrix token
	matrixToken, err := r.Matrix.Login(ctx, workerName, creds.MatrixPassword)
	if err != nil {
		return r.failUpdate(ctx, w, fmt.Sprintf("Matrix login failed: %v", err))
	}

	// --- Step 2: Deploy package if specified ---
	if w.Spec.Package != "" {
		extractedDir, err := r.Packages.ResolveAndExtract(ctx, w.Spec.Package, workerName)
		if err != nil {
			return r.failUpdate(ctx, w, fmt.Sprintf("package resolve/extract failed: %v", err))
		}
		if extractedDir != "" {
			if err := r.Packages.DeployToMinIO(ctx, extractedDir, workerName, true); err != nil {
				return r.failUpdate(ctx, w, fmt.Sprintf("package deploy failed: %v", err))
			}
			logger.Info("package deployed for update", "name", workerName, "dir", extractedDir)
		}
	}

	// Write inline configs
	if w.Spec.Identity != "" || w.Spec.Soul != "" || w.Spec.Agents != "" {
		agentDir := fmt.Sprintf("%s/%s", r.AgentFSDir, workerName)
		if err := executor.WriteInlineConfigs(agentDir, w.Spec.Runtime, w.Spec.Identity, w.Spec.Soul, w.Spec.Agents); err != nil {
			return r.failUpdate(ctx, w, fmt.Sprintf("write inline configs failed: %v", err))
		}
	}

	// Re-merge builtin AGENTS.md section + inject coordination context (single read-write)
	if err := r.prepareAndPushAgentsMD(ctx, workerName, agentPrefix, role, teamName, teamLeaderName); err != nil {
		logger.Error(err, "AGENTS.md prepare failed (non-fatal)")
	}

	// --- Step 3: Regenerate openclaw.json ---
	var channelPolicy *agentconfig.ChannelPolicy
	if w.Spec.ChannelPolicy != nil {
		channelPolicy = &agentconfig.ChannelPolicy{
			GroupAllowExtra: w.Spec.ChannelPolicy.GroupAllowExtra,
			GroupDenyExtra:  w.Spec.ChannelPolicy.GroupDenyExtra,
			DMAllowExtra:    w.Spec.ChannelPolicy.DmAllowExtra,
			DMDenyExtra:     w.Spec.ChannelPolicy.DmDenyExtra,
		}
	}

	configJSON, err := r.AgentConfig.GenerateOpenClawConfig(agentconfig.WorkerConfigRequest{
		WorkerName:     workerName,
		MatrixToken:    matrixToken,
		GatewayKey:     creds.GatewayKey,
		ModelName:      w.Spec.Model,
		TeamLeaderName: teamLeaderName,
		ChannelPolicy:  channelPolicy,
	})
	if err != nil {
		return r.failUpdate(ctx, w, fmt.Sprintf("config generation failed: %v", err))
	}
	if err := r.OSS.PutObject(ctx, agentPrefix+"/openclaw.json", configJSON); err != nil {
		return r.failUpdate(ctx, w, fmt.Sprintf("config push failed: %v", err))
	}

	// --- Step 4: Push skills (additive) ---
	if len(w.Spec.Skills) > 0 && r.Packages != nil {
		if _, err := r.Executor.RunSimple(ctx,
			"/opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh",
			"--worker", workerName, "--no-notify",
		); err != nil {
			logger.Error(err, "skill push failed (non-fatal)")
		}
	}

	// --- Step 5: Reauthorize MCP servers if changed ---
	if len(w.Spec.McpServers) > 0 {
		authorizedMCPs, err := r.Gateway.AuthorizeMCPServers(ctx, consumerName, w.Spec.McpServers)
		if err != nil {
			logger.Error(err, "MCP reauthorization failed (non-fatal)")
		}
		if len(authorizedMCPs) > 0 {
			mcporterJSON, _ := r.AgentConfig.GenerateMcporterConfig(creds.GatewayKey, "", authorizedMCPs)
			if mcporterJSON != nil {
				if err := r.OSS.PutObject(ctx, agentPrefix+"/mcporter-servers.json", mcporterJSON); err != nil {
					logger.Error(err, "mcporter config push failed (non-fatal)")
				}
			}
		}
	}

	// --- Step 6: Sync config to storage (exclude memory) ---
	localAgentDir := fmt.Sprintf("%s/%s", r.AgentFSDir, workerName)
	if err := r.OSS.Mirror(ctx, localAgentDir+"/", agentPrefix+"/", oss.MirrorOptions{Overwrite: true}); err != nil {
		logger.Error(err, "config sync failed (non-fatal)")
	}

	// Update registry
	if r.RegistryPath != "" {
		_ = UpdateWorkersRegistry(r.RegistryPath, WorkerRegistryEntry{
			Name:         workerName,
			MatrixUserID: r.Matrix.UserID(workerName),
			RoomID:       creds.RoomID,
			Runtime:      w.Spec.Runtime,
			Deployment:   "local",
			Skills:       w.Spec.Skills,
			Role:         role,
			TeamID:       nilIfEmpty(teamName),
			Image:        nilIfEmpty(w.Spec.Image),
		})
	}

	r.setLastSpec(w.Name, w.Spec)

	exposedPorts, exposeErr := ReconcileExpose(ctx, r.Gateway, workerName, w.Spec.Expose, w.Status.ExposedPorts)
	if exposeErr != nil {
		logger.Error(exposeErr, "failed to reconcile exposed ports (non-fatal)")
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.Phase = "Running"
	w.Status.Message = "Configuration updated (memory preserved, skills merged)"
	w.Status.ExposedPorts = exposedPorts
	if err := r.Status().Update(ctx, w); err != nil {
		logger.Error(err, "failed to update status after update (non-fatal)")
	}

	logger.Info("worker updated", "name", workerName)
	return reconcile.Result{}, nil
}

func (r *WorkerReconciler) handleDelete(ctx context.Context, w *v1beta1.Worker) error {
	logger := log.FromContext(ctx)
	logger.Info("deleting worker", "name", w.Name)

	workerName := w.Name
	consumerName := "worker-" + workerName
	workerMatrixID := r.Matrix.UserID(workerName)
	isTeamWorker := w.Annotations["hiclaw.io/team-leader"] != ""

	r.deleteLastSpec(workerName)

	// --- Clean up exposed ports ---
	currentExposed := w.Status.ExposedPorts
	if len(currentExposed) == 0 && len(w.Spec.Expose) > 0 {
		for _, ep := range w.Spec.Expose {
			currentExposed = append(currentExposed, v1beta1.ExposedPortStatus{
				Port:   ep.Port,
				Domain: domainForExpose(workerName, ep.Port),
			})
		}
	}
	if len(currentExposed) > 0 {
		if _, err := ReconcileExpose(ctx, r.Gateway, workerName, nil, currentExposed); err != nil {
			logger.Error(err, "failed to clean up exposed ports (non-fatal)")
		}
	}

	// --- Delete worker container via backend ---
	if r.Backend != nil {
		if wb := r.Backend.DetectWorkerBackend(ctx); wb != nil {
			if err := wb.Delete(ctx, workerName); err != nil {
				logger.Error(err, "failed to delete worker container (may already be removed)")
			}
		}
	}

	// --- Deauthorize gateway ---
	if err := r.Gateway.DeauthorizeAIRoutes(ctx, consumerName); err != nil {
		logger.Error(err, "failed to deauthorize AI routes (non-fatal)")
	}
	if len(w.Spec.McpServers) > 0 {
		if err := r.Gateway.DeauthorizeMCPServers(ctx, consumerName, w.Spec.McpServers); err != nil {
			logger.Error(err, "failed to deauthorize MCP servers (non-fatal)")
		}
	}
	if err := r.Gateway.DeleteConsumer(ctx, consumerName); err != nil {
		logger.Error(err, "failed to delete gateway consumer (non-fatal)")
	}

	// --- Delete MinIO user (embedded mode) ---
	if r.OSSAdmin != nil {
		if err := r.OSSAdmin.DeleteUser(ctx, workerName); err != nil {
			logger.Error(err, "failed to delete MinIO user (non-fatal)")
		}
	}

	// --- Remove from Manager groupAllowFrom (standalone workers) ---
	if !isTeamWorker && r.ManagerConfigPath != "" {
		if err := UpdateManagerGroupAllowFrom(r.ManagerConfigPath, workerMatrixID, false); err != nil {
			logger.Error(err, "failed to update Manager groupAllowFrom (non-fatal)")
		}
	}

	// --- Remove from workers-registry.json ---
	if r.RegistryPath != "" {
		if err := RemoveFromWorkersRegistry(r.RegistryPath, workerName); err != nil {
			logger.Error(err, "failed to remove from workers registry (non-fatal)")
		}
	}

	// --- Clean up OSS agent data ---
	agentPrefix := fmt.Sprintf("agents/%s/", workerName)
	if err := r.OSS.DeletePrefix(ctx, agentPrefix); err != nil {
		logger.Error(err, "failed to clean up OSS agent data (non-fatal)")
	}

	// --- Delete credentials file ---
	if err := r.Creds.Delete(ctx, workerName); err != nil {
		logger.Error(err, "failed to delete credentials (non-fatal)")
	}

	// --- Delete ServiceAccount and RoleBinding ---
	if err := r.deleteServiceAccount(ctx, workerName); err != nil {
		logger.Error(err, "failed to delete ServiceAccount (non-fatal)")
	}

	logger.Info("worker deleted", "name", workerName)
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ensureServiceAccount creates a SA + RoleBinding for the worker if it doesn't exist.
func (r *WorkerReconciler) ensureServiceAccount(ctx context.Context, workerName string) error {
	if r.K8sClient == nil {
		return nil
	}
	saName := authpkg.SAName(authpkg.RoleWorker, workerName)
	ns := r.Namespace

	_, err := r.K8sClient.CoreV1().ServiceAccounts(ns).Get(ctx, saName, metav1.GetOptions{})
	if err == nil {
		return nil // already exists
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get SA %s: %w", saName, err)
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: ns,
			Labels: map[string]string{
				"app":              "hiclaw-worker",
				"hiclaw.io/worker": workerName,
			},
		},
	}
	if _, err := r.K8sClient.CoreV1().ServiceAccounts(ns).Create(ctx, sa, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create SA %s: %w", saName, err)
		}
	}

	rbName := saName + "-rb"
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbName,
			Namespace: ns,
			Labels: map[string]string{
				"app":              "hiclaw-worker",
				"hiclaw.io/worker": workerName,
			},
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      saName,
			Namespace: ns,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "hiclaw-worker",
		},
	}
	if _, err := r.K8sClient.RbacV1().RoleBindings(ns).Create(ctx, rb, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create RoleBinding %s: %w", rbName, err)
		}
	}

	return nil
}

// deleteServiceAccount removes the SA + RoleBinding for the worker.
func (r *WorkerReconciler) deleteServiceAccount(ctx context.Context, workerName string) error {
	if r.K8sClient == nil {
		return nil
	}
	saName := authpkg.SAName(authpkg.RoleWorker, workerName)
	ns := r.Namespace

	rbName := saName + "-rb"
	_ = r.K8sClient.RbacV1().RoleBindings(ns).Delete(ctx, rbName, metav1.DeleteOptions{})
	err := r.K8sClient.CoreV1().ServiceAccounts(ns).Delete(ctx, saName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// requestSAToken issues a short-lived SA token for non-K8s backends (Docker/SAE).
func (r *WorkerReconciler) requestSAToken(ctx context.Context, workerName string) (string, error) {
	if r.K8sClient == nil {
		return "", nil
	}
	saName := authpkg.SAName(authpkg.RoleWorker, workerName)
	audience := r.AuthAudience
	if audience == "" {
		audience = authpkg.DefaultAudience
	}
	expSeconds := int64(86400) // 24h; worker should refresh before expiry

	tokenReq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences:         []string{audience},
			ExpirationSeconds: &expSeconds,
		},
	}

	result, err := r.K8sClient.CoreV1().ServiceAccounts(r.Namespace).CreateToken(
		ctx, saName, tokenReq, metav1.CreateOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("request SA token for %s: %w", workerName, err)
	}
	return result.Status.Token, nil
}

// SetupWithManager registers the WorkerReconciler with the controller manager.
func (r *WorkerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Worker{}).
		Complete(r)
}
