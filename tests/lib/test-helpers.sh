#!/bin/bash
# test-helpers.sh - Common test utilities: assertions, lifecycle, logging
# Source this file in each test script.

# NOTE: Do NOT use "set -e" here. Tests use assertions (log_pass/log_fail)
# for results, not exit codes. set -e would abort the test script on the
# first failing curl or command, hiding remaining test results.

# ============================================================
# Configuration
# ============================================================

# Auto-detect Manager container name if not set
if [ -z "${TEST_MANAGER_CONTAINER}" ]; then
    export TEST_MANAGER_CONTAINER="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -E '^hiclaw-manager' | head -1)"
    export TEST_MANAGER_CONTAINER="${TEST_MANAGER_CONTAINER:-hiclaw-manager}"
fi

# Host where the Manager container's exposed ports are reachable
export TEST_MANAGER_HOST="127.0.0.1"

# External host ports — auto-detected from container env in detect_manager_config()
export TEST_GATEWAY_PORT="${TEST_GATEWAY_PORT:-18080}"
export TEST_CONSOLE_PORT="${TEST_CONSOLE_PORT:-18001}"
export TEST_ELEMENT_PORT="${TEST_ELEMENT_PORT:-18088}"

# Internal container URLs — always fixed; all callers use exec_in_manager
export TEST_MATRIX_DIRECT_URL="http://127.0.0.1:6167"
export TEST_MINIO_URL="http://127.0.0.1:9000"

# Derived external URLs — rebuilt by detect_manager_config() after port detection
export TEST_CONSOLE_URL="http://${TEST_MANAGER_HOST}:${TEST_CONSOLE_PORT}"

# Matrix domain — auto-detected from container env in detect_manager_config()
export TEST_MATRIX_DOMAIN="${TEST_MATRIX_DOMAIN:-}"

# Test state
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_TOTAL=0
TEST_FAILURES=()

# ============================================================
# Logging
# ============================================================

log_info() {
    echo -e "\033[36m[TEST INFO]\033[0m $1" >&2
}

log_pass() {
    echo -e "\033[32m[TEST PASS]\033[0m $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
    TESTS_TOTAL=$((TESTS_TOTAL + 1))
}

log_fail() {
    echo -e "\033[31m[TEST FAIL]\033[0m $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
    TESTS_TOTAL=$((TESTS_TOTAL + 1))
    TEST_FAILURES+=("$1")
}

log_section() {
    echo ""
    echo -e "\033[35m=== $1 ===\033[0m"
}

# ============================================================
# Assertions
# ============================================================

assert_eq() {
    local expected="$1"
    local actual="$2"
    local message="${3:-assert_eq}"

    if [ "${expected}" = "${actual}" ]; then
        log_pass "${message}"
    else
        log_fail "${message} (expected: '${expected}', got: '${actual}')"
    fi
}

assert_contains() {
    local haystack="$1"
    local needle="$2"
    local message="${3:-assert_contains}"

    if echo "${haystack}" | grep -q "${needle}"; then
        log_pass "${message}"
    else
        log_fail "${message} (expected to contain: '${needle}')"
    fi
}

assert_contains_i() {
    local haystack="$1"
    local needle="$2"
    local message="${3:-assert_contains_i}"

    if echo "${haystack}" | grep -qi "${needle}"; then
        log_pass "${message}"
    else
        log_fail "${message} (expected to contain (case-insensitive): '${needle}')"
    fi
}

assert_not_empty() {
    local value="$1"
    local message="${2:-assert_not_empty}"

    if [ -n "${value}" ] && [ "${value}" != "null" ]; then
        log_pass "${message}"
    else
        log_fail "${message} (value is empty or null)"
    fi
}

assert_http_code() {
    local url="$1"
    local expected_code="$2"
    local message="${3:-assert_http_code}"
    local extra_args="${4:-}"

    local actual_code
    # Use -s (silent) without -f (fail) so curl always outputs the HTTP code.
    # With -f, curl exits non-zero on 4xx/5xx, and || echo "000" would concatenate.
    actual_code=$(curl -s -o /dev/null -w '%{http_code}' ${extra_args} "${url}" 2>/dev/null)

    assert_eq "${expected_code}" "${actual_code}" "${message}"
}

# ============================================================
# Wait / Poll utilities
# ============================================================

# Wait until a condition function returns 0, or timeout
# Usage: wait_until "description" timeout_seconds check_function [args...]
wait_until() {
    local description="$1"
    local timeout="$2"
    shift 2
    local check_fn="$@"

    local elapsed=0
    log_info "Waiting for: ${description} (timeout: ${timeout}s)"

    while ! eval "${check_fn}" 2>/dev/null; do
        sleep 5
        elapsed=$((elapsed + 5))
        if [ "${elapsed}" -ge "${timeout}" ]; then
            log_fail "Timeout waiting for: ${description}"
            return 1
        fi
    done

    log_info "${description} ready (took ${elapsed}s)"
    return 0
}

