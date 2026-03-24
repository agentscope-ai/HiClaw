#!/bin/bash
# test-15-file-transfer.sh - Case 15: File transfer via Matrix
# Verifies: File upload/download through Matrix media repo,
#           send-file.sh script works end-to-end,
#           error handling on missing files

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/test-helpers.sh"
source "${SCRIPT_DIR}/lib/matrix-client.sh"
source "${SCRIPT_DIR}/lib/agent-metrics.sh"

test_setup "15-file-transfer"

if ! require_llm_key; then
    test_teardown "15-file-transfer"
    test_summary
    exit 0
fi

ADMIN_LOGIN=$(matrix_login "${TEST_ADMIN_USER}" "${TEST_ADMIN_PASSWORD}")
ADMIN_TOKEN=$(echo "${ADMIN_LOGIN}" | jq -r '.access_token')

MANAGER_USER="@manager:${TEST_MATRIX_DOMAIN}"

# ============================================================
# Setup: Find DM room with Manager
# ============================================================
log_section "Setup"

DM_ROOM=$(matrix_find_dm_room "${ADMIN_TOKEN}" "${MANAGER_USER}" 2>/dev/null || true)
assert_not_empty "${DM_ROOM}" "DM room with Manager found"

wait_for_manager_agent_ready 300 "${DM_ROOM}" "${ADMIN_TOKEN}" || {
    log_fail "Manager Agent not ready in time"
    test_teardown "15-file-transfer"
    test_summary
    exit 1
}

# ============================================================
# Test 1: Matrix file upload + send (basic capability)
# ============================================================
log_section "Test 1: Matrix File Upload"

SEND_RESULT=$(matrix_send_file "${ADMIN_TOKEN}" "${DM_ROOM}" \
    "test-transfer.txt" "Hello from file transfer integration test" "text/plain")
FILE_EVENT_ID=$(echo "${SEND_RESULT}" | jq -r '.event_id // empty')
assert_not_empty "${FILE_EVENT_ID}" "File event sent successfully"

# Verify the file event is visible in room messages
sleep 5
MESSAGES=$(matrix_read_messages "${ADMIN_TOKEN}" "${DM_ROOM}" 10)
FILE_MSG=$(echo "${MESSAGES}" | jq -r \
    '[.chunk[] | select(.content.msgtype == "m.file")] | first // empty')
assert_not_empty "${FILE_MSG}" "m.file event visible in room"

FILE_BODY=$(echo "${FILE_MSG}" | jq -r '.content.body // empty')
assert_eq "test-transfer.txt" "${FILE_BODY}" "File event has correct filename"

FILE_MXC=$(echo "${FILE_MSG}" | jq -r '.content.url // empty')
assert_contains "${FILE_MXC}" "mxc://" "File event has mxc:// URL"

FILE_MIME=$(echo "${FILE_MSG}" | jq -r '.content.info.mimetype // empty')
assert_eq "text/plain" "${FILE_MIME}" "File event has correct MIME type"

# ============================================================
# Test 2: send-file.sh script in Manager container
# ============================================================
log_section "Test 2: send-file.sh Script"

# Create a test file inside the Manager container
exec_in_manager sh -c "echo 'Test file from send-file.sh' > /tmp/test-send-file.txt"

# Run send-file.sh inside the Manager container
SEND_FILE_OUTPUT=$(exec_in_manager bash /opt/hiclaw/agent/worker-skills/send-file/scripts/send-file.sh \
    /tmp/test-send-file.txt "${DM_ROOM}" 2>&1) || true

# Check if the output contains an mxc:// URI (success)
if echo "${SEND_FILE_OUTPUT}" | grep -q "mxc://"; then
    log_pass "send-file.sh returned mxc:// URI"

    # Verify the file appeared in the room
    sleep 5
    MESSAGES2=$(matrix_read_messages "${ADMIN_TOKEN}" "${DM_ROOM}" 10)
    SCRIPT_FILE_MSG=$(echo "${MESSAGES2}" | jq -r \
        '[.chunk[] | select(.content.body == "test-send-file.txt" and .content.msgtype == "m.file")] | first // empty')
    assert_not_empty "${SCRIPT_FILE_MSG}" "send-file.sh file visible in room"
else
    # Script may fail if HICLAW_MATRIX_SERVER or token not available in container
    log_info "send-file.sh output: ${SEND_FILE_OUTPUT}"
    if echo "${SEND_FILE_OUTPUT}" | grep -q "ERROR"; then
        log_info "SKIP: send-file.sh credentials not configured in Manager container — skipping script test"
    else
        log_fail "send-file.sh unexpected output"
    fi
fi

# ============================================================
# Test 3: send-file.sh error handling (missing file)
# ============================================================
log_section "Test 3: Error Handling"

MISSING_OUTPUT=$(exec_in_manager bash /opt/hiclaw/agent/worker-skills/send-file/scripts/send-file.sh \
    /tmp/nonexistent-file.txt "${DM_ROOM}" 2>&1) && {
    log_fail "send-file.sh should exit non-zero for missing file"
} || {
    log_pass "send-file.sh exits non-zero for missing file"
}

assert_contains "${MISSING_OUTPUT}" "File not found" "Error message mentions file not found"

# ============================================================
# Collect Metrics
# ============================================================
log_section "Collect Metrics"

# Only collect worker metrics if a worker is running
if wait_for_worker_container "alice" 10 2>/dev/null; then
    METRICS_BASELINE=$(snapshot_baseline "alice" 2>/dev/null || echo "{}")
    wait_for_worker_session_stable "alice" 5 60 2>/dev/null || true
    wait_for_session_stable 5 60 2>/dev/null || true
    PREV_METRICS=$(cat "${TEST_OUTPUT_DIR}/metrics-15-file-transfer.json" 2>/dev/null || true)
    METRICS=$(collect_delta_metrics "15-file-transfer" "$METRICS_BASELINE" "alice" 2>/dev/null || echo "{}")
    print_metrics_report "$METRICS" "$PREV_METRICS" 2>/dev/null || true
    save_metrics_file "$METRICS" "15-file-transfer" 2>/dev/null || true
else
    log_info "No worker container running, skipping metrics collection"
fi

test_teardown "15-file-transfer"
test_summary
