package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// reconcileHumanRooms drives Status.Rooms toward the desired set built
// from Spec.AccessibleWorkers / AccessibleTeams. Single declarative
// path — the pre-refactor controller had separate handleCreate /
// handleUpdate branches; those are unified here.
//
// Additions: invite via admin (always), then /join as the user (only
// when a user access token is obtainable — see ensureUserToken). When
// the user token cannot be obtained (login failed / stale password), we
// skip /join and record the invite as pending by not appending to
// Status.Rooms; a later reconcile will retry once the user re-establishes
// a password we know about.
//
// Removals: kick via admin. On failure, keep the room in Status.Rooms
// so the next reconcile retries rather than dropping state.
//
// Errors from individual invite/join/kick operations are never returned
// — the function always runs to completion so partial failures don't
// block unrelated rooms.
func (r *HumanReconciler) reconcileHumanRooms(ctx context.Context, s *humanScope) {
	logger := log.FromContext(ctx)
	h := s.human

	desired := buildDesiredHumanRooms(ctx, r.Client, h)

	observed := make(map[string]struct{}, len(h.Status.Rooms))
	for _, rid := range h.Status.Rooms {
		observed[rid] = struct{}{}
	}

	matrixUserID := h.Status.MatrixUserID
	if matrixUserID == "" {
		matrixUserID = s.identity.MatrixUserID
	}

	// Start with currently-observed rooms; we'll prune removals below.
	next := make([]string, 0, len(h.Status.Rooms)+len(desired))
	next = append(next, h.Status.Rooms...)

	powerLevel := humanRoomPowerLevel(h.Spec.PermissionLevel)

	for rid := range desired {
		alreadyMember := false
		if _, ok := observed[rid]; ok {
			alreadyMember = true
		}
		if !alreadyMember {
			if err := r.Provisioner.InviteToRoom(ctx, rid, matrixUserID); err != nil {
				logger.Error(err, "failed to invite human to room", "room", rid)
				continue
			}
			// Acquire a user token lazily — only on the first new-room
			// addition of this reconcile. Steady-state passes (desired ==
			// observed) and revoke-only passes never reach this call, so
			// Matrix Login is not issued on every 5-minute requeue.
			token := r.ensureUserToken(ctx, s)
			if token == "" {
				logger.V(1).Info("user token unavailable; invite-only this cycle",
					"room", rid, "human", h.Name, "username", s.username)
				continue
			}
			if err := r.Provisioner.JoinRoomAs(ctx, rid, token); err != nil {
				logger.Error(err, "failed to join room as human", "room", rid)
				continue
			}
			next = append(next, rid)
		}
		// Grant the human their power level in every room they should be
		// in — new rooms and already-observed ones alike. The existing-room
		// pass is the healing path: legacy rooms were created before power
		// levels accounted for human members, leaving them at the implicit
		// level 0 and 403 on room operations (rename, invite). Non-fatal
		// per this file's error policy; the next cycle retries.
		if err := r.Provisioner.EnsureRoomPowerLevel(ctx, rid, matrixUserID, powerLevel); err != nil {
			logger.Error(err, "failed to ensure human power level", "room", rid, "level", powerLevel)
		}
	}

	// Removals: in-place filter. A failed kick keeps the room so the
	// next reconcile retries, matching pre-refactor behavior.
	kept := next[:0]
	for _, rid := range next {
		if _, ok := desired[rid]; ok {
			kept = append(kept, rid)
			continue
		}
		if err := r.Provisioner.KickFromRoom(ctx, rid, matrixUserID, "access revoked"); err != nil {
			logger.Error(err, "failed to kick human from room", "room", rid)
			kept = append(kept, rid)
		}
	}

	h.Status.Rooms = kept
}

// humanRoomPowerLevel maps the Human CR permission level to the Matrix power
// level granted in rooms the human belongs to. Level 1 (admin equivalent)
// co-owns the rooms (full control); levels 2/3 (team/worker scoped) get
// level 50 — Matrix's default member authority: rename, invite, kick, ban
// and redact (the homeserver defaults all sit at 50), but not power-level
// changes, and only against members strictly below 50 — the manager/leader
// at 100 can never be kicked or banned by the human. This authority is
// accepted and documented in docs/design/room-power-levels.md.
func humanRoomPowerLevel(permissionLevel int) int {
	if permissionLevel == 1 {
		return 100
	}
	return 50
}

// ensureUserToken returns a Matrix access token for the human,
// acquiring one via Login on first call per reconcile and caching it
// in the scope. Returns "" when login fails — callers degrade to
// admin-only invite behavior without surfacing the error (stale
// passwords are an expected condition, not a reconcile failure).
//
// The lazy acquisition is critical: every Login call creates a new
// device session on Tuwunel (the homeserver does not reuse sessions
// when the caller omits device_id), so issuing a Login on every
// 5-minute requeue would accumulate ~288 orphan devices per human per
// day. By gating the Login behind "we actually have a new room to
// /join", a Human whose spec is quiescent triggers zero Logins
// regardless of requeue cadence.
func (r *HumanReconciler) ensureUserToken(ctx context.Context, s *humanScope) string {
	if s.userToken != "" {
		return s.userToken
	}
	// Fresh provisioning set userToken directly; only steady-state
	// reconciles fall through to here. Without a stored password we
	// cannot log in, so return empty and let the caller fall back to
	// admin-only invite.
	if s.identity.Source == nil {
		return ""
	}
	if s.identity.ManagesInitialPassword && !r.Provisioner.MatrixAppServiceEnabled() && s.human.Status.InitialPassword == "" {
		return ""
	}

	token, err := s.identity.Source.EnsureUserToken(ctx, &s.human.Spec, &s.human.Status, s.human.Name)
	if err != nil {
		log.FromContext(ctx).Info("human login with stored password failed; continuing with admin-only room management",
			"name", s.human.Name, "err", err.Error())
		return ""
	}
	s.userToken = token
	return token
}
