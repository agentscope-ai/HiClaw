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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const managerPodPrefix = "hiclaw-manager-"

// managerContainerName returns the container/pod name for a Manager.
// The "default" manager uses "hiclaw-manager" (no suffix) for compatibility
// with install/uninstall scripts; other managers use "hiclaw-manager-{name}".
func managerContainerName(name string) string {
	if name == "default" {
		return "hiclaw-manager"
	}
	return managerPodPrefix + name
}

// ManagerEmbeddedConfig holds embedded-mode settings for the Manager Agent
// container (workspace mount, host share, extra env from the controller's env).
type ManagerEmbeddedConfig struct {
	WorkspaceDir       string            // host path for /root/manager-workspace
	HostShareDir       string            // host path for /host-share
	ExtraEnv           map[string]string // infrastructure env vars forwarded to agent
	ManagerConsolePort string            // host port for manager console (default: 18888)
}

// ManagerReconciler reconciles Manager resources.
type ManagerReconciler struct {
	client.Client

	Provisioner      *service.Provisioner
	Deployer         *service.Deployer
	Backend          *backend.Registry
	EnvBuilder       *service.WorkerEnvBuilder
	ManagerResources *backend.ResourceRequirements
	EmbeddedConfig   *ManagerEmbeddedConfig // non-nil in embedded mode only
}

func (r *ManagerReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	var mgr v1beta1.Manager
	if err := r.Get(ctx, req.NamespacedName, &mgr); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !mgr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&mgr, finalizerName) {
			if err := r.handleDelete(ctx, &mgr); err != nil {
				logger.Error(err, "failed to delete manager", "name", mgr.Name)
				return reconcile.Result{RequeueAfter: 30 * time.Second}, err
			}
			controllerutil.RemoveFinalizer(&mgr, finalizerName)
			if err := r.Update(ctx, &mgr); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&mgr, finalizerName) {
		controllerutil.AddFinalizer(&mgr, finalizerName)
		if err := r.Update(ctx, &mgr); err != nil {
			return reconcile.Result{}, err
		}
	}

	switch mgr.Status.Phase {
	case "", "Failed":
		return r.handleCreate(ctx, &mgr)
	case "Pending":
		if mgr.Status.Message != "" {
			return r.handleCreate(ctx, &mgr)
		}
		return reconcile.Result{}, nil
	default:
		// For provisioned managers, reconcile desired lifecycle state
		desired := mgr.Spec.DesiredState()
		result, err := r.reconcileDesiredState(ctx, &mgr, desired)
		if err != nil || result.RequeueAfter > 0 {
			return result, err
		}

		if mgr.Generation == mgr.Status.ObservedGeneration {
			return reconcile.Result{RequeueAfter: reconcileInterval}, nil
		}
		return r.handleUpdate(ctx, &mgr)
	}
}

