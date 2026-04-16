package controller

import (
	"context"
	"time"

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

const (
	finalizerName       = "hiclaw.io/cleanup"
	reconcileInterval   = 5 * time.Minute
	reconcileRetryDelay = 30 * time.Second
)

// WorkerReconciler reconciles Worker resources using Service-layer orchestration.
type WorkerReconciler struct {
	client.Client

	Provisioner service.WorkerProvisioner
	Deployer    service.WorkerDeployer
	Backend     *backend.Registry
	EnvBuilder  service.WorkerEnvBuilderI
	Legacy      *service.LegacyCompat // nil in incluster mode
}

func (r *WorkerReconciler) Reconcile(ctx context.Context, req reconcile.Request) (retres reconcile.Result, reterr error) {
	logger := log.FromContext(ctx)

	var worker v1beta1.Worker
	if err := r.Get(ctx, req.NamespacedName, &worker); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	patchBase := client.MergeFrom(worker.DeepCopy())

	s := &workerScope{
		worker:    &worker,
		patchBase: patchBase,
	}

	// Unified status patch at the end of every reconcile.
	// ObservedGeneration is only written when reconcile succeeds, preventing
	// the infinite-loop bug where a failed status write triggered re-reconcile
	// with Generation != ObservedGeneration.
	defer func() {
		if !worker.DeletionTimestamp.IsZero() {
			return
		}

		worker.Status.Phase = computePhase(&worker, reterr)
		if reterr == nil {
			worker.Status.ObservedGeneration = worker.Generation
			worker.Status.Message = ""
		} else {
			worker.Status.Message = reterr.Error()
		}

		if err := r.Status().Patch(ctx, &worker, patchBase); err != nil {
			logger.Error(err, "failed to patch worker status")
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
	}()

	// Handle deletion
	if !worker.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&worker, finalizerName) {
			return r.reconcileDelete(ctx, s)
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(&worker, finalizerName) {
		controllerutil.AddFinalizer(&worker, finalizerName)
		if err := r.Update(ctx, &worker); err != nil {
			return reconcile.Result{}, err
		}
	}

	return r.reconcileNormal(ctx, s)
}

// reconcileNormal runs the declarative convergence loop: infrastructure,
// config, container, expose, legacy. Critical-path phases are serial with
// early return on error; non-critical phases log errors without failing.
func (r *WorkerReconciler) reconcileNormal(ctx context.Context, s *workerScope) (reconcile.Result, error) {
	if res, err := r.reconcileInfrastructure(ctx, s); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	if res, err := r.reconcileConfig(ctx, s); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	if res, err := r.reconcileContainer(ctx, s); err != nil || res.RequeueAfter > 0 {
		return res, err
	}

	r.reconcileExpose(ctx, s)
	r.reconcileLegacy(ctx, s)

	w := s.worker
	logger := log.FromContext(ctx)
	if w.Status.ObservedGeneration == 0 {
		logger.Info("worker created", "name", w.Name, "roomID", w.Status.RoomID)
	} else if w.Generation != w.Status.ObservedGeneration {
		logger.Info("worker updated", "name", w.Name)
	}

	return reconcile.Result{RequeueAfter: reconcileInterval}, nil
}

// reconcileExpose reconciles port exposure via the gateway. Non-critical.
func (r *WorkerReconciler) reconcileExpose(ctx context.Context, s *workerScope) {
	w := s.worker
	if len(w.Spec.Expose) == 0 && len(w.Status.ExposedPorts) == 0 {
		return
	}
	exposedPorts, err := r.Provisioner.ReconcileExpose(ctx, w.Name, w.Spec.Expose, w.Status.ExposedPorts)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to reconcile exposed ports (non-fatal)")
		return
	}
	w.Status.ExposedPorts = exposedPorts
}

// reconcileLegacy updates the legacy workers registry. Non-critical.
func (r *WorkerReconciler) reconcileLegacy(ctx context.Context, s *workerScope) {
	w := s.worker
	if r.Legacy == nil || !r.Legacy.Enabled() {
		return
	}
	logger := log.FromContext(ctx)

	role := w.Annotations["hiclaw.io/role"]
	teamName := w.Annotations["hiclaw.io/team"]
	teamLeaderName := w.Annotations["hiclaw.io/team-leader"]
	isTeamWorker := teamLeaderName != ""

	if !isTeamWorker && s.provResult != nil {
		if err := r.Legacy.UpdateManagerGroupAllowFrom(s.provResult.MatrixUserID, true); err != nil {
			logger.Error(err, "failed to update Manager groupAllowFrom (non-fatal)")
		}
	}

	if err := r.Legacy.UpdateWorkersRegistry(service.WorkerRegistryEntry{
		Name:         w.Name,
		MatrixUserID: r.Provisioner.MatrixUserID(w.Name),
		RoomID:       w.Status.RoomID,
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

func (r *WorkerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Worker{})

	if r.Backend != nil {
		if wb := r.Backend.DetectWorkerBackend(context.Background()); wb != nil && wb.Name() == "k8s" {
			bldr = bldr.Watches(
				&corev1.Pod{},
				handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
					workerName := obj.GetLabels()["hiclaw.io/worker"]
					if workerName == "" {
						return nil
					}
					return []reconcile.Request{
						{NamespacedName: client.ObjectKey{
							Name:      workerName,
							Namespace: obj.GetNamespace(),
						}},
					}
				}),
				builder.WithPredicates(podLifecyclePredicates()),
			)
		}
	}

	return bldr.Complete(r)
}

// podLifecyclePredicates filters Pod events to only trigger reconciliation
// on create, delete, or phase transitions (not every status update).
func podLifecyclePredicates() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetLabels()["hiclaw.io/worker"] != ""
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return e.Object.GetLabels()["hiclaw.io/worker"] != ""
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectNew.GetLabels()["hiclaw.io/worker"] == "" {
				return false
			}
			oldPod, ok1 := e.ObjectOld.(*corev1.Pod)
			newPod, ok2 := e.ObjectNew.(*corev1.Pod)
			if !ok1 || !ok2 {
				return true
			}
			return oldPod.Status.Phase != newPod.Status.Phase
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
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
