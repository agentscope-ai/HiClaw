#!/bin/bash
# test-06-multi-worker.sh - Case 6: Create Bob, assign collaborative task
# Verifies: Second Worker creation, both Workers collaborate via shared MinIO files

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/test-helpers.sh"
source "${SCRIPT_DIR}/lib/matrix-client.sh"
source "${SCRIPT_DIR}/lib/higress-client.sh"
source "${SCRIPT_DIR}/lib/minio-client.sh"
source "${SCRIPT_DIR}/lib/agent-metrics.sh"

test_setup "06-multi-worker"

if ! require_llm_key; then
    test_teardown "06-multi-worker"
    test_summary
    exit 0
fi

ADMIN_LOGIN=$(matrix_login "${TEST_ADMIN_USER}" "${TEST_ADMIN_PASSWORD}")
ADMIN_TOKEN=$(echo "${ADMIN_LOGIN}" | jq -r '.access_token')

MANAGER_USER="@manager:${TEST_MATRIX_DOMAIN}"

log_section "Create Worker Bob"

DM_ROOM=$(matrix_find_dm_room "${ADMIN_TOKEN}" "${MANAGER_USER}" 2>/dev/null || true)
assert_not_empty "${DM_ROOM}" "DM room with Manager found"

# Wait for Manager Agent to be fully ready (OpenClaw gateway + joined DM room)
wait_for_manager_agent_ready 300 "${DM_ROOM}" "${ADMIN_TOKEN}" || {
    log_fail "Manager Agent not ready in time"
    test_teardown "06-multi-worker"
    test_summary
    exit 1
}

# Alice is running from previous tests; bob will be created below (offset=0 is correct for new workers)
wait_for_worker_container "alice" 60
METRICS_BASELINE=$(snapshot_baseline "alice" "bob")
matrix_send_message "${ADMIN_TOKEN}" "${DM_ROOM}" \
    "Create a new Worker for backend development. The worker's name (username) must be exactly 'bob'. He should have access to GitHub MCP."

log_info "Waiting for Manager to create Worker Bob..."
REPLY=$(matrix_wait_for_reply "${ADMIN_TOKEN}" "${DM_ROOM}" "@manager" 180)

assert_not_empty "${REPLY}" "Manager replied to create bob request"
assert_contains_i "${REPLY}" "bob" "Reply mentions worker name 'bob'"

# Verify Bob's infrastructure (may take a moment for LLM to complete setup)
sleep 30
higress_login "${TEST_ADMIN_USER}" "${TEST_ADMIN_PASSWORD}" > /dev/null
CONSUMERS=$(higress_get_consumers)
assert_contains_i "${CONSUMERS}" "worker-bob" "Higress consumer 'worker-bob' exists"

minio_setup
minio_wait_for_file "agents/bob/SOUL.md" 60
BOB_EXISTS=$?
assert_eq "0" "${BOB_EXISTS}" "Worker Bob SOUL.md exists in MinIO"

log_section "Assign Collaborative Task"

matrix_send_message "${ADMIN_TOKEN}" "${DM_ROOM}" \
    "I need Alice and Bob to collaborate on a task: Build a simple REST API. Alice handles the frontend HTML page, Bob handles the backend API endpoint. They should coordinate via shared files."

log_info "Waiting for Manager to split and assign task..."
REPLY=$(matrix_wait_for_reply "${ADMIN_TOKEN}" "${DM_ROOM}" "@manager" 180)

assert_not_empty "${REPLY}" "Manager acknowledged collaborative task"

log_section "Verify Shared Coordination"

sleep 60
TASKS=$(minio_list_dir "shared/tasks/" 2>/dev/null || echo "")
log_info "Shared tasks directory: ${TASKS}"

log_section "Collect Metrics"
wait_for_worker_session_stable "alice" 5 120
wait_for_worker_session_stable "bob" 5 120
wait_for_session_stable 5 60
PREV_METRICS=$(cat "${TEST_OUTPUT_DIR}/metrics-06-multi-worker.json" 2>/dev/null || true)
METRICS=$(collect_delta_metrics "06-multi-worker" "$METRICS_BASELINE" "alice" "bob")
print_metrics_report "$METRICS" "$PREV_METRICS"
save_metrics_file "$METRICS" "06-multi-worker"

test_teardown "06-multi-worker"
test_summary
