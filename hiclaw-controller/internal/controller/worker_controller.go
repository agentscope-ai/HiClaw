package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	authpkg "github.com/hiclaw/hiclaw-controller/internal/auth"
	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	finalizerName       = "hiclaw.io/cleanup"
	reconcileInterval   = 5 * time.Minute
	reconcileRetryDelay = 30 * time.Second
)

// WorkerReconciler reconciles Worker resources using Service-layer orchestration.
type WorkerReconciler struct {
	client.Client

	Provisioner *service.Provisioner
	Deployer    *service.Deployer
	Backend     *backend.Registry
	EnvBuilder  *service.WorkerEnvBuilder
	Legacy      *service.LegacyCompat // nil in incluster mode
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
		if worker.Status.Message != "" {
			return r.handleCreate(ctx, &worker)
		}
		return reconcile.Result{}, nil
	default:
		// For provisioned workers, reconcile desired lifecycle state
		desired := worker.Spec.DesiredState()
		result, err := r.reconcileDesiredState(ctx, &worker, desired)
		if err != nil || result.RequeueAfter > 0 {
			return result, err
		}

		if worker.Generation == worker.Status.ObservedGeneration {
			return reconcile.Result{RequeueAfter: reconcileInterval}, nil
		}
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
	role := w.Annotations["hiclaw.io/role"]
	teamName := w.Annotations["hiclaw.io/team"]
	teamLeaderName := w.Annotations["hiclaw.io/team-leader"]
	teamAdminMatrixID := w.Annotations["hiclaw.io/team-admin-id"]
	isTeamWorker := teamLeaderName != ""

	// --- Phase 1: Package + inline configs ---
	if err := r.Deployer.DeployPackage(ctx, workerName, w.Spec.Package, false); err != nil {
		return r.failCreate(ctx, w, err.Error())
	}
	if err := r.Deployer.WriteInlineConfigs(workerName, w.Spec); err != nil {
		return r.failCreate(ctx, w, fmt.Sprintf("write inline configs failed: %v", err))
	}

	// --- Phase 2: Provision infrastructure ---
	provResult, err := r.Provisioner.ProvisionWorker(ctx, service.WorkerProvisionRequest{
		Name:           workerName,
		Role:           roleForAnnotations(role, teamLeaderName),
		TeamName:       teamName,
		TeamLeaderName: teamLeaderName,
		McpServers:     w.Spec.McpServers,
	})
	if err != nil {
		return r.failCreate(ctx, w, err.Error())
	}

	// --- Phase 3: Deploy configuration ---
	if err := r.Deployer.DeployWorkerConfig(ctx, service.WorkerDeployRequest{
		Name:              workerName,
		Spec:              w.Spec,
		Role:              roleForAnnotations(role, teamLeaderName),
		TeamName:          teamName,
		TeamLeaderName:    teamLeaderName,
		MatrixToken:       provResult.MatrixToken,
		GatewayKey:        provResult.GatewayKey,
		MatrixPassword:    provResult.MatrixPassword,
		AuthorizedMCPs:    provResult.AuthorizedMCPs,
		TeamAdminMatrixID: teamAdminMatrixID,
	}); err != nil {
		return r.failCreate(ctx, w, err.Error())
	}

	// --- Phase 4: Legacy compat ---
	if r.Legacy != nil && r.Legacy.Enabled() {
		if !isTeamWorker {
			if err := r.Legacy.UpdateManagerGroupAllowFrom(provResult.MatrixUserID, true); err != nil {
				logger.Error(err, "failed to update Manager groupAllowFrom (non-fatal)")
			}
		}
		if err := r.Legacy.UpdateWorkersRegistry(service.WorkerRegistryEntry{
			Name:         workerName,
			MatrixUserID: provResult.MatrixUserID,
			RoomID:       provResult.RoomID,
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

	// --- Phase 5: On-demand skills ---
	if err := r.Deployer.PushOnDemandSkills(ctx, workerName, w.Spec.Skills); err != nil {
		logger.Error(err, "skill push failed (non-fatal)")
	}

	// --- Phase 6: ServiceAccount + Container start ---
	logger.Info("ensuring service account", "name", workerName)
	if err := r.Provisioner.EnsureServiceAccount(ctx, workerName); err != nil {
		return r.failCreate(ctx, w, fmt.Sprintf("ServiceAccount creation failed: %v", err))
	}

	logger.Info("starting worker container", "name", workerName)
	if r.Backend != nil {
		if wb := r.Backend.DetectWorkerBackend(ctx); wb != nil {
			workerEnv := r.EnvBuilder.Build(workerName, provResult)
			saName := authpkg.SAName(authpkg.RoleWorker, workerName)
			createReq := backend.CreateRequest{
				Name:               workerName,
				Image:              w.Spec.Image,
				Runtime:            w.Spec.Runtime,
				Env:                workerEnv,
				ServiceAccountName: saName,
			}
			if wb.Name() != "k8s" {
				token, err := r.Provisioner.RequestSAToken(ctx, workerName)
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

	// --- Phase 7: Expose + Status ---
	var exposedPorts []v1beta1.ExposedPortStatus
	if len(w.Spec.Expose) > 0 {
		var exposeErr error
		exposedPorts, exposeErr = r.Provisioner.ReconcileExpose(ctx, workerName, w.Spec.Expose, nil)
		if exposeErr != nil {
			logger.Error(exposeErr, "failed to expose ports (non-fatal)")
		}
	}

	if err := r.Get(ctx, client.ObjectKeyFromObject(w), w); err != nil {
		return reconcile.Result{}, err
	}
	w.Status.ObservedGeneration = w.Generation
	w.Status.Phase = "Running"
	w.Status.MatrixUserID = provResult.MatrixUserID
	w.Status.RoomID = provResult.RoomID
	w.Status.Message = ""
	w.Status.ExposedPorts = exposedPorts
	if err := r.Status().Update(ctx, w); err != nil {
		logger.Error(err, "failed to update status after create (non-fatal)")
	}

	logger.Info("worker created", "name", workerName, "roomID", provResult.RoomID)
	return reconcile.Result{}, nil
}

func (r *WorkerReconciler) handleUpdate(ctx context.Context, w *v1beta1.Worker) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("worker spec changed, updating configuration", "name", w.Name)
	workerName := w.Name
	consumerName := "worker-" + workerName
	role := w.Annotations["hiclaw.io/role"]
	teamName := w.Annotations["hiclaw.io/team"]
	teamLeaderName := w.Annotations["hiclaw.io/team-leader"]
	teamAdminMatrixID := w.Annotations["hiclaw.io/team-admin-id"]

	w.Status.Phase = "Updating"
	w.Status.Message = "Updating worker configuration (memory preserved, skills merged)"
	if err := r.Status().Update(ctx, w); err != nil {
		return reconcile.Result{}, err
	}

	// --- Phase 1: Package + inline configs ---
	if err := r.Deployer.DeployPackage(ctx, workerName, w.Spec.Package, true); err != nil {
		return r.failUpdate(ctx, w, err.Error())
	}
	if err := r.Deployer.WriteInlineConfigs(workerName, w.Spec); err != nil {
		return r.failUpdate(ctx, w, fmt.Sprintf("write inline configs failed: %v", err))
	}

	// --- Phase 2: Refresh credentials + MCP auth ---
	refreshResult, err := r.Provisioner.RefreshCredentials(ctx, workerName)
	if err != nil {
		return r.failUpdate(ctx, w, err.Error())
	}

	var authorizedMCPs []string
	if len(w.Spec.McpServers) > 0 {
		authorizedMCPs, err = r.Provisioner.ReconcileMCPAuth(ctx, consumerName, w.Spec.McpServers)
		if err != nil {
			logger.Error(err, "MCP reauthorization failed (non-fatal)")
		}
	}

	// --- Phase 3: Deploy configuration ---
	if err := r.Deployer.DeployWorkerConfig(ctx, service.WorkerDeployRequest{
		Name:              workerName,
		Spec:              w.Spec,
		Role:              roleForAnnotations(role, teamLeaderName),
		TeamName:          teamName,
		TeamLeaderName:    teamLeaderName,
		MatrixToken:       refreshResult.MatrixToken,
		GatewayKey:        refreshResult.GatewayKey,
		MatrixPassword:    refreshResult.MatrixPassword,
		AuthorizedMCPs:    authorizedMCPs,
		TeamAdminMatrixID: teamAdminMatrixID,
		IsUpdate:          true,
	}); err != nil {
		return r.failUpdate(ctx, w, err.Error())
	}

	// --- Phase 4: On-demand skills ---
	if err := r.Deployer.PushOnDemandSkills(ctx, workerName, w.Spec.Skills); err != nil {
		logger.Error(err, "skill push failed (non-fatal)")
	}

	// --- Phase 5: Legacy compat ---
	if r.Legacy != nil && r.Legacy.Enabled() {
		_ = r.Legacy.UpdateWorkersRegistry(service.WorkerRegistryEntry{
			Name:         workerName,
			MatrixUserID: r.Provisioner.MatrixUserID(workerName),
			RoomID:       w.Status.RoomID,
			Runtime:      w.Spec.Runtime,
			Deployment:   "local",
			Skills:       w.Spec.Skills,
			Role:         role,
			TeamID:       nilIfEmpty(teamName),
			Image:        nilIfEmpty(w.Spec.Image),
		})
	}

	// --- Phase 6: Expose + Status ---
	exposedPorts, exposeErr := r.Provisioner.ReconcileExpose(ctx, workerName, w.Spec.Expose, w.Status.ExposedPorts)
	if exposeErr != nil {
		logger.Error(exposeErr, "failed to reconcile exposed ports (non-fatal)")
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.ObservedGeneration = w.Generation
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
	isTeamWorker := w.Annotations["hiclaw.io/team-leader"] != ""

	// --- Phase 1: Deprovision infrastructure ---
	if err := r.Provisioner.DeprovisionWorker(ctx, service.WorkerDeprovisionRequest{
		Name:         workerName,
		IsTeamWorker: isTeamWorker,
		McpServers:   w.Spec.McpServers,
		ExposedPorts: w.Status.ExposedPorts,
		ExposeSpec:   w.Spec.Expose,
	}); err != nil {
		logger.Error(err, "deprovision failed (non-fatal)")
	}

	// --- Phase 2: Delete worker container ---
	if r.Backend != nil {
		if wb := r.Backend.DetectWorkerBackend(ctx); wb != nil {
			if err := wb.Delete(ctx, workerName); err != nil {
				logger.Error(err, "failed to delete worker container (may already be removed)")
			}
		}
	}

	// --- Phase 3: Legacy compat ---
	if r.Legacy != nil && r.Legacy.Enabled() {
		workerMatrixID := r.Provisioner.MatrixUserID(workerName)
		if !isTeamWorker {
			if err := r.Legacy.UpdateManagerGroupAllowFrom(workerMatrixID, false); err != nil {
				logger.Error(err, "failed to update Manager groupAllowFrom (non-fatal)")
			}
		}
		if err := r.Legacy.RemoveFromWorkersRegistry(workerName); err != nil {
			logger.Error(err, "failed to remove from workers registry (non-fatal)")
		}
	}

	// --- Phase 4: Clean up OSS + credentials + SA ---
	if err := r.Deployer.CleanupOSSData(ctx, workerName); err != nil {
		logger.Error(err, "failed to clean up OSS agent data (non-fatal)")
	}
	if err := r.Provisioner.DeleteCredentials(ctx, workerName); err != nil {
		logger.Error(err, "failed to delete credentials (non-fatal)")
	}
	if err := r.Provisioner.DeleteServiceAccount(ctx, workerName); err != nil {
		logger.Error(err, "failed to delete ServiceAccount (non-fatal)")
	}

	logger.Info("worker deleted", "name", workerName)
	return nil
}

func (r *WorkerReconciler) failCreate(ctx context.Context, w *v1beta1.Worker, msg string) (reconcile.Result, error) {
	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.Phase = "Failed"
	w.Status.Message = msg
	r.Status().Update(ctx, w)
	return reconcile.Result{RequeueAfter: time.Minute}, fmt.Errorf("%s", msg)
}

func (r *WorkerReconciler) failUpdate(ctx context.Context, w *v1beta1.Worker, msg string) (reconcile.Result, error) {
	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.Phase = "Running"
	w.Status.Message = msg
	r.Status().Update(ctx, w)
	return reconcile.Result{RequeueAfter: time.Minute}, fmt.Errorf("%s", msg)
}

// --- Desired state reconciliation ---

// reconcileDesiredState compares the desired lifecycle state (spec.state) with
// the actual backend state and takes corrective action when drift is detected.
func (r *WorkerReconciler) reconcileDesiredState(ctx context.Context, w *v1beta1.Worker, desired string) (reconcile.Result, error) {
	// If current phase already matches desired state, nothing to do.
	// This also avoids unnecessary backend Status calls on every reconcile.
	if w.Status.Phase == desired {
		return reconcile.Result{}, nil
	}

	logger := log.FromContext(ctx)
	logger.Info("reconciling desired state", "name", w.Name, "current", w.Status.Phase, "desired", desired)

	switch desired {
	case "Running":
		return r.ensureWorkerRunning(ctx, w)
	case "Sleeping":
		return r.ensureWorkerSleeping(ctx, w)
	case "Stopped":
		return r.ensureWorkerStopped(ctx, w)
	default:
		logger.Info("unknown desired state, ignoring", "state", desired)
		return reconcile.Result{}, nil
	}
}

// ensureWorkerRunning wakes a worker from Sleeping or Stopped state.
func (r *WorkerReconciler) ensureWorkerRunning(ctx context.Context, w *v1beta1.Worker) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	if r.Backend == nil {
		return reconcile.Result{}, nil
	}
	wb := r.Backend.DetectWorkerBackend(ctx)
	if wb == nil {
		return reconcile.Result{}, nil
	}

	result, err := wb.Status(ctx, w.Name)
	if err != nil {
		logger.Error(err, "failed to check backend status")
		return reconcile.Result{RequeueAfter: reconcileRetryDelay}, nil
	}

	switch {
	case result.Status == backend.StatusRunning || result.Status == backend.StatusStarting:
		// Already running — just update status.phase
	case result.Status == backend.StatusStopped && wb.Name() == "docker":
		// Docker Sleeping: container exists but stopped → docker start
		if err := wb.Start(ctx, w.Name); err != nil {
			return r.failLifecycle(ctx, w, fmt.Sprintf("start failed: %v", err))
		}
	default:
		// Container removed (Docker Stopped) or pod deleted (K8s) → recreate
		if err := r.recreateWorkerContainer(ctx, w, wb); err != nil {
			return r.failLifecycle(ctx, w, fmt.Sprintf("recreate failed: %v", err))
		}
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.Phase = "Running"
	w.Status.Message = ""
	r.Status().Update(ctx, w)

	logger.Info("worker running", "name", w.Name)
	return reconcile.Result{}, nil
}

// ensureWorkerSleeping stops a worker: docker stop (container kept) or delete pod (K8s).
func (r *WorkerReconciler) ensureWorkerSleeping(ctx context.Context, w *v1beta1.Worker) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	if r.Backend == nil {
		return reconcile.Result{}, nil
	}
	wb := r.Backend.DetectWorkerBackend(ctx)
	if wb == nil {
		return reconcile.Result{}, nil
	}

	if err := wb.Stop(ctx, w.Name); err != nil && !errors.Is(err, backend.ErrNotFound) {
		return r.failLifecycle(ctx, w, fmt.Sprintf("sleep failed: %v", err))
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.Phase = "Sleeping"
	w.Status.Message = ""
	r.Status().Update(ctx, w)

	logger.Info("worker sleeping", "name", w.Name)
	return reconcile.Result{}, nil
}

// ensureWorkerStopped stops and removes a worker: docker stop+rm or delete pod (K8s).
func (r *WorkerReconciler) ensureWorkerStopped(ctx context.Context, w *v1beta1.Worker) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	if r.Backend == nil {
		return reconcile.Result{}, nil
	}
	wb := r.Backend.DetectWorkerBackend(ctx)
	if wb == nil {
		return reconcile.Result{}, nil
	}

	_ = wb.Stop(ctx, w.Name) // best-effort stop first
	if err := wb.Delete(ctx, w.Name); err != nil && !errors.Is(err, backend.ErrNotFound) {
		return r.failLifecycle(ctx, w, fmt.Sprintf("stop failed: %v", err))
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.Phase = "Stopped"
	w.Status.Message = ""
	r.Status().Update(ctx, w)

	logger.Info("worker stopped", "name", w.Name)
	return reconcile.Result{}, nil
}

// recreateWorkerContainer rebuilds a worker container/pod from the Worker CR spec
// and stored credentials. Used when waking from Stopped (container removed) or
// K8s (pod deleted).
func (r *WorkerReconciler) recreateWorkerContainer(ctx context.Context, w *v1beta1.Worker, wb backend.WorkerBackend) error {
	logger := log.FromContext(ctx)
	workerName := w.Name

	// Refresh credentials from persisted store
	refreshResult, err := r.Provisioner.RefreshCredentials(ctx, workerName)
	if err != nil {
		return fmt.Errorf("refresh credentials: %w", err)
	}

	// Ensure ServiceAccount still exists
	if err := r.Provisioner.EnsureServiceAccount(ctx, workerName); err != nil {
		logger.Error(err, "ServiceAccount ensure during recreate (non-fatal)")
	}

	// Build env from stored credentials
	workerEnv := r.EnvBuilder.Build(workerName, &service.WorkerProvisionResult{
		MatrixToken:    refreshResult.MatrixToken,
		GatewayKey:     refreshResult.GatewayKey,
		MinIOPassword:  refreshResult.MinIOPassword,
		MatrixPassword: refreshResult.MatrixPassword,
	})

	// Build create request
	saName := authpkg.SAName(authpkg.RoleWorker, workerName)
	createReq := backend.CreateRequest{
		Name:               workerName,
		Image:              w.Spec.Image,
		Runtime:            w.Spec.Runtime,
		Env:                workerEnv,
		ServiceAccountName: saName,
	}
	if wb.Name() != "k8s" {
		token, err := r.Provisioner.RequestSAToken(ctx, workerName)
		if err != nil {
			logger.Error(err, "SA token request during recreate (non-fatal)")
		}
		createReq.AuthToken = token
	}

	if _, err := wb.Create(ctx, createReq); err != nil {
		return fmt.Errorf("container recreate: %w", err)
	}
	return nil
}

func (r *WorkerReconciler) failLifecycle(ctx context.Context, w *v1beta1.Worker, msg string) (reconcile.Result, error) {
	_ = r.Get(ctx, client.ObjectKeyFromObject(w), w)
	w.Status.Message = msg
	r.Status().Update(ctx, w)
	return reconcile.Result{RequeueAfter: time.Minute}, fmt.Errorf("%s", msg)
}

func (r *WorkerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Worker{})

	// In-cluster mode: watch Pod events with hiclaw.io/worker label,
	// map to corresponding Worker CR for immediate reconcile on pod failure/deletion.
	if r.Backend != nil {
		if wb := r.Backend.DetectWorkerBackend(context.Background()); wb != nil && wb.Name() == "k8s" {
			builder = builder.Watches(
				&corev1.Pod{},
				handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
					workerName := obj.GetLabels()["hiclaw.io/worker"]
					if workerName == "" {
						return nil
					}
					return []reconcile.Request{
						{NamespacedName: client.ObjectKey{Name: workerName}},
					}
				}),
			)
		}
	}

	return builder.Complete(r)
}

// --- Package-level helpers ---

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func roleForAnnotations(role, teamLeaderName string) string {
	if role == "team_leader" {
		return "team_leader"
	}
	if teamLeaderName != "" {
		return "worker"
	}
	return "standalone"
}