func (r *ManagerReconciler) handleCreate(ctx context.Context, m *v1beta1.Manager) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("creating manager", "name", m.Name)

	m.Status.Phase = "Pending"
	if err := r.Status().Update(ctx, m); err != nil {
		return reconcile.Result{}, err
	}

	managerName := m.Name

	// --- Phase 1: Package (if any) ---
	if err := r.Deployer.DeployPackage(ctx, managerName, m.Spec.Package, false); err != nil {
		return r.failManager(ctx, m, err.Error())
	}

	// --- Phase 2: Provision infrastructure ---
	provResult, err := r.Provisioner.ProvisionManager(ctx, service.ManagerProvisionRequest{
		Name:       managerName,
		McpServers: m.Spec.McpServers,
	})
	if err != nil {
		return r.failManager(ctx, m, err.Error())
	}

	// --- Phase 3: Deploy configuration to OSS ---
	if err := r.Deployer.DeployManagerConfig(ctx, service.ManagerDeployRequest{
		Name:           managerName,
		Spec:           m.Spec,
		MatrixToken:    provResult.MatrixToken,
		GatewayKey:     provResult.GatewayKey,
		MatrixPassword: provResult.MatrixPassword,
		AuthorizedMCPs: provResult.AuthorizedMCPs,
	}); err != nil {
		return r.failManager(ctx, m, err.Error())
	}

	// --- Phase 4: On-demand skills ---
	if err := r.Deployer.PushOnDemandSkills(ctx, managerName, m.Spec.Skills); err != nil {
		logger.Error(err, "skill push failed (non-fatal)")
	}

	// --- Phase 5: ServiceAccount ---
	logger.Info("ensuring manager service account", "name", managerName)
	if err := r.Provisioner.EnsureManagerServiceAccount(ctx, managerName); err != nil {
		return r.failManager(ctx, m, fmt.Sprintf("ServiceAccount creation failed: %v", err))
	}

	// --- Phase 6: Container start ---
	logger.Info("starting manager container", "name", managerName)
	if r.Backend != nil {
		if wb := r.Backend.DetectWorkerBackend(ctx); wb != nil {
			managerEnv := r.EnvBuilder.BuildManager(managerName, provResult, m.Spec)
			saName := authpkg.SAName(authpkg.RoleManager, managerName)
			createReq := backend.CreateRequest{
				Name:               managerName,
				ContainerName:      managerContainerName(managerName),
				Image:              m.Spec.Image,
				Runtime:            m.Spec.Runtime,
				Env:                managerEnv,
				ServiceAccountName: saName,
				Resources:          r.ManagerResources,
				Labels: map[string]string{
					"app":               "hiclaw-manager",
					"hiclaw.io/manager": managerName,
				},
			}
			if wb.Name() != "k8s" {
				token, err := r.Provisioner.RequestManagerSAToken(ctx, managerName)
				if err != nil {
					logger.Error(err, "failed to request manager SA token (non-fatal, auth will fail)")
				}
				createReq.AuthToken = token
			}

			// Embedded (Docker) mode: inject host volume mounts and extra env
			if wb.Name() == "docker" && r.EmbeddedConfig != nil {
				if r.EmbeddedConfig.WorkspaceDir != "" {
					createReq.Volumes = append(createReq.Volumes, backend.VolumeMount{
						HostPath:      r.EmbeddedConfig.WorkspaceDir,
						ContainerPath: "/root/manager-workspace",
					})
				}
				if r.EmbeddedConfig.HostShareDir != "" {
					createReq.Volumes = append(createReq.Volumes, backend.VolumeMount{
						HostPath:      r.EmbeddedConfig.HostShareDir,
						ContainerPath: "/host-share",
					})
				}
				createReq.RestartPolicy = "unless-stopped"
				// Map manager console port to host (18799 is the UI console for both runtimes)
				consoleHostPort := r.EmbeddedConfig.ManagerConsolePort
				if consoleHostPort == "" {
					consoleHostPort = "18888"
				}
				createReq.Ports = append(createReq.Ports, backend.PortMapping{
					HostIP:        "127.0.0.1",
					HostPort:      consoleHostPort,
					ContainerPort: "18799",
				})
				for k, v := range r.EmbeddedConfig.ExtraEnv {
					if _, exists := createReq.Env[k]; !exists {
						createReq.Env[k] = v
					}
				}
			}

			if _, err := wb.Create(ctx, createReq); err != nil {
				logger.Error(err, "manager container creation failed (non-fatal, can be started manually)")
			}
		} else {
			logger.Info("no worker backend available, manager needs manual start")
		}
	}

	// --- Phase 7: Status ---
	if err := r.Get(ctx, client.ObjectKeyFromObject(m), m); err != nil {
		return reconcile.Result{}, err
	}
	m.Status.ObservedGeneration = m.Generation
	m.Status.Phase = "Running"
	m.Status.MatrixUserID = provResult.MatrixUserID
	m.Status.RoomID = provResult.RoomID
	m.Status.Message = ""
	if m.Spec.Image != "" {
		m.Status.Version = m.Spec.Image
	}
	if err := r.Status().Update(ctx, m); err != nil {
		logger.Error(err, "failed to update status after create (non-fatal)")
	}

	logger.Info("manager created", "name", managerName, "roomID", provResult.RoomID)
	return reconcile.Result{}, nil
}

