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

// WorkerReconciler reconciles standalone Worker resources. Team members are
// owned by Team CRs and are reconciled by TeamReconciler through the shared
// member_reconcile helpers, not by WorkerReconciler.
type WorkerReconciler struct {
	client.Client

	Provisioner service.WorkerProvisioner
	Deployer    service.WorkerDeployer
	Backend     *backend.Registry
	EnvBuilder  service.WorkerEnvBuilderI
	Legacy      *service.LegacyCompat // nil in incluster mode

	// DefaultRuntime is the value passed to backend.CreateRequest.RuntimeFallback
	// when a Worker CR omits spec.runtime. Sourced from
	// HICLAW_DEFAULT_WORKER_RUNTIME (Config.DefaultWorkerRuntime). Empty means
	// "no operator preference" — backend.ResolveRuntime will fall back to
	// "openclaw".
	DefaultRuntime string
}

func (r *WorkerReconciler) Reconcile(ctx context.Context, req reconcile.Request) (retres reconcile.Result, reterr error) {
	logger := log.FromContext(ctx)

	var worker v1beta1.Worker
	if err := r.Get(ctx, req.NamespacedName, &worker); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	patchBase := client.MergeFrom(worker.DeepCopy())

	// Unified status patch at the end of every reconcile. ObservedGeneration
	// is only written when reconcile succeeds, preventing the infinite-loop
	// bug where a failed status write triggered re-reconcile with
	// Generation != ObservedGeneration.
	defer func() {
		if !worker.DeletionTimestamp.IsZero() {
			return
		}
		worker.Status.Phase = computeWorkerPhase(&worker, reterr)
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

	if !worker.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&worker, finalizerName) {
			return r.reconcileDelete(ctx, &worker)
		}
		return reconcile.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&worker, finalizerName) {
		controllerutil.AddFinalizer(&worker, finalizerName)
		if err := r.Update(ctx, &worker); err != nil {
			return reconcile.Result{}, err
		}
	}

	return r.reconcileNormal(ctx, &worker)
}

// reconcileNormal builds a MemberContext from the Worker CR, runs the shared
// member reconcile phases, and writes runtime state back to Worker.Status.
// Legacy Manager groupAllowFrom is updated here only for standalone workers;
// team leaders are handled by TeamReconciler.
func (r *WorkerReconciler) reconcileNormal(ctx context.Context, w *v1beta1.Worker) (reconcile.Result, error) {
	deps := MemberDeps{
		Provisioner:    r.Provisioner,
		Deployer:       r.Deployer,
		Backend:        r.Backend,
		EnvBuilder:     r.EnvBuilder,
		DefaultRuntime: r.DefaultRuntime,
	}
	mctx := workerMemberContext(w)
	state := &MemberState{}

	if res, err := ReconcileMemberInfra(ctx, deps, mctx, state); err != nil || res.RequeueAfter > 0 {
		applyMemberStateToWorker(w, state)
		return res, err
	}
	if err := EnsureMemberServiceAccount(ctx, deps, mctx); err != nil {
		applyMemberStateToWorker(w, state)
		return reconcile.Result{}, err
	}
	if err := ReconcileMemberConfig(ctx, deps, mctx, state); err != nil {
		applyMemberStateToWorker(w, state)
		return reconcile.Result{}, err
	}
	if res, err := ReconcileMemberContainer(ctx, deps, mctx, state); err != nil || res.RequeueAfter > 0 {
		applyMemberStateToWorker(w, state)
		return res, err
	}
	_ = ReconcileMemberExpose(ctx, deps, mctx, state)
	applyMemberStateToWorker(w, state)

	r.reconcileLegacy(ctx, w, state)

	logger := log.FromContext(ctx)
	if w.Status.ObservedGeneration == 0 {
		logger.Info("worker created", "name", w.Name, "roomID", w.Status.RoomID)
	} else if w.Generation != w.Status.ObservedGeneration {
		logger.Info("worker updated", "name", w.Name)
	}

	return reconcile.Result{RequeueAfter: reconcileInterval}, nil
}

// reconcileDelete cleans up all infrastructure for the Worker and then removes
// the finalizer. Legacy Manager groupAllowFrom is rolled back here only for
// standalone workers.
func (r *WorkerReconciler) reconcileDelete(ctx context.Context, w *v1beta1.Worker) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("deleting worker", "name", w.Name)

	deps := MemberDeps{
		Provisioner:    r.Provisioner,
		Deployer:       r.Deployer,
		Backend:        r.Backend,
		EnvBuilder:     r.EnvBuilder,
		DefaultRuntime: r.DefaultRuntime,
	}
	mctx := workerMemberContext(w)

	_ = ReconcileMemberDelete(ctx, deps, mctx)

	if r.Legacy != nil && r.Legacy.Enabled() {
		workerMatrixID := r.Provisioner.MatrixUserID(w.Name)
		if mctx.Role == RoleStandalone {
			if err := r.Legacy.UpdateManagerGroupAllowFrom(workerMatrixID, false); err != nil {
				logger.Error(err, "failed to update Manager groupAllowFrom (non-fatal)")
			}
		}
		if err := r.Legacy.RemoveFromWorkersRegistry(w.Name); err != nil {
			logger.Error(err, "failed to remove from workers registry (non-fatal)")
		}
	}

	controllerutil.RemoveFinalizer(w, finalizerName)
	if err := r.Update(ctx, w); err != nil {
		return reconcile.Result{}, err
	}

	logger.Info("worker deleted", "name", w.Name)
	return reconcile.Result{}, nil
}

