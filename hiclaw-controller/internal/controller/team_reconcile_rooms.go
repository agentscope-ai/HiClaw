package controller

import (
	"context"
	"fmt"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconcileRooms ensures the Team Room and Leader DM Room exist and their
// membership matches the observed leader + members + admins set. When no
// leader is ready, the phase short-circuits with a LeaderNotReady
// condition — Rooms are intentionally not created speculatively because
// their invite list depends on the leader's Matrix identity.
func (r *TeamReconciler) reconcileRooms(ctx context.Context, s *teamScope) (reconcile.Result, error) {
	t := s.team
	logger := log.FromContext(ctx)

	if s.leader == nil || !s.leader.Ready || s.leader.MatrixUserID == "" {
		setCondition(&t.Status.Conditions, v1beta1.ConditionTeamRoomReady, metav1.ConditionFalse,
			"LeaderNotReady", "Team Room creation deferred until leader Worker is ready", t.Generation)
		return reconcile.Result{}, nil
	}

	memberIDs := collectMatrixIDs(s.members)
	adminIDs := collectAdminMatrixIDs(s.admins)

	rooms, err := r.Provisioner.EnsureTeamRooms(ctx, service.TeamRoomsRequest{
		TeamName:               t.Name,
		LeaderMatrixID:         s.leader.MatrixUserID,
		MemberMatrixIDs:        memberIDs,
		AdminMatrixIDs:         adminIDs,
		ExistingTeamRoomID:     t.Status.TeamRoomID,
		ExistingLeaderDMRoomID: t.Status.LeaderDMRoomID,
	})
	if err != nil {
		setCondition(&t.Status.Conditions, v1beta1.ConditionTeamRoomReady, metav1.ConditionFalse,
			"EnsureRoomsFailed", err.Error(), t.Generation)
		return reconcile.Result{}, fmt.Errorf("ensure team rooms: %w", err)
	}

	t.Status.TeamRoomID = rooms.TeamRoomID
	t.Status.LeaderDMRoomID = rooms.LeaderDMRoomID

	// Compute desired membership sets for each room and reconcile.
	desiredTeamRoom := append([]string{s.leader.MatrixUserID}, memberIDs...)
	desiredTeamRoom = append(desiredTeamRoom, adminIDs...)
	desiredLeaderDM := append([]string{s.leader.MatrixUserID}, adminIDs...)

	if err := r.Provisioner.ReconcileTeamRoomMembership(ctx, service.TeamRoomMembershipRequest{
		TeamRoomID:           rooms.TeamRoomID,
		LeaderDMRoomID:       rooms.LeaderDMRoomID,
		DesiredTeamMembers:   desiredTeamRoom,
		DesiredLeaderDMUsers: desiredLeaderDM,
	}); err != nil {
		// Membership reconciliation is non-fatal: Rooms exist and are
		// functional even if invite/kick partially fails. Track the error
		// via the condition so admins can observe drift.
		logger.Error(err, "team room membership reconcile had errors")
		setCondition(&t.Status.Conditions, v1beta1.ConditionTeamRoomReady, metav1.ConditionTrue,
			"RoomsReadyWithMembershipDrift", err.Error(), t.Generation)
		return reconcile.Result{}, nil
	}

	setCondition(&t.Status.Conditions, v1beta1.ConditionTeamRoomReady, metav1.ConditionTrue,
		"RoomsReady", "", t.Generation)
	return reconcile.Result{}, nil
}

// collectMatrixIDs returns the non-empty Matrix IDs of all ready
// observations. Callers are responsible for deduping.
func collectMatrixIDs(obs []service.WorkerObservation) []string {
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		if o.MatrixUserID == "" {
			continue
		}
		out = append(out, o.MatrixUserID)
	}
	return out
}

// collectAdminMatrixIDs returns the non-empty Matrix IDs of all observed
// Team admins.
func collectAdminMatrixIDs(obs []service.HumanObservation) []string {
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		if o.MatrixUserID == "" {
			continue
		}
		out = append(out, o.MatrixUserID)
	}
	return out
}