# Wait for Manager container to be healthy
wait_for_manager() {
    local timeout="${1:-300}"
    wait_until "Manager container healthy" "${timeout}" \
        "curl -sf http://${TEST_MANAGER_HOST}:${TEST_GATEWAY_PORT}/ > /dev/null 2>&1"
}

# Wait for Manager Agent (OpenClaw) to be fully ready
# Phase 1: OpenClaw gateway health check (inside container)
# Phase 2: Manager has joined the specified DM room
# Usage: wait_for_manager_agent_ready [timeout] [room_id] [access_token]
wait_for_manager_agent_ready() {
    local timeout="${1:-300}"
    local room_id="${2:-}"
    local access_token="${3:-}"
    local manager_container="${TEST_MANAGER_CONTAINER:-hiclaw-manager-test}"
    local manager_user="manager"
    local matrix_domain="${TEST_MATRIX_DOMAIN:-matrix-local.hiclaw.io:${TEST_GATEWAY_PORT}}"

    local elapsed=0

    # Phase 1: Wait for OpenClaw gateway to be healthy
    log_info "Waiting for Manager OpenClaw gateway to be healthy..."
    local gateway_ready=false
    while [ "${elapsed}" -lt "${timeout}" ]; do
        if docker exec "${manager_container}" openclaw gateway health --json 2>/dev/null | grep -q '"ok"'; then
            gateway_ready=true
            log_info "OpenClaw gateway is healthy (took ${elapsed}s)"
            break
        fi
        sleep 5
        elapsed=$((elapsed + 5))
        printf "\r\033[36m[TEST INFO]\033[0m Waiting for OpenClaw gateway... (%ds/%ds)" "${elapsed}" "${timeout}"
    done

    if [ "${gateway_ready}" != "true" ]; then
        log_fail "OpenClaw gateway did not become healthy within ${timeout}s"
        return 1
    fi

    # Phase 2: Wait for Manager to join the DM room (if room_id and token provided)
    if [ -n "${room_id}" ] && [ -n "${access_token}" ]; then
        log_info "Waiting for Manager to join DM room..."
        local manager_full_id="@${manager_user}:${matrix_domain}"
        local manager_joined=false

        local room_enc="${room_id//!/%21}"
        while [ "${elapsed}" -lt "${timeout}" ]; do
            local members
            members=$(docker exec "${manager_container}" curl -sf -X GET \
                -H "Authorization: Bearer ${access_token}" \
                "http://127.0.0.1:6167/_matrix/client/v3/rooms/${room_enc}/members" 2>/dev/null | \
                jq -r '.chunk[].state_key' 2>/dev/null) || true

            if echo "${members}" | grep -q "${manager_full_id}"; then
                manager_joined=true
                log_info "Manager has joined the DM room"
                break
            fi
            sleep 3
            elapsed=$((elapsed + 3))
            printf "\r\033[36m[TEST INFO]\033[0m Waiting for Manager to join room... (%ds/%ds)" "${elapsed}" "${timeout}"
        done

        if [ "${manager_joined}" != "true" ]; then
            log_fail "Manager did not join the DM room within ${timeout}s"
            return 1
        fi
    fi

    log_info "Manager Agent is fully ready"
    return 0
}

# Wait for a Worker container to be running (started by Manager on demand)
# Usage: wait_for_worker_container <worker_name> [timeout_seconds]
# Returns 0 when container is running, 1 on timeout
wait_for_worker_container() {
    local worker="$1"
    local timeout="${2:-120}"
    local container="hiclaw-worker-${worker}"
    local elapsed=0

    log_info "Waiting for Worker container '${container}' to be running (timeout: ${timeout}s)..."
    while [ "${elapsed}" -lt "${timeout}" ]; do
        if docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^${container}$"; then
            log_info "Worker container '${container}' is running (took ${elapsed}s)"
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done

    log_info "Worker container '${container}' did not start within ${timeout}s" >&2
    return 1
}

# ============================================================
# Config Detection
# ============================================================