func (r *ManagerReconciler) handleUpdate(ctx context.Context, m *v1beta1.Manager) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("manager spec changed, updating configuration", "name", m.Name)
	managerName := m.Name

	m.Status.Phase = "Updating"
	m.Status.Message = "Updating manager configuration"
	if err := r.Status().Update(ctx, m); err != nil {
		return reconcile.Result{}, err
	}

	// Refresh credentials
	refreshResult, err := r.Provisioner.RefreshCredentials(ctx, managerName)
	if err != nil {
		return r.failManagerUpdate(ctx, m, err.Error())
	}

	// Reconcile MCP auth
	var authorizedMCPs []string
	if len(m.Spec.McpServers) > 0 {
		authorizedMCPs, err = r.Provisioner.ReconcileMCPAuth(ctx, "manager", m.Spec.McpServers)
		if err != nil {
			logger.Error(err, "MCP reauthorization failed (non-fatal)")
		}
	}

	// Redeploy configuration
	if err := r.Deployer.DeployManagerConfig(ctx, service.ManagerDeployRequest{
		Name:           managerName,
		Spec:           m.Spec,
		MatrixToken:    refreshResult.MatrixToken,
		GatewayKey:     refreshResult.GatewayKey,
		MatrixPassword: refreshResult.MatrixPassword,
		AuthorizedMCPs: authorizedMCPs,
		IsUpdate:       true,
	}); err != nil {
		return r.failManagerUpdate(ctx, m, err.Error())
	}

	// On-demand skills
	if err := r.Deployer.PushOnDemandSkills(ctx, managerName, m.Spec.Skills); err != nil {
		logger.Error(err, "skill push failed (non-fatal)")
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(m), m)
	m.Status.ObservedGeneration = m.Generation
	m.Status.Phase = "Running"
	m.Status.Message = "Configuration updated"
	if m.Spec.Image != "" {
		m.Status.Version = m.Spec.Image
	}
	if err := r.Status().Update(ctx, m); err != nil {
		logger.Error(err, "failed to update status after update (non-fatal)")
	}

	logger.Info("manager updated", "name", managerName)
	return reconcile.Result{}, nil
}

func (r *ManagerReconciler) handleDelete(ctx context.Context, m *v1beta1.Manager) error {
	logger := log.FromContext(ctx)
	logger.Info("deleting manager", "name", m.Name)

	managerName := m.Name

	// Deprovision infrastructure
	if err := r.Provisioner.DeprovisionManager(ctx, managerName, m.Spec.McpServers); err != nil {
		logger.Error(err, "deprovision failed (non-fatal)")
	}

	// Delete manager pod by exact name (bypasses backend's default worker prefix)
	managerPod := &corev1.Pod{}
	managerPod.Name = managerContainerName(managerName)
	managerPod.Namespace = m.Namespace
	if err := r.Delete(ctx, managerPod); err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "failed to delete manager pod (may already be removed)")
	}

	// Clean up OSS data
	if err := r.Deployer.CleanupOSSData(ctx, managerName); err != nil {
		logger.Error(err, "failed to clean up OSS agent data (non-fatal)")
	}

	// Delete credentials
	if err := r.Provisioner.DeleteCredentials(ctx, managerName); err != nil {
		logger.Error(err, "failed to delete credentials (non-fatal)")
	}

	// Delete ServiceAccount
	if err := r.Provisioner.DeleteManagerServiceAccount(ctx, managerName); err != nil {
		logger.Error(err, "failed to delete ServiceAccount (non-fatal)")
	}

	logger.Info("manager deleted", "name", managerName)
	return nil
}

func (r *ManagerReconciler) failManager(ctx context.Context, m *v1beta1.Manager, msg string) (reconcile.Result, error) {
	_ = r.Get(ctx, client.ObjectKeyFromObject(m), m)
	m.Status.Phase = "Failed"
	m.Status.Message = msg
	r.Status().Update(ctx, m)
	return reconcile.Result{RequeueAfter: time.Minute}, fmt.Errorf("%s", msg)
}

func (r *ManagerReconciler) failManagerUpdate(ctx context.Context, m *v1beta1.Manager, msg string) (reconcile.Result, error) {
	_ = r.Get(ctx, client.ObjectKeyFromObject(m), m)
	m.Status.Phase = "Running"
	m.Status.Message = msg
	r.Status().Update(ctx, m)
	return reconcile.Result{RequeueAfter: time.Minute}, fmt.Errorf("%s", msg)
}

// --- Desired state reconciliation ---

