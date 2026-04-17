package controller

import (
	"context"
	"fmt"
	"sort"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconcileMembers is the first phase: it observes Worker CRs that claim
// membership in this team, classifies them into leader / members,
// populates the scope, and projects the observations into Team.status.
// It also emits LeaderResolved / NoLeader / MultipleLeaders conditions
// reflecting the structural state of the team.
func (r *TeamReconciler) reconcileMembers(ctx context.Context, s *teamScope) (reconcile.Result, error) {
	t := s.team
	logger := log.FromContext(ctx)

	obs, err := r.Observer.ListTeamMembers(ctx, t.Name)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("list team members: %w", err)
	}

	var (
		leaders []service.WorkerObservation
		members []service.WorkerObservation
	)
	for _, w := range obs {
		switch w.Role {
		case v1beta1.WorkerRoleTeamLeader:
			leaders = append(leaders, w)
		case v1beta1.WorkerRoleTeamWorker:
			members = append(members, w)
		}
	}

	sort.Slice(leaders, func(i, j int) bool { return leaders[i].Name < leaders[j].Name })
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })

	switch len(leaders) {
	case 0:
		s.leader = nil
		s.multipleLeader = false
		setCondition(&t.Status.Conditions, v1beta1.ConditionLeaderResolved, metav1.ConditionFalse,
			v1beta1.ConditionNoLeader, "no Worker with role=team_leader references this team", t.Generation)
	case 1:
		leader := leaders[0]
		s.leader = &leader
		s.multipleLeader = false
		setCondition(&t.Status.Conditions, v1beta1.ConditionLeaderResolved, metav1.ConditionTrue,
			"LeaderFound", fmt.Sprintf("leader is %q", leader.Name), t.Generation)
	default:
		first := leaders[0]
		s.leader = &first
		s.multipleLeader = true
		names := make([]string, 0, len(leaders))
		for _, l := range leaders {
			names = append(names, l.Name)
		}
		setCondition(&t.Status.Conditions, v1beta1.ConditionLeaderResolved, metav1.ConditionFalse,
			v1beta1.ConditionMultipleLeaders,
			fmt.Sprintf("multiple team_leader Workers detected: %v", names),
			t.Generation)
	}

	s.members = members

	t.Status.Members = projectMembers(members)
	t.Status.Leader = projectLeader(s.leader)
	t.Status.TotalMembers = len(members)
	t.Status.ReadyMembers = countReady(members)

	if len(members) == 0 || t.Status.ReadyMembers == len(members) {
		setCondition(&t.Status.Conditions, v1beta1.ConditionMembersHealthy, metav1.ConditionTrue,
			"AllMembersReady", "", t.Generation)
	} else {
		setCondition(&t.Status.Conditions, v1beta1.ConditionMembersHealthy, metav1.ConditionFalse,
			"SomeMembersNotReady",
			fmt.Sprintf("%d/%d members ready", t.Status.ReadyMembers, len(members)),
			t.Generation)
	}

	logger.V(1).Info("members observed",
		"team", t.Name,
		"leader", t.Status.Leader,
		"total", t.Status.TotalMembers,
		"ready", t.Status.ReadyMembers,
		"multiLeader", s.multipleLeader)
	return reconcile.Result{}, nil
}

// projectMembers converts the internal WorkerObservation slice into the
// CR-level TeamMemberObservation slice written to Team.status.
func projectMembers(members []service.WorkerObservation) []v1beta1.TeamMemberObservation {
	if len(members) == 0 {
		return nil
	}
	out := make([]v1beta1.TeamMemberObservation, 0, len(members))
	for _, m := range members {
		out = append(out, v1beta1.TeamMemberObservation{
			Name:         m.Name,
			Role:         m.Role,
			MatrixUserID: m.MatrixUserID,
			Ready:        m.Ready,
		})
	}
	return out
}

// projectLeader converts the internal observation into the CR-level
// TeamLeaderObservation struct.
func projectLeader(leader *service.WorkerObservation) *v1beta1.TeamLeaderObservation {
	if leader == nil {
		return nil
	}
	return &v1beta1.TeamLeaderObservation{
		Name:         leader.Name,
		MatrixUserID: leader.MatrixUserID,
		Ready:        leader.Ready,
	}
}

// countReady returns the number of observations with Ready=true.
func countReady(obs []service.WorkerObservation) int {
	n := 0
	for _, o := range obs {
		if o.Ready {
			n++
		}
	}
	return n
}

// setCondition upserts a metav1.Condition into conds keyed by type. It
// only bumps LastTransitionTime when Status changes, preserving the
// original transition timestamp across no-op reconciles.
func setCondition(conds *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, message string, gen int64) {
	now := metav1.Now()
	for i := range *conds {
		c := &(*conds)[i]
		if c.Type != condType {
			continue
		}
		if c.Status != status {
			c.LastTransitionTime = now
		}
		c.Status = status
		c.Reason = reason
		c.Message = message
		c.ObservedGeneration = gen
		return
	}
	*conds = append(*conds, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: gen,
		LastTransitionTime: now,
	})
}
