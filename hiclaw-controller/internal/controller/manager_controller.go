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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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

// reconcileManagerNormal runs the declarative convergence loop:
//
//   infrastructure -> allowFrom -> service account -> config -> container
//
// allowFrom runs before config so the deployed openclaw.json reflects the
// authoritative Manager allow list on every reconcile. Critical-path
// phases return early on error.
func (r *ManagerReconciler) reconcileManagerNormal(ctx context.Context, s *managerScope) (reconcile.Result, error) {
	if res, err := r.reconcileManagerInfrastructure(ctx, s); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	if res, err := r.reconcileManagerAllowFrom(ctx, s); err != nil || res.RequeueAfter > 0 {
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

	// Watch Worker: any role / matrix ID change in a Worker could alter
	// this Manager's effective allowFrom. Mapper fans out to every
	// Manager in the namespace so multi-Manager deployments re-reconcile
	// consistently.
	bldr = bldr.Watches(
		&v1beta1.Worker{},
		handler.EnqueueRequestsFromMapFunc(r.workerToManagersMapper),
		builder.WithPredicates(workerToManagersPredicates()),
	)

	// Watch Human: superAdmin toggles and matrix ID changes affect the
	// allowFrom composition.
	bldr = bldr.Watches(
		&v1beta1.Human{},
		handler.EnqueueRequestsFromMapFunc(r.humanToManagersMapper),
		builder.WithPredicates(humanToManagersPredicates()),
	)

	return bldr.Complete(r)
}

// workerToManagersMapper emits a reconcile request for every Manager in
// the same namespace as the changed Worker. Returns nil for Workers
// whose role is neither standalone nor team_leader (team_worker changes
// never reach the Manager).
func (r *ManagerReconciler) workerToManagersMapper(ctx context.Context, obj client.Object) []reconcile.Request {
	w, ok := obj.(*v1beta1.Worker)
	if !ok {
		return nil
	}
	role := w.Spec.EffectiveRole()
	if role != v1beta1.WorkerRoleStandalone && role != v1beta1.WorkerRoleTeamLeader {
		return nil
	}
	return r.listManagerRequests(ctx, w.Namespace)
}

// humanToManagersMapper emits a reconcile request for every Manager in
// the same namespace when a superAdmin Human changes. Non-superAdmin
// Humans never affect the Manager allowFrom.
func (r *ManagerReconciler) humanToManagersMapper(ctx context.Context, obj client.Object) []reconcile.Request {
	h, ok := obj.(*v1beta1.Human)
	if !ok {
		return nil
	}
	if !h.Spec.SuperAdmin {
		return nil
	}
	return r.listManagerRequests(ctx, h.Namespace)
}

// listManagerRequests returns one reconcile.Request per Manager in the
// namespace. Used by both cross-CR mappers.
func (r *ManagerReconciler) listManagerRequests(ctx context.Context, ns string) []reconcile.Request {
	var list v1beta1.ManagerList
	if err := r.List(ctx, &list, client.InNamespace(ns)); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: client.ObjectKey{
			Name:      list.Items[i].Name,
			Namespace: list.Items[i].Namespace,
		}})
	}
	return out
}

// workerToManagersPredicates filters Worker events to changes that affect
// Manager allowFrom composition.
func workerToManagersPredicates() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldW, ok1 := e.ObjectOld.(*v1beta1.Worker)
			newW, ok2 := e.ObjectNew.(*v1beta1.Worker)
			if !ok1 || !ok2 {
				return true
			}
			if oldW.Spec.Role != newW.Spec.Role {
				return true
			}
			if oldW.Status.MatrixUserID != newW.Status.MatrixUserID {
				return true
			}
			return false
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// humanToManagersPredicates filters Human events to superAdmin transitions
// and matrix ID changes that affect Manager allowFrom.
func humanToManagersPredicates() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			h, ok := e.Object.(*v1beta1.Human)
			return ok && h.Spec.SuperAdmin
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			h, ok := e.Object.(*v1beta1.Human)
			return ok && h.Spec.SuperAdmin
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldH, ok1 := e.ObjectOld.(*v1beta1.Human)
			newH, ok2 := e.ObjectNew.(*v1beta1.Human)
			if !ok1 || !ok2 {
				return true
			}
			if oldH.Spec.SuperAdmin != newH.Spec.SuperAdmin {
				return true
			}
			if !newH.Spec.SuperAdmin {
				return false
			}
			return oldH.Status.MatrixUserID != newH.Status.MatrixUserID
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}
