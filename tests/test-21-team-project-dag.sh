#!/bin/bash
# test-21-team-project-dag.sh - Case 21: Team project DAG orchestration end-to-end
#
# Tests:
#   Part A (infrastructure): Team storage, S3 policy, skills, DAG resolver, state tracking
#   Part B (room topology): Manager NOT in Team Room / Leader DM / Worker Rooms
#   Part C (e2e via LLM): Admin delegates task in Leader DM, Leader coordinates workers via Team Room
#
# NOTE: This test does NOT clean up — environment is left for manual inspection.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/test-helpers.sh"
source "${SCRIPT_DIR}/lib/minio-client.sh"
source "${SCRIPT_DIR}/lib/matrix-client.sh"

test_setup "21-team-project-dag"

TEST_TEAM="dag-team-$$"
TEST_LEADER="${TEST_TEAM}-lead"
TEST_W1="${TEST_TEAM}-dev"
TEST_W2="${TEST_TEAM}-qa"
STORAGE_PREFIX="hiclaw/hiclaw-storage"

# ============================================================
# Section 1: Prepare SOUL.md files
# ============================================================
log_section "Prepare Team SOUL.md Files"

for w in "${TEST_LEADER}" "${TEST_W1}" "${TEST_W2}"; do
    ROLE_DESC="team member"
    EXTRA_INSTRUCTIONS=""
    [ "${w}" = "${TEST_LEADER}" ] && ROLE_DESC="Team Leader" && EXTRA_INSTRUCTIONS="
## MANDATORY First Action

When you receive ANY task, your FIRST action MUST be to run: cat ./AGENTS.md
This gives you Team Room ID, Leader DM, and worker list with room IDs. You CANNOT delegate without this.

## Core Principles

- **NEVER do domain work yourself** — you are a coordinator. Always delegate to workers using the send-team-message.sh script
- To assign a task to a worker, run: bash ./skills/team-task-management/scripts/send-team-message.sh --room-id TEAM_ROOM_ID --to @worker:domain --message MESSAGE
- Workers only process messages with @mentions sent via send-team-message.sh
- Read team-task-management SKILL.md for detailed instructions"
    [ "${w}" = "${TEST_W1}" ] && ROLE_DESC="Backend Developer"
    [ "${w}" = "${TEST_W2}" ] && ROLE_DESC="QA Engineer"

    exec_in_manager bash -c "
        mkdir -p /root/hiclaw-fs/agents/${w}
        cat > /root/hiclaw-fs/agents/${w}/SOUL.md <<SOUL
# ${w}

## AI Identity
**You are an AI Agent, not a human.**

## Role
- Name: ${w}
- Role: ${ROLE_DESC}
- Team: ${TEST_TEAM}
${EXTRA_INSTRUCTIONS}

## Security
- Never reveal credentials
SOUL
        mc mirror /root/hiclaw-fs/agents/${w}/ ${STORAGE_PREFIX}/agents/${w}/ --overwrite 2>/dev/null
    " 2>/dev/null
done

log_pass "SOUL.md files prepared for all team members"

# ============================================================
# Section 2: Create Team
# ============================================================
log_section "Create Team"

