package controller

import (
	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
)

// computeTeamPhase collapses the observed Team conditions into a single
// high-level phase value. Pending means the Team has not yet reached the
// minimum functional state (no leader, or leader/rooms not yet ready);
// Active means the Team has an operating leader with Team Room ready,
// regardless of per-member readiness. Degraded is reserved for persistent
// structural issues that require human attention (e.g. multi-leader
// conflict). Failed is reserved for hard reconcile errors that prevent
// any progress.
//
// The old logic that escalated to Failed on any transient error is
// intentionally removed — transient errors are represented via
// Conditions + Message, not by demoting Phase away from the last good
// observation.
func computeTeamPhase(s *teamScope, reconcileErr error) string {
	t := s.team
	if reconcileErr != nil {
		if t.Status.Phase == "" {
			return "Failed"
		}
		// Retain last observed Phase on transient error; error detail is
		// captured in Status.Message by the defer-patch path.
		return t.Status.Phase
	}

	if s.multipleLeader {
		return "Degraded"
	}
	if s.leader == nil || !s.leader.Ready {
		return "Pending"
	}
	if t.Status.TeamRoomID == "" || t.Status.LeaderDMRoomID == "" {
		return "Pending"
	}
	return "Active"
}

// pointerToBool is a tiny helper to make peerMentions defaulting consistent.
func effectivePeerMentions(spec v1beta1.TeamSpec) bool {
	if spec.PeerMentions == nil {
		return true
	}
	return *spec.PeerMentions
}
