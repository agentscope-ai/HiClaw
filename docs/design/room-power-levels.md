# Room Power Levels for Human Members

Status: implemented
Surfaces: `m.room.power_levels` in team / worker / project rooms; `create-project.sh --grant-admin`

## Problem

Humans are invited into worker and team rooms by the human reconciler and
into project rooms by the Manager — but nothing ever grants them a Matrix
power level. Two consequences:

1. **Rooms created before power levels accounted for humans** (and rooms
   where the human joined later) carry a `m.room.power_levels` state that
   lists only manager / leader / admin at 100 and workers at 0. The human
   sits at the implicit level 0.
2. **Rooms that never had the state set at all** (legacy) fall back to the
   homeserver's strict defaults.

Either way, a human operator gets `403` on every room operation — renaming
the room, inviting a colleague, even housekeeping. The room works for the
bots that own it and is unusable for the person it is meant for.

## Design

**Declarative grant in the human room reconcile.** The human reconciler
already walks the full desired room set on every cycle (new rooms: invite +
join; observed rooms: skip). It now additionally ensures the human's power
level in *every* desired room — new and already-observed. The
observed-room pass is the healing path: legacy rooms are fixed on the first
reconcile after deployment without any manual backfill.

- Level mapping (`humanRoomPowerLevel`): `permissionLevel 1` → 100
  (co-owner: full room control, matching the admin-equivalent scope);
  levels 2/3 → 50 (Matrix's default member authority).
- **Level-50 authority — explicitly accepted.** Matrix homeserver defaults
  gate `kick`, `ban`, and `redact` at 50, so an L2/L3 human at level 50 can
  rename the room, invite members, kick/ban members strictly below 50
  (the workers at 0 — but never the manager/leader/admin at 100), and
  redact any message in the room. They cannot change power levels (100) or
  create the room. We accept this authority rather than raising the
  `kick`/`ban`/`redact` thresholds to 100:
  - The humans at 50 are operators scoped to those rooms; the room's
    manager sits at 100 above them, so kick/ban cannot be turned against
    the team's control plane.
  - Workers are service accounts; a human kicking/banning a stuck worker
    is reversible housekeeping (the reconciler re-invites membership on
    the next cycle) and is a useful operator lever, not a security
    escalation.
  - Redact at 50 is message cleanup within a room the human is already a
    member of; the alternative (raising thresholds in every room-creation
    path) would change the security posture of all worker/team/project
    rooms — a system-wide policy change out of scope for this PR.
- The grant is a **merge**, never a replace: `Provisioner.
  EnsureRoomPowerLevel` reads the current `m.room.power_levels`
  (`matrix.Client.GetRoomState`, new — admin identity, 404 → empty state),
  adds/raises the human's entry in `users`, preserves every other user and
  every non-user setting (`users_default`, `state_default`, `ban`, …), and
  writes back only when the level actually changed. Steady state = one GET
  per room per cycle, zero writes.
- Errors are non-fatal per the reconcile's existing error policy: a failed
  grant is logged and retried on the next cycle; the room is still recorded
  in `status.rooms`.

**Project rooms.** The Controller never creates project rooms; the Manager
does, via `create-project.sh`, which already writes a
`power_level_content_override` (manager + admin at 100, workers at 0) but
has no way to lift a human operator. New optional flag:

```
create-project.sh --id p1 --title T --workers w1,w2 --grant-admin luo,sunzong
```

`--grant-admin` accepts local parts or full Matrix IDs and adds each user at
level 100 to the creation-time override. Rooms created before this change
are healed one-time by the Manager (a `PUT m.room.power_levels` per room) —
a one-off operations task, not part of this PR.

## What is not changed

- Worker / team / DM room creation keeps its existing power levels
  (manager / admin / leader at 100, workers at 0).
- Worker service accounts still cannot manage rooms (level 0 unchanged).
- No CRD change; the mapping is derived from the existing
  `spec.permissionLevel`.

## Tests

- `internal/matrix/client_test.go` — `TestGetRoomState`: returns the state
  **content** (not the event envelope) with the admin token; missing state
  → `(nil, nil)`, not an error.
- `internal/service/provisioner_power_test.go`: legacy room → write with
  the user's level; existing users merged and untouched; extension fields
  (`events`, `invite`, `notifications`) preserved through the write — only
  the target users entry is mutated; exact-match level → no write;
  **demotion revokes: a user at 100 granted 50 is lowered to 50**; read
  error propagates with no write; state without a `users` map handled;
  second grant preserves the first (JSON round-trip semantics).
- `internal/controller/human_controller_test.go`:
  `TestHumanReconciler_PowerLevelMapping` (level 1 → 100 in both a new room
  and an already-observed room; grant targets the human's Matrix ID),
  `TestHumanReconciler_PowerLevelL2GetsDefault` (level 2 → 50),
  `TestHumanReconciler_PowerLevelErrorNonFatal` (grant failure does not
  block the reconcile; room still recorded).
