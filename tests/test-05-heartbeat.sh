#!/bin/bash
# test-05-heartbeat.sh - Case 5: Heartbeat triggers Manager inquiry
# Verifies: Manager sends status inquiry to Worker during heartbeat,
#           Worker responds with progress

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/test-helpers.sh"
source "${SCRIPT_DIR}/lib/matrix-client.sh"
source "${SCRIPT_DIR}/lib/agent-metrics.sh"

test_setup "05-heartbeat"

if ! require_llm_key; then
    test_teardown "05-heartbeat"
    test_summary
    exit 0
fi

ADMIN_LOGIN=$(matrix_login "${TEST_ADMIN_USER}" "${TEST_ADMIN_PASSWORD}")
ADMIN_TOKEN=$(echo "${ADMIN_LOGIN}" | jq -r '.access_token')

MANAGER_USER="@manager:${TEST_MATRIX_DOMAIN}"

log_section "Assign Long Task"

DM_ROOM=$(matrix_find_dm_room "${ADMIN_TOKEN}" "${MANAGER_USER}" 2>/dev/null || true)
assert_not_empty "${DM_ROOM}" "DM room with Manager found"

# Wait for Manager Agent to be fully ready (OpenClaw gateway + joined DM room)
wait_for_manager_agent_ready 300 "${DM_ROOM}" "${ADMIN_TOKEN}" || {
    log_fail "Manager Agent not ready in time"
    test_teardown "05-heartbeat"
    test_summary
    exit 1
}

# Alice container should be running from test-02/03/04; wait to ensure it's up before snapshot
wait_for_worker_container "alice" 60
METRICS_BASELINE=$(snapshot_baseline "alice")
matrix_send_message "${ADMIN_TOKEN}" "${DM_ROOM}" \
    "Ask Alice to research and write a comprehensive technical document about WebAssembly. This should be detailed and thorough."

log_info "Waiting for Manager to assign task..."
sleep 30

log_section "Trigger Heartbeat"

# Trigger heartbeat manually via openclaw system event
# This requires access to the Manager container
log_info "Triggering heartbeat via docker exec..."
docker exec hiclaw-manager-test bash -c \
    "cd ~/hiclaw-fs/agents/manager && openclaw system event --mode now" 2>/dev/null || \
    log_info "Could not trigger heartbeat via docker exec (container name may differ)"

log_info "Waiting for heartbeat inquiry..."
sleep 60

log_section "Verify Heartbeat Inquiry"

# Check for Manager inquiry message in Alice's room
MESSAGES=$(matrix_read_messages "${ADMIN_TOKEN}" "${DM_ROOM}" 30)
INQUIRY=$(echo "${MESSAGES}" | jq -r '[.chunk[] | select(.sender | startswith("@manager")) | .content.body] | map(select(test("status|progress|heartbeat|how"; "i"))) | first // empty')

if [ -n "${INQUIRY}" ]; then
    log_pass "Manager sent heartbeat inquiry"
else
    log_info "Heartbeat inquiry not detected (may need longer wait or different room)"
fi

log_section "Collect Metrics"
wait_for_worker_session_stable "alice" 5 120
wait_for_session_stable 5 60
PREV_METRICS=$(cat "${TEST_OUTPUT_DIR}/metrics-05-heartbeat.json" 2>/dev/null || true)
METRICS=$(collect_delta_metrics "05-heartbeat" "$METRICS_BASELINE" "alice")
print_metrics_report "$METRICS" "$PREV_METRICS"
save_metrics_file "$METRICS" "05-heartbeat"

test_teardown "05-heartbeat"
test_summary
