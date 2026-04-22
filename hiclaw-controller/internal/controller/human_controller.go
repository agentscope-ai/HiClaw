package controller

import (
	"context"
	"fmt"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/matrix"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumanReconciler reconciles Human resources using Service-layer orchestration.
type HumanReconciler struct {
	client.Client

	Matrix matrix.Client
	Legacy *service.LegacyCompat // nil in incluster mode
}

func (r *HumanReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	var human v1beta1.Human
	if err := r.Get(ctx, req.NamespacedName, &human); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !human.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&human, finalizerName) {
			if err := r.handleDelete(ctx, &human); err != nil {
				logger.Error(err, "failed to delete human", "name", human.Name)
				return reconcile.Result{RequeueAfter: 30 * time.Second}, err
			}
			controllerutil.RemoveFinalizer(&human, finalizerName)
			if err := r.Update(ctx, &human); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&human, finalizerName) {
		controllerutil.AddFinalizer(&human, finalizerName)
		if err := r.Update(ctx, &human); err != nil {
			return reconcile.Result{}, err
		}
	}

	switch human.Status.Phase {
	case "", "Failed":
		return r.handleCreate(ctx, &human)
	default:
		return r.handleUpdate(ctx, &human)
	}
}

func (r *HumanReconciler) handleCreate(ctx context.Context, h *v1beta1.Human) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("creating human", "name", h.Name)

	h.Status.Phase = "Pending"
	if err := r.Status().Update(ctx, h); err != nil {
		return reconcile.Result{}, err
	}

	userCreds, err := r.Matrix.EnsureUser(ctx, matrix.EnsureUserRequest{
		Username: h.Name,
	})
	if err != nil {
		h.Status.Phase = "Failed"
		h.Status.Message = fmt.Sprintf("Matrix registration failed: %v", err)
		r.Status().Update(ctx, h)
		return reconcile.Result{RequeueAfter: time.Minute}, err
	}

	matrixUserID := r.Matrix.UserID(h.Name)

	var joinedRooms []string
	for _, workerName := range h.Spec.AccessibleWorkers {
		var worker v1beta1.Worker
		if err := r.Get(ctx, client.ObjectKey{Name: workerName, Namespace: h.Namespace}, &worker); err != nil {
			logger.Error(err, "failed to look up worker for room join", "worker", workerName)
			continue
		}
		if worker.Status.RoomID != "" {
			if err := r.Matrix.JoinRoom(ctx, worker.Status.RoomID, userCreds.AccessToken); err != nil {
				logger.Error(err, "failed to join worker room", "worker", workerName, "room", worker.Status.RoomID)
			} else {
				joinedRooms = append(joinedRooms, worker.Status.RoomID)
			}
		}
	}

	for _, teamName := range h.Spec.AccessibleTeams {
		var team v1beta1.Team
		if err := r.Get(ctx, client.ObjectKey{Name: teamName, Namespace: h.Namespace}, &team); err != nil {
			logger.Error(err, "failed to look up team for room join", "team", teamName)
			continue
		}
		if team.Status.TeamRoomID != "" {
			if err := r.Matrix.JoinRoom(ctx, team.Status.TeamRoomID, userCreds.AccessToken); err != nil {
				logger.Error(err, "failed to join team room", "team", teamName, "room", team.Status.TeamRoomID)
			} else {
				joinedRooms = append(joinedRooms, team.Status.TeamRoomID)
			}
		}
	}

	// Legacy: update humans-registry
	if r.Legacy != nil && r.Legacy.Enabled() {
		if err := r.Legacy.UpdateHumansRegistry(service.HumanRegistryEntry{
			Name:            h.Name,
			MatrixUserID:    matrixUserID,
			DisplayName:     h.Spec.DisplayName,
			PermissionLevel: h.Spec.PermissionLevel,
			AccessibleTeams: h.Spec.AccessibleTeams,
		}); err != nil {
			logger.Error(err, "humans-registry update failed (non-fatal)")
		}
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(h), h)
	h.Status.Phase = "Active"
	h.Status.MatrixUserID = matrixUserID
	h.Status.InitialPassword = userCreds.Password
	h.Status.Rooms = joinedRooms
	h.Status.Message = ""
	if err := r.Status().Update(ctx, h); err != nil {
		logger.Error(err, "failed to update human status (non-fatal)")
	}

	logger.Info("human created", "name", h.Name, "matrixUserID", matrixUserID, "rooms", len(joinedRooms))
	return reconcile.Result{}, nil
}

func (r *HumanReconciler) handleUpdate(ctx context.Context, h *v1beta1.Human) (reconcile.Result, error) {
	// TODO: detect permission level / accessible teams changes and reconfigure
	return reconcile.Result{}, nil
}

func (r *HumanReconciler) handleDelete(ctx context.Context, h *v1beta1.Human) error {
	logger := log.FromContext(ctx)
	logger.Info("deleting human", "name", h.Name)

	// Force the human out of every room the controller knows about. The
	// human holds no credentials we can log in with, so we rely on the
	// Tuwunel admin bot to kick them. Fire-and-forget: processing is
	// asynchronous inside tuwunel, and the
	// delete_rooms_after_leave/forget_forced_upon_leave homeserver flags
	// provide a fallback if this never lands.
	if r.Matrix != nil {
		humanUserID := r.Matrix.UserID(h.Name)
		for _, roomID := range h.Status.Rooms {
			cmd := fmt.Sprintf("!admin users force-leave-room %s %s", humanUserID, roomID)
			if err := r.Matrix.AdminCommand(ctx, cmd); err != nil {
				logger.Error(err, "force-leave-room failed (non-fatal)",
					"user", humanUserID, "roomID", roomID)
			}
		}
	}

	if r.Legacy != nil {
		if err := r.Legacy.RemoveFromHumansRegistry(ctx, h.Name); err != nil {
			logger.Error(err, "failed to remove human from registry (non-fatal)")
		}
	}

	return nil
}

func (r *HumanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Human{}).
		Complete(r)
}
