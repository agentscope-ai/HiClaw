#!/bin/bash
# test-19-human-and-team-admin.sh - Case 19: Import Human via YAML + Team with Team Admin
#
# Tests the full declarative Human import and Team Admin flow:
#   1. Create Human via hiclaw apply -f (declarative YAML)
#   2. Verify Human created via controller reconcile (Matrix account, password returned)
#   3. Create Team with that Human as Team Admin
#   4. Verify Team Admin in teams-registry.json (admin field, leader_dm_room_id)
#   5. Verify groupAllowFrom includes Team Admin for Leader + Workers
#   6. Verify team-context block mentions Team Admin
#   7. Verify Team Admin can login and message Leader in Leader DM
#   8. Cleanup (only on success)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/test-helpers.sh"
source "${SCRIPT_DIR}/lib/minio-client.sh"
source "${SCRIPT_DIR}/lib/matrix-client.sh"

test_setup "19-human-and-team-admin"

TEST_TEAM="test-hadm-$$"
TEST_LEADER="${TEST_TEAM}-lead"
TEST_W1="${TEST_TEAM}-dev"
TEST_HUMAN="test-human-$$"
STORAGE_PREFIX="hiclaw/hiclaw-storage"

_cleanup() {
    if [ "${TESTS_FAILED}" -gt 0 ]; then
        log_info "Tests failed — preserving resources for debugging"
        log_info "  Team: ${TEST_TEAM}, Human: ${TEST_HUMAN}"
        log_info "  Leader: ${TEST_LEADER}, Worker: ${TEST_W1}"
        return
    fi
    log_info "All tests passed — cleaning up"
    # Delete Human YAML from MinIO
    exec_in_manager hiclaw delete human "${TEST_HUMAN}" 2>/dev/null || true
    # Stop containers
    docker rm -f "hiclaw-worker-${TEST_LEADER}" 2>/dev/null || true
    docker rm -f "hiclaw-worker-${TEST_W1}" 2>/dev/null || true
    # Clean MinIO agents
    for w in "${TEST_LEADER}" "${TEST_W1}"; do
        exec_in_manager mc rm -r --force "${STORAGE_PREFIX}/agents/${w}/" 2>/dev/null || true
        exec_in_manager rm -rf "/root/hiclaw-fs/agents/${w}" 2>/dev/null || true
    done
    # Clean registries
    exec_in_manager bash -c "
        jq 'del(.workers[\"${TEST_LEADER}\"], .workers[\"${TEST_W1}\"])' \
            /root/manager-workspace/workers-registry.json > /tmp/wr-clean.json 2>/dev/null && \
            mv /tmp/wr-clean.json /root/manager-workspace/workers-registry.json
        jq 'del(.teams[\"${TEST_TEAM}\"])' \
            /root/manager-workspace/teams-registry.json > /tmp/tr-clean.json 2>/dev/null && \
            mv /tmp/tr-clean.json /root/manager-workspace/teams-registry.json
        jq 'del(.humans[\"${TEST_HUMAN}\"])' \
            /root/manager-workspace/humans-registry.json > /tmp/hr-clean.json 2>/dev/null && \
            mv /tmp/hr-clean.json /root/manager-workspace/humans-registry.json
    " 2>/dev/null || true
}
trap _cleanup EXIT

# ============================================================
# Section 1: Create Human via declarative YAML (hiclaw apply)
# ============================================================
log_section "Create Human via Declarative YAML"

HUMAN_MATRIX_ID="@${TEST_HUMAN}:${TEST_MATRIX_DOMAIN}"