func (r *ManagerReconciler) reconcileDesiredState(ctx context.Context, m *v1beta1.Manager, desired string) (reconcile.Result, error) {
	// For Running state, always verify the container exists (it may have been
	// removed externally, e.g. by an upgrade install script).
	// For other states, skip if phase already matches.
	if m.Status.Phase == desired && desired != "Running" {
		return reconcile.Result{}, nil
	}

	logger := log.FromContext(ctx)
	logger.Info("reconciling desired state", "name", m.Name, "current", m.Status.Phase, "desired", desired)

	switch desired {
	case "Running":
		return r.ensureManagerRunning(ctx, m)
	case "Sleeping":
		return r.ensureManagerSleeping(ctx, m)
	case "Stopped":
		return r.ensureManagerStopped(ctx, m)
	default:
		logger.Info("unknown desired state, ignoring", "state", desired)
		return reconcile.Result{}, nil
	}
}

// managerBackend returns the WorkerBackend with Docker's container prefix cleared.
// Manager containers are created with ContainerName override (e.g. "hiclaw-manager"),
// which doesn't include the default worker prefix ("hiclaw-worker-").  Without this
// adjustment, Docker backend's Status/Stop/Delete/Start would look for the wrong name.
func (r *ManagerReconciler) managerBackend(ctx context.Context) backend.WorkerBackend {
	if r.Backend == nil {
		return nil
	}
	wb := r.Backend.DetectWorkerBackend(ctx)
	if wb == nil {
		return nil
	}
	// Docker backend always prepends containerPrefix to name parameters.
	// Manager containers use a different naming scheme, so strip the prefix.
	if docker, ok := wb.(*backend.DockerBackend); ok {
		return docker.WithPrefix("")
	}
	return wb
}

func (r *ManagerReconciler) ensureManagerRunning(ctx context.Context, m *v1beta1.Manager) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	wb := r.managerBackend(ctx)
	if wb == nil {
		return reconcile.Result{}, nil
	}

	containerName := managerContainerName(m.Name)
	result, err := wb.Status(ctx, containerName)
	if err != nil {
		logger.Error(err, "failed to check backend status")
		return reconcile.Result{RequeueAfter: reconcileRetryDelay}, nil
	}

	switch {
	case result.Status == backend.StatusRunning || result.Status == backend.StatusStarting:
		// Already running
	case result.Status == backend.StatusStopped && wb.Name() == "docker":
		if err := wb.Start(ctx, containerName); err != nil {
			return r.failManagerLifecycle(ctx, m, fmt.Sprintf("start failed: %v", err))
		}
	default:
		if err := r.recreateManagerContainer(ctx, m, wb); err != nil {
			return r.failManagerLifecycle(ctx, m, fmt.Sprintf("recreate failed: %v", err))
		}
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(m), m)
	m.Status.Phase = "Running"
	m.Status.Message = ""
	r.Status().Update(ctx, m)

	logger.Info("manager running", "name", m.Name)
	return reconcile.Result{}, nil
}

func (r *ManagerReconciler) ensureManagerSleeping(ctx context.Context, m *v1beta1.Manager) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	wb := r.managerBackend(ctx)
	if wb == nil {
		return reconcile.Result{}, nil
	}

	containerName := managerContainerName(m.Name)
	if err := wb.Stop(ctx, containerName); err != nil && !errors.Is(err, backend.ErrNotFound) {
		return r.failManagerLifecycle(ctx, m, fmt.Sprintf("sleep failed: %v", err))
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(m), m)
	m.Status.Phase = "Sleeping"
	m.Status.Message = ""
	r.Status().Update(ctx, m)

	logger.Info("manager sleeping", "name", m.Name)
	return reconcile.Result{}, nil
}

func (r *ManagerReconciler) ensureManagerStopped(ctx context.Context, m *v1beta1.Manager) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	wb := r.managerBackend(ctx)
	if wb == nil {
		return reconcile.Result{}, nil
	}

	containerName := managerContainerName(m.Name)
	_ = wb.Stop(ctx, containerName) // best-effort stop first
	if err := wb.Delete(ctx, containerName); err != nil && !errors.Is(err, backend.ErrNotFound) {
		return r.failManagerLifecycle(ctx, m, fmt.Sprintf("stop failed: %v", err))
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(m), m)
	m.Status.Phase = "Stopped"
	m.Status.Message = ""
	r.Status().Update(ctx, m)

	logger.Info("manager stopped", "name", m.Name)
	return reconcile.Result{}, nil
}