# Auto-detect configuration from Manager container
# This reads HICLAW_* environment variables from the container and sets
# TEST_* variables accordingly. Call this after the container is running.
detect_manager_config() {
    local container="${TEST_MANAGER_CONTAINER:-hiclaw-manager}"
    
    # Skip if container is not running
    if ! docker ps --format '{{.Names}}' | grep -q "^${container}$"; then
        return 0
    fi
    
    # Read all config and credentials from container environment in one call
    local container_env
    container_env=$(docker exec "${container}" printenv 2>/dev/null) || true

    _cenv() { echo "${container_env}" | grep "^${1}=" | cut -d= -f2-; }

    local detected_domain detected_gateway_port detected_console_port detected_element_port
    detected_domain=$(        _cenv HICLAW_MATRIX_DOMAIN)
    detected_gateway_port=$(  _cenv HICLAW_PORT_GATEWAY)
    detected_console_port=$(  _cenv HICLAW_PORT_CONSOLE)
    detected_element_port=$(  _cenv HICLAW_PORT_ELEMENT_WEB)

    [ -n "${detected_gateway_port}" ] && export TEST_GATEWAY_PORT="${detected_gateway_port}"
    [ -n "${detected_console_port}" ] && export TEST_CONSOLE_PORT="${detected_console_port}"
    [ -n "${detected_element_port}" ] && export TEST_ELEMENT_PORT="${detected_element_port}"

    # Rebuild derived URLs after port detection
    export TEST_CONSOLE_URL="http://${TEST_MANAGER_HOST}:${TEST_CONSOLE_PORT}"

    if [ -n "${detected_domain}" ] && [ -z "${TEST_MATRIX_DOMAIN}" ]; then
        export TEST_MATRIX_DOMAIN="${detected_domain}"
    elif [ -z "${TEST_MATRIX_DOMAIN}" ]; then
        export TEST_MATRIX_DOMAIN="matrix-local.hiclaw.io:${TEST_GATEWAY_PORT}"
    fi

    # Load credentials from container env (only if not already set externally)
    [ -z "${TEST_ADMIN_USER}" ]          && export TEST_ADMIN_USER="$(           _cenv HICLAW_ADMIN_USER)"
    [ -z "${TEST_ADMIN_PASSWORD}" ]      && export TEST_ADMIN_PASSWORD="$(        _cenv HICLAW_ADMIN_PASSWORD)"
    [ -z "${TEST_MINIO_USER}" ]          && export TEST_MINIO_USER="$(            _cenv HICLAW_MINIO_USER)"
    [ -z "${TEST_MINIO_PASSWORD}" ]      && export TEST_MINIO_PASSWORD="$(        _cenv HICLAW_MINIO_PASSWORD)"
    [ -z "${TEST_REGISTRATION_TOKEN}" ]  && export TEST_REGISTRATION_TOKEN="$(    _cenv HICLAW_REGISTRATION_TOKEN)"
    [ -z "${HICLAW_LLM_API_KEY}" ]       && export HICLAW_LLM_API_KEY="$(         _cenv HICLAW_LLM_API_KEY)"
    [ -z "${TEST_MANAGER_GATEWAY_KEY}" ] && export TEST_MANAGER_GATEWAY_KEY="$(   _cenv HICLAW_MANAGER_GATEWAY_KEY)"
}

# ============================================================
# Test Lifecycle
# ============================================================

test_setup() {
    local test_name="$1"
    log_section "Starting: ${test_name}"
    
    # Auto-detect configuration from Manager container
    detect_manager_config
}

test_teardown() {
    local test_name="$1"
    log_section "Finished: ${test_name}"
}

# Print summary and exit with appropriate code
test_summary() {
    echo ""
    echo "========================================"
    echo "  Test Summary"
    echo "========================================"
    echo "  Total:  ${TESTS_TOTAL}"
    echo -e "  \033[32mPassed: ${TESTS_PASSED}\033[0m"
    echo -e "  \033[31mFailed: ${TESTS_FAILED}\033[0m"
    echo "========================================"

    if [ ${TESTS_FAILED} -gt 0 ]; then
        echo ""
        echo "Failures:"
        for failure in "${TEST_FAILURES[@]}"; do
            echo "  - ${failure}"
        done
        echo ""
        return 1
    fi

    return 0
}

# ============================================================
# LLM / Agent helpers
# ============================================================

# Check if LLM API key is configured (required for tests that need Manager Agent responses)
require_llm_key() {
    if [ -z "${HICLAW_LLM_API_KEY}" ]; then
        log_info "SKIP: No LLM API key configured (set HICLAW_LLM_API_KEY). This test requires Manager Agent LLM responses."
        return 1
    fi
    return 0
}

# ============================================================
# Docker helpers
# ============================================================

# Run a command inside the Manager container.
# Used by matrix-client.sh and minio-client.sh to avoid exposing Matrix/MinIO ports to host.
exec_in_manager() {
    docker exec "${TEST_MANAGER_CONTAINER:-hiclaw-manager}" "$@"
}

start_worker_container() {
    local worker_name="$1"
    local container_name="hiclaw-test-worker-${worker_name}"

    docker run -d \
        --name "${container_name}" \
        --network host \
        -e "HICLAW_WORKER_NAME=${worker_name}" \
        -e "HICLAW_MATRIX_SERVER=http://${TEST_MANAGER_HOST}:${TEST_GATEWAY_PORT}" \
        -e "HICLAW_AI_GATEWAY=http://${TEST_MANAGER_HOST}:${TEST_GATEWAY_PORT}" \
        -e "HICLAW_FS_ENDPOINT=http://${TEST_MANAGER_HOST}:9000" \
        -e "HICLAW_FS_ACCESS_KEY=${TEST_MINIO_USER}" \
        -e "HICLAW_FS_SECRET_KEY=${TEST_MINIO_PASSWORD}" \
        "hiclaw/worker-agent:${HICLAW_VERSION:-latest}" 2>/dev/null

    echo "${container_name}"
}

stop_worker_container() {
    local container_name="$1"
    docker stop "${container_name}" 2>/dev/null || true
    docker rm "${container_name}" 2>/dev/null || true
}
