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
		if worker.Status.RoomID == "" {
			continue
		}
		if err := r.Matrix.InviteToRoom(ctx, worker.Status.RoomID, matrixUserID); err != nil {
			logger.Error(err, "failed to invite human to worker room (non-fatal)", "worker", workerName, "room", worker.Status.RoomID)
		}
		if err := r.Matrix.JoinRoom(ctx, worker.Status.RoomID, userCreds.AccessToken); err != nil {
			logger.Error(err, "failed to join worker room", "worker", workerName, "room", worker.Status.RoomID)
		} else {
			joinedRooms = append(joinedRooms, worker.Status.RoomID)
		}
	}

	for _, teamName := range h.Spec.AccessibleTeams {
		var team v1beta1.Team
		if err := r.Get(ctx, client.ObjectKey{Name: teamName, Namespace: h.Namespace}, &team); err != nil {
			logger.Error(err, "failed to look up team for room join", "team", teamName)
			continue
		}
		if team.Status.TeamRoomID == "" {
			continue
		}
		if err := r.Matrix.InviteToRoom(ctx, team.Status.TeamRoomID, matrixUserID); err != nil {
			logger.Error(err, "failed to invite human to team room (non-fatal)", "team", teamName, "room", team.Status.TeamRoomID)
		}
		if err := r.Matrix.JoinRoom(ctx, team.Status.TeamRoomID, userCreds.AccessToken); err != nil {
			logger.Error(err, "failed to join team room", "team", teamName, "room", team.Status.TeamRoomID)
		} else {
			joinedRooms = append(joinedRooms, team.Status.TeamRoomID)
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
	logger := log.FromContext(ctx)

	// Compute the desired room ID set from the current spec. Rooms for
	// workers/teams that don't exist or haven't finished provisioning are
	// simply skipped this cycle and picked up on a later reconcile.
	desired := make(map[string]struct{})
	for _, workerName := range h.Spec.AccessibleWorkers {
		var worker v1beta1.Worker
		if err := r.Get(ctx, client.ObjectKey{Name: workerName, Namespace: h.Namespace}, &worker); err != nil {
			continue
		}
		if worker.Status.RoomID != "" {
			desired[worker.Status.RoomID] = struct{}{}
		}
	}
	for _, teamName := range h.Spec.AccessibleTeams {
		var team v1beta1.Team
		if err := r.Get(ctx, client.ObjectKey{Name: teamName, Namespace: h.Namespace}, &team); err != nil {
			continue
		}
		if team.Status.TeamRoomID != "" {
			desired[team.Status.TeamRoomID] = struct{}{}
		}
	}

	observed := make(map[string]struct{}, len(h.Status.Rooms))
	for _, rid := range h.Status.Rooms {
		observed[rid] = struct{}{}
	}

	matrixUserID := r.Matrix.UserID(h.Name)

	// Additions: invite via admin token, then join with the human's own
	// access token. Private rooms (trusted_private_chat preset) require a
	// pending invite before /join succeeds.
	var userToken string
	if len(desired) > len(observed) {
		creds, err := r.Matrix.EnsureUser(ctx, matrix.EnsureUserRequest{Username: h.Name})
		if err != nil {
			logger.Error(err, "failed to obtain human matrix token for update")
			return reconcile.Result{RequeueAfter: time.Minute}, nil
		}
		userToken = creds.AccessToken
	}

	newRooms := make([]string, 0, len(h.Status.Rooms)+len(desired))
	newRooms = append(newRooms, h.Status.Rooms...)

	for rid := range desired {
		if _, ok := observed[rid]; ok {
			continue
		}
		if err := r.Matrix.InviteToRoom(ctx, rid, matrixUserID); err != nil {
			logger.Error(err, "failed to invite human to room", "room", rid)
			continue
		}
		if err := r.Matrix.JoinRoom(ctx, rid, userToken); err != nil {
			logger.Error(err, "failed to join room as human", "room", rid)
			continue
		}
		newRooms = append(newRooms, rid)
	}

	// Removals: kick via admin token. On failure, keep the room in the
	// observed list so the next reconcile retries.
	kept := newRooms[:0]
	for _, rid := range newRooms {
		if _, ok := desired[rid]; ok {
			kept = append(kept, rid)
			continue
		}
		if err := r.Matrix.KickFromRoom(ctx, rid, matrixUserID, "access revoked"); err != nil {
			logger.Error(err, "failed to kick human from room", "room", rid)
			kept = append(kept, rid)
		}
	}

	if !stringSliceEqual(kept, h.Status.Rooms) {
		h.Status.Rooms = kept
		if err := r.Status().Update(ctx, h); err != nil {
			logger.Error(err, "failed to update human status (non-fatal)")
		}
	}

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

	return reconcile.Result{}, nil
}

func stringSliceEqual(a, b []string) bool {
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