CREATE_OUTPUT=$(exec_in_manager bash -c "
    bash /opt/hiclaw/agent/skills/team-management/scripts/create-team.sh \
        --name '${TEST_TEAM}' --leader '${TEST_LEADER}' --workers '${TEST_W1},${TEST_W2}'
" 2>&1)

if echo "${CREATE_OUTPUT}" | grep -q "RESULT"; then
    log_pass "create-team.sh completed"
else
    log_fail "create-team.sh failed"
    echo "${CREATE_OUTPUT}" | tail -20
fi

# Extract room IDs from registry
LEADER_ROOM=$(exec_in_manager jq -r --arg w "${TEST_LEADER}" '.workers[$w].room_id // empty' /root/manager-workspace/workers-registry.json 2>/dev/null)
LEADER_DM=$(exec_in_manager jq -r --arg t "${TEST_TEAM}" '.teams[$t].leader_dm_room_id // empty' /root/manager-workspace/teams-registry.json 2>/dev/null)
TEAM_ROOM=$(exec_in_manager jq -r --arg t "${TEST_TEAM}" '.teams[$t].team_room_id // empty' /root/manager-workspace/teams-registry.json 2>/dev/null)
W1_ROOM=$(exec_in_manager jq -r --arg w "${TEST_W1}" '.workers[$w].room_id // empty' /root/manager-workspace/workers-registry.json 2>/dev/null)
W2_ROOM=$(exec_in_manager jq -r --arg w "${TEST_W2}" '.workers[$w].room_id // empty' /root/manager-workspace/workers-registry.json 2>/dev/null)

log_info "Leader Room: ${LEADER_ROOM}"
log_info "Leader DM: ${LEADER_DM}"
log_info "Team Room: ${TEAM_ROOM}"

# ============================================================
# Section 3: Verify Team Storage Initialized in MinIO
# ============================================================
log_section "Verify Team Storage Initialization"

for subdir in shared/tasks shared/projects shared/knowledge; do
    KEEP_STAT=$(exec_in_manager mc stat "${STORAGE_PREFIX}/teams/${TEST_TEAM}/${subdir}/.keep" 2>&1)
    if echo "${KEEP_STAT}" | grep -q "Name"; then
        log_pass "teams/${TEST_TEAM}/${subdir}/.keep exists in MinIO"
    else
        log_fail "teams/${TEST_TEAM}/${subdir}/.keep missing in MinIO"
    fi
done

# ============================================================
# Section 4: Verify S3 Policy
# ============================================================
log_section "Verify S3 Policy for Team Members"

WRITE_TEST=$(exec_in_manager bash -c "
    echo 'test' > /tmp/team-storage-test.txt
    mc cp /tmp/team-storage-test.txt ${STORAGE_PREFIX}/teams/${TEST_TEAM}/shared/test-write.txt 2>&1
    mc cat ${STORAGE_PREFIX}/teams/${TEST_TEAM}/shared/test-write.txt 2>/dev/null
    mc rm ${STORAGE_PREFIX}/teams/${TEST_TEAM}/shared/test-write.txt 2>/dev/null
    rm -f /tmp/team-storage-test.txt
" 2>&1)
if echo "${WRITE_TEST}" | grep -q "test"; then
    log_pass "Team storage is writable (functional test)"
else
    log_fail "Team storage write test failed"
fi

# ============================================================
# Section 5: Verify Leader Skills
# ============================================================
log_section "Verify Leader Skills"

for skill in team-task-management team-project-management team-task-coordination; do
    SKILL_EXISTS=$(exec_in_manager bash -c "mc ls '${STORAGE_PREFIX}/agents/${TEST_LEADER}/skills/${skill}/SKILL.md' >/dev/null 2>&1 && echo yes || echo no")
    if [ "${SKILL_EXISTS}" = "yes" ]; then
        log_pass "Leader has ${skill} skill"
    else
        log_fail "Leader missing ${skill} skill"
    fi
done

# ============================================================
# Section 6: Verify Room Topology — Manager NOT in team rooms
# ============================================================
log_section "Verify Room Topology (Manager Delegation Boundary)"

# Login as admin inside container for room membership checks
_check_manager_in_room() {
    local room_id="$1"
    local room_label="$2"
    local room_enc
    room_enc=$(echo "${room_id}" | sed 's/!/%21/g')
    local members
    members=$(exec_in_manager bash -c '
        TOKEN=$(curl -sf -X POST "http://127.0.0.1:6167/_matrix/client/v3/login" \
            -H "Content-Type: application/json" \
            -d "{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"admin\"},\"password\":\"'"${TEST_ADMIN_PASSWORD}"'\"}" | jq -r ".access_token")
        curl -sf "http://127.0.0.1:6167/_matrix/client/v3/rooms/'"${room_enc}"'/members" \
            -H "Authorization: Bearer ${TOKEN}" | jq -r ".chunk[] | select(.content.membership == \"join\") | .state_key"
    ' 2>/dev/null)
    if echo "${members}" | grep -q "@manager:"; then
        log_fail "Manager IS in ${room_label} (should NOT be)"
    else
        log_pass "Manager NOT in ${room_label}"
    fi
}

# Manager should NOT be in these rooms
_check_manager_in_room "${TEAM_ROOM}" "Team Room"
_check_manager_in_room "${LEADER_DM}" "Leader DM"
_check_manager_in_room "${W1_ROOM}" "Worker 1 Room"
_check_manager_in_room "${W2_ROOM}" "Worker 2 Room"

# Manager SHOULD be in Leader Room
LEADER_ROOM_ENC=$(echo "${LEADER_ROOM}" | sed 's/!/%21/g')
LEADER_ROOM_MEMBERS=$(exec_in_manager bash -c '
    TOKEN=$(curl -sf -X POST "http://127.0.0.1:6167/_matrix/client/v3/login" \
        -H "Content-Type: application/json" \
        -d "{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"admin\"},\"password\":\"'"${TEST_ADMIN_PASSWORD}"'\"}" | jq -r ".access_token")
    curl -sf "http://127.0.0.1:6167/_matrix/client/v3/rooms/'"${LEADER_ROOM_ENC}"'/members" \
        -H "Authorization: Bearer ${TOKEN}" | jq -r ".chunk[] | select(.content.membership == \"join\") | .state_key"
' 2>/dev/null)
if echo "${LEADER_ROOM_MEMBERS}" | grep -q "@manager:"; then
    log_pass "Manager IS in Leader Room (correct)"
else
    log_fail "Manager NOT in Leader Room (should be)"
fi

# ============================================================
# Section 7: DAG Resolver Tests (infrastructure)
# ============================================================
log_section "DAG Resolver Tests"

LEADER_HOME="/root/hiclaw-fs/agents/${TEST_LEADER}"
PLAN_PATH="/root/hiclaw-fs/teams/${TEST_TEAM}/projects/tp-infra-test/plan.md"

# Copy scripts to Leader HOME (same as create-team.sh does via skills push)
exec_in_manager bash -c "
    mkdir -p '${LEADER_HOME}/skills/team-project-management/scripts'
    mkdir -p '${LEADER_HOME}/skills/team-task-management/scripts'
    cp /opt/hiclaw/agent/team-leader-agent/skills/team-project-management/scripts/resolve-dag.sh \
       '${LEADER_HOME}/skills/team-project-management/scripts/'
    cp /opt/hiclaw/agent/team-leader-agent/skills/team-project-management/scripts/create-team-project.sh \
       '${LEADER_HOME}/skills/team-project-management/scripts/'
    cp /opt/hiclaw/agent/team-leader-agent/skills/team-task-management/scripts/manage-team-state.sh \
       '${LEADER_HOME}/skills/team-task-management/scripts/'
" 2>/dev/null

# Write DAG plan
exec_in_manager bash -c "
mkdir -p /root/hiclaw-fs/teams/${TEST_TEAM}/projects/tp-infra-test
cat > '${PLAN_PATH}' <<'PLAN'
# Team Project: Infra Test

## DAG Task Plan

- [ ] st-01 — Design schema (assigned: @${TEST_W1}:${TEST_MATRIX_DOMAIN})
- [ ] st-02 — Design API (assigned: @${TEST_W1}:${TEST_MATRIX_DOMAIN})
- [ ] st-03 — Implement backend (assigned: @${TEST_W1}:${TEST_MATRIX_DOMAIN}, depends: st-01, st-02)
- [ ] st-04 — Write tests (assigned: @${TEST_W2}:${TEST_MATRIX_DOMAIN}, depends: st-02)
- [ ] st-05 — Integration test (assigned: @${TEST_W2}:${TEST_MATRIX_DOMAIN}, depends: st-03, st-04)

## Change Log
PLAN
" 2>/dev/null

# Validate
VALIDATE_OUTPUT=$(exec_in_manager bash -c "
    export HOME='${LEADER_HOME}'
    cd '${LEADER_HOME}'
    bash ${LEADER_HOME}/skills/team-project-management/scripts/resolve-dag.sh \
        --plan '${PLAN_PATH}' --action validate
" 2>&1)
VALID=$(echo "${VALIDATE_OUTPUT}" | jq -r '.valid // empty' 2>/dev/null)
assert_eq "true" "${VALID}" "DAG validate: no cycles"

# Wave 1
READY_OUTPUT=$(exec_in_manager bash -c "
    export HOME='${LEADER_HOME}'
    cd '${LEADER_HOME}'
    bash ${LEADER_HOME}/skills/team-project-management/scripts/resolve-dag.sh \
        --plan '${PLAN_PATH}' --action ready
" 2>&1)
READY_IDS=$(echo "${READY_OUTPUT}" | jq -r '[.ready_tasks[].id] | sort | join(",")' 2>/dev/null)
assert_eq "st-01,st-02" "${READY_IDS}" "DAG wave 1: st-01, st-02 ready"

# Complete st-01, st-02 → wave 2
exec_in_manager bash -c "
    sed -i 's/- \[ \] st-01/- [x] st-01/' '${PLAN_PATH}'
    sed -i 's/- \[ \] st-02/- [x] st-02/' '${PLAN_PATH}'
" 2>/dev/null

WAVE2_OUTPUT=$(exec_in_manager bash -c "
    export HOME='${LEADER_HOME}'
    cd '${LEADER_HOME}'
    bash ${LEADER_HOME}/skills/team-project-management/scripts/resolve-dag.sh \
        --plan '${PLAN_PATH}' --action ready
" 2>&1)
WAVE2_IDS=$(echo "${WAVE2_OUTPUT}" | jq -r '[.ready_tasks[].id] | sort | join(",")' 2>/dev/null)
assert_eq "st-03,st-04" "${WAVE2_IDS}" "DAG wave 2: st-03, st-04 ready (parallel)"

# Complete st-03, st-04 → wave 3
exec_in_manager bash -c "
    sed -i 's/- \[ \] st-03/- [x] st-03/' '${PLAN_PATH}'
    sed -i 's/- \[ \] st-04/- [x] st-04/' '${PLAN_PATH}'
" 2>/dev/null

WAVE3_OUTPUT=$(exec_in_manager bash -c "
    export HOME='${LEADER_HOME}'
    cd '${LEADER_HOME}'
    bash ${LEADER_HOME}/skills/team-project-management/scripts/resolve-dag.sh \
        --plan '${PLAN_PATH}' --action ready
" 2>&1)
WAVE3_IDS=$(echo "${WAVE3_OUTPUT}" | jq -r '[.ready_tasks[].id] | join(",")' 2>/dev/null)
assert_eq "st-05" "${WAVE3_IDS}" "DAG wave 3: st-05 ready"

# Cycle detection
exec_in_manager bash -c "
cat > /tmp/cycle-plan.md <<'PLAN'
# Cycle Test
## DAG Task Plan
- [ ] st-01 — A (assigned: @w1:d, depends: st-03)
- [ ] st-02 — B (assigned: @w2:d, depends: st-01)
- [ ] st-03 — C (assigned: @w1:d, depends: st-02)
PLAN
" 2>/dev/null

CYCLE_OUTPUT=$(exec_in_manager bash -c "
    export HOME='${LEADER_HOME}'
    cd '${LEADER_HOME}'
    bash /opt/hiclaw/agent/team-leader-agent/skills/team-project-management/scripts/resolve-dag.sh \
        --plan /tmp/cycle-plan.md --action validate 2>&1 || true
" 2>&1)
if echo "${CYCLE_OUTPUT}" | grep -q '"valid": false'; then
    log_pass "DAG cycle detection: correctly identified cycle"
else
    log_fail "DAG cycle detection failed"
fi

# ============================================================
# Section 8: State Tracking Tests
# ============================================================
log_section "State Tracking"

STATE_SCRIPT="${LEADER_HOME}/skills/team-task-management/scripts/manage-team-state.sh"

exec_in_manager bash -c "
    rm -f '${LEADER_HOME}/team-state.json'
    export HOME='${LEADER_HOME}'
    cd '${LEADER_HOME}'
    cd '${LEADER_HOME}'
    bash '${STATE_SCRIPT}' --action init
" 2>/dev/null

# Manager source
ADD_OUT=$(exec_in_manager bash -c "
    export HOME='${LEADER_HOME}'
    cd '${LEADER_HOME}'
    cd '${LEADER_HOME}'
    bash '${STATE_SCRIPT}' --action add-project --project-id tp-mgr --title 'Mgr Project' --source manager --parent-task-id task-mgr
" 2>&1)
assert_contains "${ADD_OUT}" "OK" "add-project (manager source)"

# Team Admin source
ADD_OUT2=$(exec_in_manager bash -c "
    export HOME='${LEADER_HOME}'
    cd '${LEADER_HOME}'
    bash '${STATE_SCRIPT}' --action add-project --project-id tp-admin --title 'Admin Project' --source team-admin --requester '@admin:domain'
" 2>&1)
assert_contains "${ADD_OUT2}" "OK" "add-project (team-admin source)"

STATE_JSON=$(exec_in_manager cat "${LEADER_HOME}/team-state.json" 2>/dev/null)
assert_eq "2" "$(echo "${STATE_JSON}" | jq '.active_projects | length')" "2 active projects in state"

# Complete manager project
exec_in_manager bash -c "
    export HOME='${LEADER_HOME}'
    cd '${LEADER_HOME}'
    bash '${STATE_SCRIPT}' --action complete-project --project-id tp-mgr
" 2>/dev/null
STATE_JSON2=$(exec_in_manager cat "${LEADER_HOME}/team-state.json" 2>/dev/null)
assert_eq "1" "$(echo "${STATE_JSON2}" | jq '.active_projects | length')" "1 project remaining after completion"
assert_eq "tp-admin" "$(echo "${STATE_JSON2}" | jq -r '.active_projects[0].project_id')" "Remaining project is admin project"

# ============================================================
# Section 9: End-to-End LLM Test — Admin delegates via Leader DM
# ============================================================
log_section "E2E: Admin Delegates Task via Leader DM"

if ! require_llm_key; then
    log_info "SKIP: No LLM API key — skipping e2e LLM test"
    test_teardown "21-team-project-dag"
    test_summary
    exit 0
fi

# Wait for worker containers
for w in "${TEST_LEADER}" "${TEST_W1}" "${TEST_W2}"; do
    wait_for_worker_container "${w}" 120 || log_fail "Container ${w} not running"
done

# Send task from Admin directly in Leader DM
assert_not_empty "${LEADER_DM}" "Leader DM room exists"

exec_in_manager bash -c '
TOKEN=$(curl -sf -X POST "http://127.0.0.1:6167/_matrix/client/v3/login" \
    -H "Content-Type: application/json" \
    -d "{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"admin\"},\"password\":\"'"${TEST_ADMIN_PASSWORD}"'\"}" | jq -r ".access_token")
ROOM_ENC=$(echo "'"${LEADER_DM}"'" | sed "s/!/%21/g")
TXN=$(date +%s%N)
curl -sf -X PUT "http://127.0.0.1:6167/_matrix/client/v3/rooms/${ROOM_ENC}/send/m.room.message/${TXN}" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Content-Type: application/json" \
    -d "{\"msgtype\":\"m.text\",\"body\":\"Please build a simple REST API for a todo-list app. The dev worker should design the API endpoints first, then implement them. The QA worker should write test cases after the API design is done. Coordinate your team and report back when everything is complete.\"}"
' 2>/dev/null

log_info "Task sent to Leader via Leader DM. Monitoring rooms..."

# Poll for Leader activity in Team Room (up to 10 minutes)
TEAM_ROOM_ENC=$(echo "${TEAM_ROOM}" | sed 's/!/%21/g')
LEADER_DM_ENC=$(echo "${LEADER_DM}" | sed 's/!/%21/g')

LEADER_RESPONDED=false
for i in $(seq 1 20); do
    sleep 30
    log_info "Polling rooms... (${i}/20, elapsed: $((i*30))s)"

    # Check Team Room for Leader messages
    TEAM_MSGS=$(exec_in_manager bash -c '
        TOKEN=$(curl -sf -X POST "http://127.0.0.1:6167/_matrix/client/v3/login" \
            -H "Content-Type: application/json" \
            -d "{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"admin\"},\"password\":\"'"${TEST_ADMIN_PASSWORD}"'\"}" | jq -r ".access_token")
        curl -sf "http://127.0.0.1:6167/_matrix/client/v3/rooms/'"${TEAM_ROOM_ENC}"'/messages?dir=b&limit=10" \
            -H "Authorization: Bearer ${TOKEN}" | jq -r ".chunk[] | select(.type == \"m.room.message\") | \"\(.sender | split(\":\")[0]): \(.content.body[0:200])\""
    ' 2>/dev/null)

    if echo "${TEAM_MSGS}" | grep -q "@${TEST_LEADER}:"; then
        log_info "Leader is active in Team Room"
        LEADER_RESPONDED=true
    fi

    # Check if any worker has responded in Team Room
    if echo "${TEAM_MSGS}" | grep -qi "${TEST_W1}\|${TEST_W2}"; then
        log_info "Workers are responding in Team Room"
        break
    fi

    # Also check Leader DM for any response back to admin
    DM_MSGS=$(exec_in_manager bash -c '
        TOKEN=$(curl -sf -X POST "http://127.0.0.1:6167/_matrix/client/v3/login" \
            -H "Content-Type: application/json" \
            -d "{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"admin\"},\"password\":\"'"${TEST_ADMIN_PASSWORD}"'\"}" | jq -r ".access_token")
        curl -sf "http://127.0.0.1:6167/_matrix/client/v3/rooms/'"${LEADER_DM_ENC}"'/messages?dir=b&limit=5" \
            -H "Authorization: Bearer ${TOKEN}" | jq -r ".chunk[] | select(.type == \"m.room.message\" and (.sender | contains(\"'"${TEST_LEADER}"'\"))) | .content.body[0:200]"
    ' 2>/dev/null)

    if [ -n "${DM_MSGS}" ]; then
        log_info "Leader responded in Leader DM"
        LEADER_RESPONDED=true
    fi
done

if [ "${LEADER_RESPONDED}" = "true" ]; then
    log_pass "Leader received and processed task from Admin via Leader DM"
else
    log_fail "Leader did not respond within timeout"
fi

# Final snapshot of all rooms
log_section "Final Room Snapshot"

exec_in_manager bash -c '
TOKEN=$(curl -sf -X POST "http://127.0.0.1:6167/_matrix/client/v3/login" \
    -H "Content-Type: application/json" \
    -d "{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"admin\"},\"password\":\"'"${TEST_ADMIN_PASSWORD}"'\"}" | jq -r ".access_token")

echo "--- Leader DM (Admin <-> Leader) ---"
ROOM_ENC=$(echo "'"${LEADER_DM}"'" | sed "s/!/%21/g")
curl -sf "http://127.0.0.1:6167/_matrix/client/v3/rooms/${ROOM_ENC}/messages?dir=b&limit=10" \
    -H "Authorization: Bearer ${TOKEN}" | jq -r ".chunk[] | select(.type == \"m.room.message\") | \"\(.sender | split(\":\")[0]): \(.content.body[0:300])\""

echo ""
echo "--- Team Room ---"
ROOM_ENC=$(echo "'"${TEAM_ROOM}"'" | sed "s/!/%21/g")
curl -sf "http://127.0.0.1:6167/_matrix/client/v3/rooms/${ROOM_ENC}/messages?dir=b&limit=15" \
    -H "Authorization: Bearer ${TOKEN}" | jq -r ".chunk[] | select(.type == \"m.room.message\") | \"\(.sender | split(\":\")[0]): \(.content.body[0:300])\""

echo ""
echo "--- Leader Room (Manager <-> Leader) ---"
ROOM_ENC=$(echo "'"${LEADER_ROOM}"'" | sed "s/!/%21/g")
curl -sf "http://127.0.0.1:6167/_matrix/client/v3/rooms/${ROOM_ENC}/messages?dir=b&limit=10" \
    -H "Authorization: Bearer ${TOKEN}" | jq -r ".chunk[] | select(.type == \"m.room.message\") | \"\(.sender | split(\":\")[0]): \(.content.body[0:300])\""
' 2>&1

log_info "Environment NOT cleaned up — inspect via Element at http://127.0.0.1:${TEST_ELEMENT_PORT:-18088}"

test_teardown "21-team-project-dag"
test_summary