func (r *ManagerReconciler) recreateManagerContainer(ctx context.Context, m *v1beta1.Manager, wb backend.WorkerBackend) error {
	logger := log.FromContext(ctx)
	managerName := m.Name

	// Manager CR name is "default" but Matrix username is always "manager"
	refreshResult, err := r.Provisioner.RefreshManagerCredentials(ctx, managerName)
	if err != nil {
		return fmt.Errorf("refresh credentials: %w", err)
	}

	// Ensure gateway consumer is authorized on AI routes (may have been lost after upgrade)
	if err := r.Provisioner.EnsureManagerGatewayAuth(ctx, managerName, refreshResult.GatewayKey); err != nil {
		logger.Error(err, "gateway auth during recreate (non-fatal)")
	}

	_ = r.Provisioner.EnsureManagerServiceAccount(ctx, managerName)

	managerEnv := r.EnvBuilder.BuildManager(managerName, &service.ManagerProvisionResult{
		MatrixToken:    refreshResult.MatrixToken,
		GatewayKey:     refreshResult.GatewayKey,
		MinIOPassword:  refreshResult.MinIOPassword,
		MatrixPassword: refreshResult.MatrixPassword,
	}, m.Spec)

	containerName := managerContainerName(managerName)
	saName := authpkg.SAName(authpkg.RoleManager, managerName)
	createReq := backend.CreateRequest{
		Name:               managerName,
		ContainerName:      containerName,
		Image:              m.Spec.Image,
		Runtime:            m.Spec.Runtime,
		Env:                managerEnv,
		ServiceAccountName: saName,
		Resources:          r.ManagerResources,
		Labels: map[string]string{
			"app":               "hiclaw-manager",
			"hiclaw.io/manager": managerName,
		},
	}
	if wb.Name() != "k8s" {
		token, err := r.Provisioner.RequestManagerSAToken(ctx, managerName)
		if err != nil {
			logger.Error(err, "SA token request during recreate (non-fatal)")
		}
		createReq.AuthToken = token
	}

	// Embedded (Docker) mode: inject host volume mounts and extra env
	if wb.Name() == "docker" && r.EmbeddedConfig != nil {
		if r.EmbeddedConfig.WorkspaceDir != "" {
			createReq.Volumes = append(createReq.Volumes, backend.VolumeMount{
				HostPath:      r.EmbeddedConfig.WorkspaceDir,
				ContainerPath: "/root/manager-workspace",
			})
		}
		if r.EmbeddedConfig.HostShareDir != "" {
			createReq.Volumes = append(createReq.Volumes, backend.VolumeMount{
				HostPath:      r.EmbeddedConfig.HostShareDir,
				ContainerPath: "/host-share",
			})
		}
		createReq.RestartPolicy = "unless-stopped"
		// Map manager console port to host (18799 is the UI console for both runtimes)
		consoleHostPort := r.EmbeddedConfig.ManagerConsolePort
		if consoleHostPort == "" {
			consoleHostPort = "18888"
		}
		createReq.Ports = append(createReq.Ports, backend.PortMapping{
			HostIP:        "127.0.0.1",
			HostPort:      consoleHostPort,
			ContainerPort: "18799",
		})
		for k, v := range r.EmbeddedConfig.ExtraEnv {
			if _, exists := createReq.Env[k]; !exists {
				createReq.Env[k] = v
			}
		}
	}

	if _, err := wb.Create(ctx, createReq); err != nil {
		return fmt.Errorf("container recreate: %w", err)
	}
	return nil
}

func (r *ManagerReconciler) failManagerLifecycle(ctx context.Context, m *v1beta1.Manager, msg string) (reconcile.Result, error) {
	_ = r.Get(ctx, client.ObjectKeyFromObject(m), m)
	m.Status.Message = msg
	r.Status().Update(ctx, m)
	return reconcile.Result{RequeueAfter: time.Minute}, fmt.Errorf("%s", msg)
}

func (r *ManagerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Manager{})

	// In-cluster mode: watch Pod events with hiclaw.io/manager label,
	// map to corresponding Manager CR for immediate reconcile on pod failure/deletion.
	if r.Backend != nil {
		if wb := r.Backend.DetectWorkerBackend(context.Background()); wb != nil && wb.Name() == "k8s" {
			builder = builder.Watches(
				&corev1.Pod{},
				handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
					managerName := obj.GetLabels()["hiclaw.io/manager"]
					if managerName == "" {
						return nil
					}
					return []reconcile.Request{
						{NamespacedName: client.ObjectKey{Name: managerName}},
					}
				}),
			)
		}
	}

	return builder.Complete(r)
}
