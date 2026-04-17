package controller

import (
	"context"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/service"
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

// TeamReconciler reconciles Team resources as a pure coordination CR.
//
// The reconciler never writes to Worker or Human CRs; it observes them via
// the Observer interface, derives the team's leader / members / admins,
// and ensures team-level infrastructure (Matrix Rooms, shared storage,
// legacy registry entries). Worker deletion/migration is the exclusive
// responsibility of WorkerReconciler; Team deletion does not cascade to
// Workers — the finalizer cleans up only Team-scoped resources.
type TeamReconciler struct {
	client.Client

	Provisioner service.TeamProvisioner
	Observer    service.TeamObserver
	Legacy      *service.LegacyCompat // nil in incluster mode
}

// Reconcile implements the level-triggered convergence loop. Status is
// always written via a single defer-patch at the end; ObservedGeneration
// is only advanced when reconcile completes without error, preventing
// spurious re-reconcile loops driven by mid-reconcile status writes.
func (r *TeamReconciler) Reconcile(ctx context.Context, req reconcile.Request) (retres reconcile.Result, reterr error) {
	logger := log.FromContext(ctx)

	var team v1beta1.Team
	if err := r.Get(ctx, req.NamespacedName, &team); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	patchBase := client.MergeFrom(team.DeepCopy())
	s := &teamScope{
		team:      &team,
		patchBase: patchBase,
	}

	defer func() {
		if !team.DeletionTimestamp.IsZero() {
			return
		}
		team.Status.Phase = computeTeamPhase(s, reterr)
		if reterr == nil {
			team.Status.ObservedGeneration = team.Generation
			team.Status.Message = ""
		} else {
			team.Status.Message = reterr.Error()
		}
		if err := r.Status().Patch(ctx, &team, patchBase); err != nil {
			logger.Error(err, "failed to patch team status")
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
	}()

	if !team.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&team, finalizerName) {
			return r.reconcileTeamDelete(ctx, s)
		}
		return reconcile.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&team, finalizerName) {
		controllerutil.AddFinalizer(&team, finalizerName)
		if err := r.Update(ctx, &team); err != nil {
			return reconcile.Result{}, err
		}
	}

	return r.reconcileTeamNormal(ctx, s)
}

// reconcileTeamNormal runs the declarative convergence phases in order.
// Phases that fail fatally return early; non-critical phases log errors
// and continue. Room creation is critical; admin/member observation and
// storage are best-effort for partial progress during transient failures.
func (r *TeamReconciler) reconcileTeamNormal(ctx context.Context, s *teamScope) (reconcile.Result, error) {
	if res, err := r.reconcileMembers(ctx, s); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	if res, err := r.reconcileAdmins(ctx, s); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	if res, err := r.reconcileRooms(ctx, s); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	r.reconcileStorage(ctx, s)
	r.reconcileLegacy(ctx, s)

	t := s.team
	logger := log.FromContext(ctx)
	if t.Status.ObservedGeneration == 0 {
		logger.Info("team created", "name", t.Name, "teamRoomID", t.Status.TeamRoomID)
	} else if t.Generation != t.Status.ObservedGeneration {
		logger.Info("team updated", "name", t.Name)
	}

	return reconcile.Result{RequeueAfter: reconcileInterval}, nil
}

// SetupWithManager wires the Team reconciler with two cross-CR watches:
// Worker (so that team membership changes trigger team reconcile) and
// Human (so that teamAccess-driven admin membership changes do the same).
// For each external object, the mapper emits one Request per Team that
// the object claims to belong to, so that a single Worker / Human change
// touches exactly the teams it references.
func (r *TeamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Team{}).
		Watches(
			&v1beta1.Worker{},
			handler.EnqueueRequestsFromMapFunc(workerToTeamsMapper),
			builder.WithPredicates(workerTeamRefPredicates()),
		).
		Watches(
			&v1beta1.Human{},
			handler.EnqueueRequestsFromMapFunc(humanToTeamsMapper),
			builder.WithPredicates(humanTeamAccessPredicates()),
		).
		Complete(r)
}

// workerToTeamsMapper maps a Worker change to its claimed Team.
func workerToTeamsMapper(_ context.Context, obj client.Object) []reconcile.Request {
	w, ok := obj.(*v1beta1.Worker)
	if !ok {
		return nil
	}
	if w.Spec.TeamRef == "" {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: w.Spec.TeamRef, Namespace: w.Namespace},
	}}
}

// workerTeamRefPredicates filters Worker events so Team reconcile only
// fires on changes that could affect team membership observations.
// Create/Delete always pass when teamRef is set; Update is rate-limited
// to transitions that alter role/teamRef or readiness.
func workerTeamRefPredicates() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			w, ok := e.Object.(*v1beta1.Worker)
			return ok && (w.Spec.TeamRef != "")
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			w, ok := e.Object.(*v1beta1.Worker)
			return ok && (w.Spec.TeamRef != "")
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldW, ok1 := e.ObjectOld.(*v1beta1.Worker)
			newW, ok2 := e.ObjectNew.(*v1beta1.Worker)
			if !ok1 || !ok2 {
				return true
			}
			if oldW.Spec.TeamRef != newW.Spec.TeamRef {
				return true
			}
			if newW.Spec.TeamRef == "" {
				return false
			}
			if oldW.Spec.Role != newW.Spec.Role {
				return true
			}
			if oldW.Status.Phase != newW.Status.Phase {
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

// humanToTeamsMapper maps a Human change to every Team named in its
// teamAccess list. Typical fan-out is 1-3 Teams per Human.
func humanToTeamsMapper(_ context.Context, obj client.Object) []reconcile.Request {
	h, ok := obj.(*v1beta1.Human)
	if !ok {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(h.Spec.TeamAccess))
	seen := make(map[string]bool, len(h.Spec.TeamAccess))
	for _, entry := range h.Spec.TeamAccess {
		if entry.Team == "" || seen[entry.Team] {
			continue
		}
		seen[entry.Team] = true
		reqs = append(reqs, reconcile.Request{
			NamespacedName: client.ObjectKey{Name: entry.Team, Namespace: h.Namespace},
		})
	}
	return reqs
}

// humanTeamAccessPredicates mirrors workerTeamRefPredicates for Human
// events: Team reconcile only runs when the change might alter an admin
// relationship or matrix ID.
func humanTeamAccessPredicates() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			h, ok := e.Object.(*v1beta1.Human)
			return ok && len(h.Spec.TeamAccess) > 0
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			h, ok := e.Object.(*v1beta1.Human)
			return ok && len(h.Spec.TeamAccess) > 0
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldH, ok1 := e.ObjectOld.(*v1beta1.Human)
			newH, ok2 := e.ObjectNew.(*v1beta1.Human)
			if !ok1 || !ok2 {
				return true
			}
			if !teamAccessEqual(oldH.Spec.TeamAccess, newH.Spec.TeamAccess) {
				return true
			}
			if oldH.Status.MatrixUserID != newH.Status.MatrixUserID {
				return true
			}
			return false
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// teamAccessEqual checks whether two TeamAccess slices are identical in
// content and order. Order changes are treated as a delta because the
// admin-vs-member role is position-significant per-team.
func teamAccessEqual(a, b []v1beta1.TeamAccessEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