// reconcileLegacy writes the worker to the legacy workers-registry and grants
// the standalone worker publish rights into the Manager's group DM room.
func (r *WorkerReconciler) reconcileLegacy(ctx context.Context, w *v1beta1.Worker, state *MemberState) {
	if r.Legacy == nil || !r.Legacy.Enabled() {
		return
	}
	logger := log.FromContext(ctx)

	role := w.Annotations["hiclaw.io/role"]
	teamName := w.Annotations["hiclaw.io/team"]
	teamLeaderName := w.Annotations["hiclaw.io/team-leader"]
	memberRole := roleForAnnotations(role, teamLeaderName)

	// Only standalone workers grant themselves group-DM publish rights. Team
	// leaders are handled by TeamReconciler; team workers never go through
	// WorkerReconciler post-refactor.
	if memberRole == RoleStandalone && state.ProvResult != nil {
		if err := r.Legacy.UpdateManagerGroupAllowFrom(state.ProvResult.MatrixUserID, true); err != nil {
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

// workerMemberContext translates a Worker CR into a MemberContext for the
// shared member reconcile helpers.
func workerMemberContext(w *v1beta1.Worker) MemberContext {
	role := roleForAnnotations(w.Annotations["hiclaw.io/role"], w.Annotations["hiclaw.io/team-leader"])
	return MemberContext{
		Name:               w.Name,
		Namespace:          w.Namespace,
		Role:               role,
		Spec:               w.Spec,
		Generation:         w.Generation,
		ObservedGeneration: w.Status.ObservedGeneration,
		// For Worker CR, spec change is detected the classic way:
		// Generation increments on every spec mutation; ObservedGeneration
		// is written after a successful reconcile. Status defaults to 0
		// on a freshly created Worker, which correctly marks the first
		// reconcile as "changed" so the initial container is created.
		SpecChanged:          w.Generation != w.Status.ObservedGeneration,
		IsUpdate:             w.Status.Phase != "" && w.Status.Phase != "Pending" && w.Status.Phase != "Failed",
		TeamName:             w.Annotations["hiclaw.io/team"],
		TeamLeaderName:       w.Annotations["hiclaw.io/team-leader"],
		TeamAdminMatrixID:    w.Annotations["hiclaw.io/team-admin-id"],
		ExistingMatrixUserID: w.Status.MatrixUserID,
		ExistingRoomID:       w.Status.RoomID,
		CurrentExposedPorts:  w.Status.ExposedPorts,
	}
}

// applyMemberStateToWorker copies runtime state into Worker.Status fields.
// Phase, ObservedGeneration, Message are owned by the deferred patch in
// Reconcile; this helper only touches infra/runtime fields.
func applyMemberStateToWorker(w *v1beta1.Worker, state *MemberState) {
	if state == nil {
		return
	}
	if state.MatrixUserID != "" {
		w.Status.MatrixUserID = state.MatrixUserID
	}
	if state.RoomID != "" {
		w.Status.RoomID = state.RoomID
	}
	if state.ContainerState != "" {
		w.Status.ContainerState = state.ContainerState
	}
	if state.ExposedPorts != nil || len(w.Spec.Expose) == 0 {
		w.Status.ExposedPorts = state.ExposedPorts
	}
}

// computeWorkerPhase determines the Worker status phase from the reconcile
// outcome. On success, phase reflects the desired lifecycle state.
func computeWorkerPhase(w *v1beta1.Worker, reconcileErr error) string {
	if reconcileErr != nil {
		if w.Status.MatrixUserID == "" {
			return "Failed"
		}
		if w.Status.Phase == "" {
			return "Pending"
		}
		// Keep the old Phase to avoid marking a healthy worker as Failed on a
		// transient error; the error surfaces through Status.Message instead.
		return w.Status.Phase
	}
	return w.Spec.DesiredState()
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
					// Skip pods owned by a Team (those are reconciled via
					// the Team controller's own pod watch).
					if obj.GetLabels()["hiclaw.io/team"] != "" {
						return nil
					}
					return []reconcile.Request{
						{NamespacedName: client.ObjectKey{
							Name:      workerName,
							Namespace: obj.GetNamespace(),
						}},
					}
				}),
				builder.WithPredicates(podLifecyclePredicates("hiclaw.io/worker")),
			)
		}
	}

	return bldr.Complete(r)
}

// podLifecyclePredicates filters Pod events to only trigger reconciliation on
// create, delete, or phase transitions. labelKey is the pod label used to
// identify which CR owns the pod (e.g. "hiclaw.io/worker", "hiclaw.io/team",
// "hiclaw.io/manager").
func podLifecyclePredicates(labelKey string) predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetLabels()[labelKey] != ""
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return e.Object.GetLabels()[labelKey] != ""
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectNew.GetLabels()[labelKey] == "" {
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

// roleForAnnotations maps Worker CR annotations to a MemberRole.
func roleForAnnotations(role, teamLeaderName string) MemberRole {
	if role == "team_leader" {
		return RoleTeamLeader
	}
	if teamLeaderName != "" {
		return RoleTeamWorker
	}
	return RoleStandalone
}
