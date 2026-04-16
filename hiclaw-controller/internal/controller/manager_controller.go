package controller

import (
	"context"
	"fmt"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
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

	Provisioner      service.ManagerProvisioner
	Deployer         service.ManagerDeployer
	Backend          *backend.Registry
	EnvBuilder       service.ManagerEnvBuilderI
	ManagerResources *backend.ResourceRequirements
	EmbeddedConfig   *ManagerEmbeddedConfig // non-nil in embedded mode only
}

func (r *ManagerReconciler) Reconcile(ctx context.Context, req reconcile.Request) (retres reconcile.Result, reterr error) {
	logger := log.FromContext(ctx)

	var mgr v1beta1.Manager
	if err := r.Get(ctx, req.NamespacedName, &mgr); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	patchBase := client.MergeFrom(mgr.DeepCopy())

	s := &managerScope{
		manager:   &mgr,
		patchBase: patchBase,
	}

	defer func() {
		if !mgr.DeletionTimestamp.IsZero() {
			return
		}

		mgr.Status.Phase = computeManagerPhase(&mgr, reterr)
		if reterr == nil {
			mgr.Status.ObservedGeneration = mgr.Generation
			mgr.Status.Message = ""
		} else {
			mgr.Status.Message = reterr.Error()
		}
		if mgr.Spec.Image != "" {
			mgr.Status.Version = mgr.Spec.Image
		}

		if err := r.Status().Patch(ctx, &mgr, patchBase); err != nil {
			logger.Error(err, "failed to patch manager status")
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
	}()

	if !mgr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&mgr, finalizerName) {
			return r.reconcileManagerDelete(ctx, s)
		}
		return reconcile.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&mgr, finalizerName) {
		controllerutil.AddFinalizer(&mgr, finalizerName)
		if err := r.Update(ctx, &mgr); err != nil {
			return reconcile.Result{}, err
		}
	}

	return r.reconcileManagerNormal(ctx, s)
}

// reconcileManagerNormal runs the declarative convergence loop: infrastructure,
// config, container. Critical-path phases are serial with early return on error.
func (r *ManagerReconciler) reconcileManagerNormal(ctx context.Context, s *managerScope) (reconcile.Result, error) {
	if res, err := r.reconcileManagerInfrastructure(ctx, s); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	if err := r.Provisioner.EnsureManagerServiceAccount(ctx, s.manager.Name); err != nil {
		return reconcile.Result{}, fmt.Errorf("ServiceAccount: %w", err)
	}
	if res, err := r.reconcileManagerConfig(ctx, s); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	if res, err := r.reconcileManagerContainer(ctx, s); err != nil || res.RequeueAfter > 0 {
		return res, err
	}

	m := s.manager
	logger := log.FromContext(ctx)
	if m.Status.ObservedGeneration == 0 {
		logger.Info("manager created", "name", m.Name, "roomID", m.Status.RoomID)
	} else if m.Generation != m.Status.ObservedGeneration {
		logger.Info("manager updated", "name", m.Name)
	}

	return reconcile.Result{RequeueAfter: reconcileInterval}, nil
}

func (r *ManagerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Manager{})

	if r.Backend != nil {
		if wb := r.Backend.DetectWorkerBackend(context.Background()); wb != nil && wb.Name() == "k8s" {
			bldr = bldr.Watches(
				&corev1.Pod{},
				handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
					managerName := obj.GetLabels()["hiclaw.io/manager"]
					if managerName == "" {
						return nil
					}
					return []reconcile.Request{
						{NamespacedName: client.ObjectKey{
							Name:      managerName,
							Namespace: obj.GetNamespace(),
						}},
					}
				}),
				builder.WithPredicates(podLifecyclePredicates("hiclaw.io/manager")),
			)
		}
	}

	return bldr.Complete(r)
}