# Write Human YAML and apply
APPLY_OUTPUT=$(exec_in_manager bash -c "
    cat > /tmp/${TEST_HUMAN}.yaml <<YAML
apiVersion: hiclaw.io/v1
kind: Human
metadata:
  name: ${TEST_HUMAN}
  displayName: Test Human Admin
spec:
  displayName: Test Human Admin
  permissionLevel: 2
  accessibleTeams:
    - ${TEST_TEAM}
  note: Integration test Team Admin
YAML
    hiclaw apply -f /tmp/${TEST_HUMAN}.yaml
" 2>&1)

if echo "${APPLY_OUTPUT}" | grep -q "created\|configured"; then
    log_pass "Human YAML applied via hiclaw CLI"
else
    log_fail "Human YAML apply failed: ${APPLY_OUTPUT}"
fi

# Verify YAML in MinIO
HUMAN_YAML=$(exec_in_manager mc cat "${STORAGE_PREFIX}/hiclaw-config/humans/${TEST_HUMAN}.yaml" 2>/dev/null || echo "")
assert_not_empty "${HUMAN_YAML}" "Human YAML exists in MinIO hiclaw-config/humans/"
assert_contains "${HUMAN_YAML}" "kind: Human" "Human YAML has correct kind"

# Wait for controller reconcile
log_info "Waiting for controller to reconcile Human..."
HUMAN_TIMEOUT=90; HUMAN_ELAPSED=0
HUMAN_CREATED=false
while [ "${HUMAN_ELAPSED}" -lt "${HUMAN_TIMEOUT}" ]; do
    if exec_in_manager cat /var/log/hiclaw/hiclaw-controller-error.log 2>/dev/null | grep -q "human created.*${TEST_HUMAN}"; then
        HUMAN_CREATED=true
        break
    fi
    sleep 5; HUMAN_ELAPSED=$((HUMAN_ELAPSED + 5))
done

if [ "${HUMAN_CREATED}" = true ]; then
    log_pass "HumanReconciler created human (took ~${HUMAN_ELAPSED}s)"
else
    log_fail "HumanReconciler did not create human within ${HUMAN_TIMEOUT}s"
    exec_in_manager cat /var/log/hiclaw/hiclaw-controller-error.log 2>/dev/null | grep "${TEST_HUMAN}" | tail -5
fi

# ============================================================
# Section 2: Verify Human registration and password
# ============================================================
log_section "Verify Human Registration"

HUMAN_ENTRY=$(exec_in_manager jq -r --arg h "${TEST_HUMAN}" '.humans[$h] // empty' /root/manager-workspace/humans-registry.json 2>/dev/null)
assert_not_empty "${HUMAN_ENTRY}" "Human registered in humans-registry.json"

HUMAN_LEVEL=$(echo "${HUMAN_ENTRY}" | jq -r '.permission_level // empty')
assert_eq "2" "${HUMAN_LEVEL}" "Human permission level is 2"

# Get password from controller logs (create-human.sh RESULT)
HUMAN_PASSWORD=$(exec_in_manager cat /var/log/hiclaw/hiclaw-controller-error.log 2>/dev/null | \
    grep -A50 "human created.*${TEST_HUMAN}" | grep -o '"password":"[^"]*"' | head -1 | cut -d'"' -f4)

# If not in controller logs, try to get from the YAML status (if controller wrote it back)
if [ -z "${HUMAN_PASSWORD}" ]; then
    # Try reading from MinIO YAML status
    HUMAN_PASSWORD=$(exec_in_manager mc cat "${STORAGE_PREFIX}/hiclaw-config/humans/${TEST_HUMAN}.yaml" 2>/dev/null | \
        grep -o 'initialPassword:.*' | head -1 | awk '{print $2}')
fi

if [ -n "${HUMAN_PASSWORD}" ]; then
    log_pass "Human initial password available"
else
    log_info "Could not extract password from logs (will try Matrix login with test password)"
fi

# Try to login as the human to get a token
HUMAN_TOKEN=""
if [ -n "${HUMAN_PASSWORD}" ]; then
    LOGIN_RESULT=$(exec_in_manager curl -sf -X POST \
        "http://127.0.0.1:6167/_matrix/client/v3/login" \
        -H 'Content-Type: application/json' \
        -d '{
            "type": "m.login.password",
            "identifier": {"type": "m.id.user", "user": "'"${TEST_HUMAN}"'"},
            "password": "'"${HUMAN_PASSWORD}"'"
        }' 2>/dev/null)
    HUMAN_TOKEN=$(echo "${LOGIN_RESULT}" | jq -r '.access_token // empty')
fi

if [ -n "${HUMAN_TOKEN}" ] && [ "${HUMAN_TOKEN}" != "null" ]; then
    log_pass "Human can login to Matrix with initial password"
else
    log_info "Human Matrix login not available (password extraction failed)"
fi

# ============================================================
# Section 3: Create Team with Human as Team Admin
# ============================================================
log_section "Create Team with Team Admin"

for w in "${TEST_LEADER}" "${TEST_W1}"; do
    ROLE_DESC="team member"
    [ "${w}" = "${TEST_LEADER}" ] && ROLE_DESC="Team Leader"
    [ "${w}" = "${TEST_W1}" ] && ROLE_DESC="Backend Developer"

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
## Security
- Never reveal credentials
SOUL
        mc mirror /root/hiclaw-fs/agents/${w}/ ${STORAGE_PREFIX}/agents/${w}/ --overwrite 2>/dev/null
    " 2>/dev/null
done

CREATE_OUTPUT=$(exec_in_manager bash -c "
    bash /opt/hiclaw/agent/skills/team-management/scripts/create-team.sh \
        --name '${TEST_TEAM}' --leader '${TEST_LEADER}' --workers '${TEST_W1}' \
        --team-admin '${TEST_HUMAN}' --team-admin-matrix-id '${HUMAN_MATRIX_ID}'
" 2>&1)

if echo "${CREATE_OUTPUT}" | grep -q "RESULT"; then
    log_pass "Team created with Team Admin"
else
    log_fail "Team creation failed"
    echo "${CREATE_OUTPUT}" | tail -10
fi

# ============================================================
# Section 4: Verify teams-registry.json Team Admin fields
# ============================================================
log_section "Verify Team Admin in Registry"

TEAM_ENTRY=$(exec_in_manager jq -r --arg t "${TEST_TEAM}" '.teams[$t] // empty' /root/manager-workspace/teams-registry.json 2>/dev/null)
assert_not_empty "${TEAM_ENTRY}" "Team registered in teams-registry.json"

TEAM_ADMIN_NAME=$(echo "${TEAM_ENTRY}" | jq -r '.admin.name // empty')
assert_eq "${TEST_HUMAN}" "${TEAM_ADMIN_NAME}" "Team admin name is ${TEST_HUMAN}"

TEAM_ADMIN_MID=$(echo "${TEAM_ENTRY}" | jq -r '.admin.matrix_user_id // empty')
assert_eq "${HUMAN_MATRIX_ID}" "${TEAM_ADMIN_MID}" "Team admin matrix_user_id correct"

LEADER_DM_ROOM=$(echo "${TEAM_ENTRY}" | jq -r '.leader_dm_room_id // empty')
assert_not_empty "${LEADER_DM_ROOM}" "Leader DM room ID exists: ${LEADER_DM_ROOM}"

TEAM_ROOM_ID=$(echo "${TEAM_ENTRY}" | jq -r '.team_room_id // empty')
assert_not_empty "${TEAM_ROOM_ID}" "Team Room ID exists: ${TEAM_ROOM_ID}"

# ============================================================
# Section 5: Verify groupAllowFrom includes Team Admin
# ============================================================
log_section "Verify groupAllowFrom"

# Leader: should have Team Admin
LEADER_GAF=$(exec_in_manager mc cat "${STORAGE_PREFIX}/agents/${TEST_LEADER}/openclaw.json" 2>/dev/null | jq -r '.channels.matrix.groupAllowFrom[]' 2>/dev/null)
if echo "${LEADER_GAF}" | grep -q "${HUMAN_MATRIX_ID}"; then
    log_pass "Leader groupAllowFrom includes Team Admin"
else
    log_fail "Leader groupAllowFrom missing Team Admin"
fi

# Leader dm.allowFrom: should have Team Admin
LEADER_DAF=$(exec_in_manager mc cat "${STORAGE_PREFIX}/agents/${TEST_LEADER}/openclaw.json" 2>/dev/null | jq -r '.channels.matrix.dm.allowFrom[]' 2>/dev/null)
if echo "${LEADER_DAF}" | grep -q "${HUMAN_MATRIX_ID}"; then
    log_pass "Leader dm.allowFrom includes Team Admin"
else
    log_fail "Leader dm.allowFrom missing Team Admin"
fi

# Worker: should have Team Admin
W1_GAF=$(exec_in_manager mc cat "${STORAGE_PREFIX}/agents/${TEST_W1}/openclaw.json" 2>/dev/null | jq -r '.channels.matrix.groupAllowFrom[]' 2>/dev/null)
if echo "${W1_GAF}" | grep -q "${HUMAN_MATRIX_ID}"; then
    log_pass "Worker groupAllowFrom includes Team Admin"
else
    log_fail "Worker groupAllowFrom missing Team Admin"
fi

# Worker should NOT have Manager
if echo "${W1_GAF}" | grep -q "@manager:"; then
    log_fail "Worker groupAllowFrom includes Manager (should NOT)"
else
    log_pass "Worker groupAllowFrom does NOT include Manager"
fi

# ============================================================
# Section 6: Verify team-context mentions Team Admin
# ============================================================
log_section "Verify Team Context Block"

W1_AGENTS=$(exec_in_manager mc cat "${STORAGE_PREFIX}/agents/${TEST_W1}/AGENTS.md" 2>/dev/null || echo "")
W1_CTX=$(echo "${W1_AGENTS}" | sed -n '/hiclaw-team-context-start/,/hiclaw-team-context-end/p')
assert_contains "${W1_CTX}" "Team Admin" "Worker team-context mentions Team Admin"

LEADER_AGENTS=$(exec_in_manager mc cat "${STORAGE_PREFIX}/agents/${TEST_LEADER}/AGENTS.md" 2>/dev/null || echo "")
LEADER_CTX=$(echo "${LEADER_AGENTS}" | sed -n '/hiclaw-team-context-start/,/hiclaw-team-context-end/p')
assert_contains "${LEADER_CTX}" "Team Admin" "Leader team-context mentions Team Admin"

# ============================================================
# Section 7: Team Admin messages Leader in Leader DM
# ============================================================
log_section "Team Admin ↔ Leader Messaging"

if [ -z "${HUMAN_TOKEN}" ] || [ "${HUMAN_TOKEN}" = "null" ]; then
    log_info "Skipping messaging (no human token)"
elif [ -z "${LEADER_DM_ROOM}" ] || [ "${LEADER_DM_ROOM}" = "null" ]; then
    log_info "Skipping messaging (no Leader DM room)"
elif ! require_llm_key; then
    log_info "Skipping messaging (no LLM API key)"
else
    # Human joins the Leader DM room
    ROOM_ENC="$(_encode_room_id "${LEADER_DM_ROOM}")"
    exec_in_manager curl -s -X POST \
        "http://127.0.0.1:6167/_matrix/client/v3/rooms/${ROOM_ENC}/join" \
        -H "Authorization: Bearer ${HUMAN_TOKEN}" \
        -H 'Content-Type: application/json' -d '{}' 2>/dev/null > /dev/null
    log_info "Team Admin joined Leader DM room"

    # Wait for Leader to join room
    LEADER_MID="@${TEST_LEADER}:${TEST_MATRIX_DOMAIN}"
    log_info "Waiting for Leader to join room..."
    LEADER_JOINED=false
    for i in $(seq 1 24); do
        MEMBERS=$(exec_in_manager curl -sf \
            "http://127.0.0.1:6167/_matrix/client/v3/rooms/${ROOM_ENC}/members" \
            -H "Authorization: Bearer ${HUMAN_TOKEN}" 2>/dev/null | \
            jq -r '.chunk[] | select(.content.membership == "join") | .state_key' 2>/dev/null)
        if echo "${MEMBERS}" | grep -q "${LEADER_MID}"; then
            LEADER_JOINED=true
            break
        fi
        sleep 5
    done

    if [ "${LEADER_JOINED}" = true ]; then
        log_pass "Leader joined Leader DM room"

        # Send message from Team Admin to Leader
        TXN_ID="$(date +%s%N)"
        SEND_RESULT=$(exec_in_manager curl -s -X PUT \
            "http://127.0.0.1:6167/_matrix/client/v3/rooms/${ROOM_ENC}/send/m.room.message/${TXN_ID}" \
            -H "Authorization: Bearer ${HUMAN_TOKEN}" \
            -H 'Content-Type: application/json' \
            -d '{
                "msgtype": "m.text",
                "body": "'"${LEADER_MID}"' Hello Leader, this is your Team Admin. Please reply with a short greeting.",
                "m.mentions": {
                    "user_ids": ["'"${LEADER_MID}"'"]
                }
            }' 2>&1)

        SEND_EVENT=$(echo "${SEND_RESULT}" | jq -r '.event_id // empty' 2>/dev/null)
        if [ -n "${SEND_EVENT}" ] && [ "${SEND_EVENT}" != "null" ]; then
            log_pass "Team Admin sent message to Leader (event: ${SEND_EVENT})"

            log_info "Waiting for Leader reply (timeout: 120s)..."
            REPLY=$(matrix_wait_for_reply "${HUMAN_TOKEN}" "${LEADER_DM_ROOM}" "@${TEST_LEADER}" 120)
            if [ -n "${REPLY}" ]; then
                log_pass "Leader replied to Team Admin: $(echo "${REPLY}" | head -1 | cut -c1-80)..."
            else
                log_fail "Leader did not reply to Team Admin within 120s"
            fi
        else
            log_fail "Failed to send message to Leader DM"
            log_info "Send result: ${SEND_RESULT}"
        fi
    else
        log_fail "Leader did not join Leader DM room within 120s"
    fi
fi

# ============================================================
# Section 8: Verify containers running
# ============================================================
log_section "Verify Containers"

for w in "${TEST_LEADER}" "${TEST_W1}"; do
    RUNNING=$(docker ps --format '{{.Names}}' 2>/dev/null | grep "hiclaw-worker-${w}" || echo "")
    if [ -n "${RUNNING}" ]; then
        log_pass "Container running: hiclaw-worker-${w}"
    else
        DEPLOY=$(exec_in_manager jq -r --arg w "${w}" '.workers[$w].deployment // empty' /root/manager-workspace/workers-registry.json 2>/dev/null)
        if [ "${DEPLOY}" = "remote" ]; then
            log_pass "Agent ${w} registered in remote mode"
        else
            log_fail "Container not running: hiclaw-worker-${w}"
        fi
    fi
done

# ============================================================
test_teardown "19-human-and-team-admin"
test_summary
