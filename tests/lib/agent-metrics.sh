#!/bin/bash
# agent-metrics.sh - Agent session metrics extraction and analysis
#
# Parses OpenClaw session .jsonl files to extract LLM call metrics:
# - LLM call count per agent
# - Token usage (input/output/cache)
# - Timing information
#
# Usage:
#   source lib/agent-metrics.sh
#   metrics=$(collect_test_metrics "test-name" "worker1" "worker2")
#   print_metrics_report "$metrics"

# Source dependencies
_AGENT_METRICS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${_AGENT_METRICS_DIR}/test-helpers.sh" 2>/dev/null || true

# ============================================================
# Configuration
# ============================================================

# Default thresholds (can be overridden via environment)
# These are safety limits; actual values should be much lower
export METRICS_THRESHOLD_MANAGER_LLM_CALLS="${METRICS_THRESHOLD_MANAGER_LLM_CALLS:-20}"
export METRICS_THRESHOLD_MANAGER_TOKENS_INPUT="${METRICS_THRESHOLD_MANAGER_TOKENS_INPUT:-200000}"
export METRICS_THRESHOLD_MANAGER_TOKENS_OUTPUT="${METRICS_THRESHOLD_MANAGER_TOKENS_OUTPUT:-50000}"

export METRICS_THRESHOLD_WORKER_LLM_CALLS="${METRICS_THRESHOLD_WORKER_LLM_CALLS:-10}"
export METRICS_THRESHOLD_WORKER_TOKENS_INPUT="${METRICS_THRESHOLD_WORKER_TOKENS_INPUT:-100000}"
export METRICS_THRESHOLD_WORKER_TOKENS_OUTPUT="${METRICS_THRESHOLD_WORKER_TOKENS_OUTPUT:-30000}"

# Output directory for metrics files
export TEST_OUTPUT_DIR="${TEST_OUTPUT_DIR:-${PROJECT_ROOT:-.}/tests/output}"

# ============================================================
# Session JSONL Parsing
# ============================================================

# Parse session jsonl content from stdin and output metrics JSON
# Input: jsonl lines via stdin
# Output: {"llm_calls": N, "tokens": {...}, "timing": {...}}
parse_session_metrics_inline() {
    local llm_calls=0
    local total_input=0
    local total_output=0
    local total_cache_read=0
    local total_cache_write=0
    local start_ts=""
    local end_ts=""
    
    while IFS= read -r line; do
        # Skip empty lines
        [ -z "$line" ] && continue
        
        # Parse message type
        local type
        type=$(echo "$line" | jq -r '.type // empty' 2>/dev/null)
        [ "$type" != "message" ] && continue
        
        # Check for assistant message with usage
        local role
        role=$(echo "$line" | jq -r '.message.role // empty' 2>/dev/null)
        [ "$role" != "assistant" ] && continue
        
        # Extract usage if present
        local usage
        usage=$(echo "$line" | jq -c '.message.usage // empty' 2>/dev/null)
        [ -z "$usage" ] || [ "$usage" = "null" ] || [ "$usage" = "" ] && continue
        
        # Count this LLM call
        llm_calls=$((llm_calls + 1))
        
        # Accumulate token counts
        local input output cache_read cache_write
        input=$(echo "$usage" | jq -r '.input // 0' 2>/dev/null)
        output=$(echo "$usage" | jq -r '.output // 0' 2>/dev/null)
        cache_read=$(echo "$usage" | jq -r '.cacheRead // 0' 2>/dev/null)
        cache_write=$(echo "$usage" | jq -r '.cacheWrite // 0' 2>/dev/null)
        
        total_input=$((total_input + input))
        total_output=$((total_output + output))
        total_cache_read=$((total_cache_read + cache_read))
        total_cache_write=$((total_cache_write + cache_write))
        
        # Track timing
        local ts
        ts=$(echo "$line" | jq -r '.timestamp // empty' 2>/dev/null)
        if [ -n "$ts" ]; then
            if [ -z "$start_ts" ] || [[ "$ts" < "$start_ts" ]]; then
                start_ts="$ts"
            fi
            if [ -z "$end_ts" ] || [[ "$ts" > "$end_ts" ]]; then
                end_ts="$ts"
            fi
        fi
    done
    
    local total_tokens=$((total_input + total_output))
    
    cat <<EOF
{
  "llm_calls": ${llm_calls},
  "tokens": {
    "input": ${total_input},
    "output": ${total_output},
    "cache_read": ${total_cache_read},
    "cache_write": ${total_cache_write},
    "total": ${total_tokens}
  },
  "timing": {
    "start": "${start_ts}",
    "end": "${end_ts}",
    "duration_seconds": $(calculate_duration_seconds "$start_ts" "$end_ts")
  }
}
EOF
}

# Calculate duration in seconds between two ISO timestamps
calculate_duration_seconds() {
    local start_ts="$1"
    local end_ts="$2"
    
    if [ -z "$start_ts" ] || [ -z "$end_ts" ]; then
        echo "0"
        return
    fi
    
    # Convert ISO timestamps to Unix epoch seconds
    local start_epoch end_epoch
    start_epoch=$(date -d "${start_ts}" +%s 2>/dev/null) || { echo "0"; return; }
    end_epoch=$(date -d "${end_ts}" +%s 2>/dev/null) || { echo "0"; return; }
    
    echo $((end_epoch - start_epoch))
}

# ============================================================
# Multi-Agent Metrics Collection
# ============================================================

# Get the latest session file for an agent
# Usage: get_latest_session <container> <session_dir>
get_latest_session() {
    local container="$1"
    local session_dir="$2"
    
    docker exec "$container" sh -c "ls -t '${session_dir}'/*.jsonl 2>/dev/null | head -1" 2>/dev/null
}

# ============================================================
# Baseline & Delta Metrics (for per-test metrics)
# ============================================================

# Snapshot current session metrics as baseline for later delta calculation
# Usage: METRICS_BASELINE=$(snapshot_baseline "worker1" "worker2" ...)
# Returns: JSON with current cumulative metrics for all agents
snapshot_baseline() {
    local workers=("$@")
    
    local manager_container="${TEST_MANAGER_CONTAINER:-hiclaw-manager}"
    local manager_session_dir="/root/manager-workspace/.openclaw/agents/main/sessions"
    
    local snapshot_result='{"agents": {}}'
    
    # Collect Manager baseline
    local manager_session
    manager_session=$(get_latest_session "$manager_container" "$manager_session_dir")
    
    if [ -n "$manager_session" ]; then
        local manager_metrics
        manager_metrics=$(docker exec "$manager_container" cat "$manager_session" 2>/dev/null | parse_session_metrics_inline)
        if [ -n "$manager_metrics" ]; then
            snapshot_result=$(echo "$snapshot_result" | jq --argjson m "$manager_metrics" '.agents.manager = $m')
        fi
    fi
    
    # Collect Worker baselines
    for worker in "${workers[@]}"; do
        local worker_container="hiclaw-worker-${worker}"
        local worker_session_dir="/root/hiclaw-fs/agents/${worker}/.openclaw/agents/main/sessions"
        
        if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^${worker_container}$"; then
            continue
        fi
        
        local worker_session
        worker_session=$(get_latest_session "$worker_container" "$worker_session_dir")
        
        if [ -n "$worker_session" ]; then
            local worker_metrics
            worker_metrics=$(docker exec "$worker_container" cat "$worker_session" 2>/dev/null | parse_session_metrics_inline)
            if [ -n "$worker_metrics" ]; then
                snapshot_result=$(echo "$snapshot_result" | jq --arg w "$worker" --argjson m "$worker_metrics" '.agents[$w] = $m')
            fi
        fi
    done
    
    echo "$snapshot_result"
}

# Collect delta metrics (difference from baseline) - metrics consumed during THIS test only
# Usage: METRICS=$(collect_delta_metrics <test_name> "$METRICS_BASELINE" "worker1" "worker2" ...)
collect_delta_metrics() {
    local test_name="$1"
    local baseline="$2"
    shift 2
    local workers=("$@")
    
    local manager_container="${TEST_MANAGER_CONTAINER:-hiclaw-manager}"
    local manager_session_dir="/root/manager-workspace/.openclaw/agents/main/sessions"
    
    # Initialize result structure
    local delta_result='{"test_name": "'"${test_name}"'", "timestamp": "'"$(date -Iseconds)"'", "agents": {}, "totals": {"llm_calls": 0, "tokens": {"input": 0, "output": 0, "cache_read": 0, "cache_write": 0, "total": 0}, "timing": {"duration_seconds": 0}}}'
    
    # Collect Manager delta
    log_info "Collecting Manager delta metrics..." >&2
    local manager_session
    manager_session=$(get_latest_session "$manager_container" "$manager_session_dir")
    
    if [ -n "$manager_session" ]; then
        local current_manager
        current_manager=$(docker exec "$manager_container" cat "$manager_session" 2>/dev/null | parse_session_metrics_inline)
        if [ -n "$current_manager" ]; then
            local baseline_manager=$(echo "$baseline" | jq -r '.agents.manager // empty')
            local manager_delta
            
            if [ -n "$baseline_manager" ] && [ "$baseline_manager" != "null" ] && [ "$baseline_manager" != "" ]; then
                # Calculate delta
                manager_delta=$(echo "$current_manager" | jq --argjson base "$baseline_manager" '
                    {
                        llm_calls: (.llm_calls - $base.llm_calls),
                        tokens: {
                            input: (.tokens.input - $base.tokens.input),
                            output: (.tokens.output - $base.tokens.output),
                            cache_read: (.tokens.cache_read - $base.tokens.cache_read),
                            cache_write: (.tokens.cache_write - $base.tokens.cache_write),
                            total: ((.tokens.input - $base.tokens.input) + (.tokens.output - $base.tokens.output))
                        },
                        timing: .timing
                    }
                ')
            else
                # No baseline, use current as-is
                manager_delta="$current_manager"
            fi
            
            delta_result=$(echo "$delta_result" | jq --argjson m "$manager_delta" '.agents.manager = $m')
            log_info "Manager delta: $(echo "$manager_delta" | jq -r '.llm_calls') LLM calls, $(echo "$manager_delta" | jq -r '.tokens.total') tokens" >&2
        fi
    fi
    
    # Collect Worker deltas
    for worker in "${workers[@]}"; do
        local worker_container="hiclaw-worker-${worker}"
        local worker_session_dir="/root/hiclaw-fs/agents/${worker}/.openclaw/agents/main/sessions"
        
        log_info "Collecting Worker '${worker}' delta metrics..." >&2
        
        if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^${worker_container}$"; then
            log_info "Worker '${worker}' container not running, skipping" >&2
            continue
        fi
        
        local worker_session
        worker_session=$(get_latest_session "$worker_container" "$worker_session_dir")
        
        if [ -n "$worker_session" ]; then
            local current_worker
            current_worker=$(docker exec "$worker_container" cat "$worker_session" 2>/dev/null | parse_session_metrics_inline)
            if [ -n "$current_worker" ]; then
                local baseline_worker=$(echo "$baseline" | jq -r --arg w "$worker" '.agents[$w] // empty')
                local worker_delta
                
                if [ -n "$baseline_worker" ] && [ "$baseline_worker" != "null" ] && [ "$baseline_worker" != "" ]; then
                    # Calculate delta
                    worker_delta=$(echo "$current_worker" | jq --argjson base "$baseline_worker" '
                        {
                            llm_calls: (.llm_calls - $base.llm_calls),
                            tokens: {
                                input: (.tokens.input - $base.tokens.input),
                                output: (.tokens.output - $base.tokens.output),
                                cache_read: (.tokens.cache_read - $base.tokens.cache_read),
                                cache_write: (.tokens.cache_write - $base.tokens.cache_write),
                                total: ((.tokens.input - $base.tokens.input) + (.tokens.output - $base.tokens.output))
                            },
                            timing: .timing
                        }
                    ')
                else
                    # No baseline, use current as-is
                    worker_delta="$current_worker"
                fi
                
                delta_result=$(echo "$delta_result" | jq --arg w "$worker" --argjson m "$worker_delta" '.agents[$w] = $m')
                log_info "Worker '${worker}' delta: $(echo "$worker_delta" | jq -r '.llm_calls') LLM calls, $(echo "$worker_delta" | jq -r '.tokens.total') tokens" >&2
            fi
        else
            log_info "No session found for Worker '${worker}'" >&2
        fi
    done
    
    # Calculate totals
    delta_result=$(echo "$delta_result" | jq '
        .totals.llm_calls = ([.agents[].llm_calls] | add // 0)
        | .totals.tokens.input = ([.agents[].tokens.input] | add // 0)
        | .totals.tokens.output = ([.agents[].tokens.output] | add // 0)
        | .totals.tokens.cache_read = ([.agents[].tokens.cache_read] | add // 0)
        | .totals.tokens.cache_write = ([.agents[].tokens.cache_write] | add // 0)
        | .totals.tokens.total = (.totals.tokens.input + .totals.tokens.output)
        | .totals.timing.duration_seconds = ([.agents[].timing.duration_seconds] | add // 0)
    ')
    
    echo "$delta_result"
}

# ============================================================
# Multi-Agent Metrics Collection (Cumulative)
# ============================================================

# Collect metrics from Manager and specified workers
# Usage: collect_test_metrics <test_name> [worker_names...]
# Output: JSON with all agent metrics and totals
collect_test_metrics() {
    local test_name="$1"
    shift
    local workers=("$@")
    
    local manager_container="${TEST_MANAGER_CONTAINER:-hiclaw-manager}"
    local manager_session_dir="/root/manager-workspace/.openclaw/agents/main/sessions"
    
    # Initialize result structure
    local cumulative_result='{"test_name": "'"${test_name}"'", "timestamp": "'"$(date -Iseconds)"'", "agents": {}, "totals": {"llm_calls": 0, "tokens": {"input": 0, "output": 0, "cache_read": 0, "cache_write": 0, "total": 0}, "timing": {"duration_seconds": 0}}}'
    
    # Collect Manager metrics
    log_info "Collecting Manager metrics..." >&2
    local manager_session
    manager_session=$(get_latest_session "$manager_container" "$manager_session_dir")
    
    if [ -n "$manager_session" ]; then
        local manager_metrics
        manager_metrics=$(docker exec "$manager_container" cat "$manager_session" 2>/dev/null | parse_session_metrics_inline)
        if [ -n "$manager_metrics" ]; then
            cumulative_result=$(echo "$cumulative_result" | jq --argjson m "$manager_metrics" '.agents.manager = $m')
            log_info "Manager: $(echo "$manager_metrics" | jq -r '.llm_calls') LLM calls, $(echo "$manager_metrics" | jq -r '.tokens.total') tokens" >&2
        fi
    else
        log_info "No Manager session found" >&2
    fi
    
    # Collect Worker metrics
    for worker in "${workers[@]}"; do
        local worker_container="hiclaw-worker-${worker}"
        local worker_session_dir="/root/hiclaw-fs/agents/${worker}/.openclaw/agents/main/sessions"
        
        log_info "Collecting Worker '${worker}' metrics..." >&2
        
        # Check if worker container exists and is running
        if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^${worker_container}$"; then
            log_info "Worker '${worker}' container not running, skipping" >&2
            continue
        fi
        
        local worker_session
        worker_session=$(get_latest_session "$worker_container" "$worker_session_dir")
        
        if [ -n "$worker_session" ]; then
            local worker_metrics
            worker_metrics=$(docker exec "$worker_container" cat "$worker_session" 2>/dev/null | parse_session_metrics_inline)
            if [ -n "$worker_metrics" ]; then
                cumulative_result=$(echo "$cumulative_result" | jq --arg w "$worker" --argjson m "$worker_metrics" '.agents[$w] = $m')
                log_info "Worker '${worker}': $(echo "$worker_metrics" | jq -r '.llm_calls') LLM calls, $(echo "$worker_metrics" | jq -r '.tokens.total') tokens" >&2
            fi
        else
            log_info "No session found for Worker '${worker}'" >&2
        fi
    done
    
    # Calculate totals
    cumulative_result=$(echo "$cumulative_result" | jq '
        .totals.llm_calls = ([.agents[].llm_calls] | add // 0)
        | .totals.tokens.input = ([.agents[].tokens.input] | add // 0)
        | .totals.tokens.output = ([.agents[].tokens.output] | add // 0)
        | .totals.tokens.cache_read = ([.agents[].tokens.cache_read] | add // 0)
        | .totals.tokens.cache_write = ([.agents[].tokens.cache_write] | add // 0)
        | .totals.tokens.total = (.totals.tokens.input + .totals.tokens.output)
        | .totals.timing.duration_seconds = ([.agents[].timing.duration_seconds] | add // 0)
    ')
    
    echo "$cumulative_result"
}

# ============================================================
# Metrics Reporting
# ============================================================

# Print a formatted metrics report to stdout
# Usage: print_metrics_report <metrics_json>
print_metrics_report() {
    local metrics="$1"
    
    echo ""
    echo "========================================"
    echo "  Agent Metrics Report"
    echo "========================================"
    echo "  Test: $(echo "$metrics" | jq -r '.test_name')"
    echo "  Time: $(echo "$metrics" | jq -r '.timestamp')"
    echo "========================================"
    
    # Print each agent's metrics
    local agent_names
    agent_names=$(echo "$metrics" | jq -r '.agents | keys[]' 2>/dev/null)
    
    for agent in $agent_names; do
        local agent_data
        agent_data=$(echo "$metrics" | jq -c ".agents[\"$agent\"]")
        
        echo ""
        echo "  [$agent]"
        echo "    LLM Calls:    $(echo "$agent_data" | jq -r '.llm_calls')"
        echo "    Input Tokens: $(echo "$agent_data" | jq -r '.tokens.input')"
        echo "    Output Tokens: $(echo "$agent_data" | jq -r '.tokens.output')"
        echo "    Cache Read:   $(echo "$agent_data" | jq -r '.tokens.cache_read')"
        echo "    Cache Write:  $(echo "$agent_data" | jq -r '.tokens.cache_write')"
        echo "    Total Tokens: $(echo "$agent_data" | jq -r '.tokens.total')"
        echo "    Duration:     $(echo "$agent_data" | jq -r '.timing.duration_seconds')s"
        echo "    Start:        $(echo "$agent_data" | jq -r '.timing.start')"
        echo "    End:          $(echo "$agent_data" | jq -r '.timing.end')"
    done
    
    echo ""
    echo "----------------------------------------"
    echo "  TOTALS"
    echo "----------------------------------------"
    echo "    LLM Calls:    $(echo "$metrics" | jq -r '.totals.llm_calls')"
    echo "    Input Tokens: $(echo "$metrics" | jq -r '.totals.tokens.input')"
    echo "    Output Tokens: $(echo "$metrics" | jq -r '.totals.tokens.output')"
    echo "    Cache Read:   $(echo "$metrics" | jq -r '.totals.tokens.cache_read')"
    echo "    Cache Write:  $(echo "$metrics" | jq -r '.totals.tokens.cache_write')"
    echo "    Total Tokens: $(echo "$metrics" | jq -r '.totals.tokens.total')"
    echo "    Duration:     $(echo "$metrics" | jq -r '.totals.timing.duration_seconds')s"
    echo "========================================"
}

# ============================================================
# Metrics Assertions
# ============================================================

# Assert that a metric value is within threshold
# Usage: assert_metrics_threshold <metrics_json> <agent_name> <metric_path> <max_value>
# Example: assert_metrics_threshold "$metrics" "manager" "llm_calls" 10
# Example: assert_metrics_threshold "$metrics" "manager" "tokens.input" 50000
assert_metrics_threshold() {
    local metrics="$1"
    local agent="$2"
    local metric_path="$3"
    local max_value="$4"
    
    # Build jq path for the metric
    local actual
    if [ "$metric_path" = "llm_calls" ]; then
        actual=$(echo "$metrics" | jq -r ".agents[\"${agent}\"].llm_calls // 0")
    else
        actual=$(echo "$metrics" | jq -r ".agents[\"${agent}\"].${metric_path} // 0")
    fi
    
    if [ -z "$actual" ] || [ "$actual" = "null" ]; then
        actual=0
    fi
    
    if [ "$actual" -le "$max_value" ]; then
        log_pass "metrics.${agent}.${metric_path} <= ${max_value} (actual: ${actual})"
        return 0
    else
        log_fail "metrics.${agent}.${metric_path} <= ${max_value} (actual: ${actual}) EXCEEDED!"
        return 1
    fi
}

# Assert all agents are within default thresholds
# Usage: assert_all_thresholds <metrics_json>
assert_all_thresholds() {
    local metrics="$1"
    local failed=0
    
    local agent_names
    agent_names=$(echo "$metrics" | jq -r '.agents | keys[]' 2>/dev/null)
    
    for agent in $agent_names; do
        if [ "$agent" = "manager" ]; then
            assert_metrics_threshold "$metrics" "$agent" "llm_calls" "$METRICS_THRESHOLD_MANAGER_LLM_CALLS" || failed=$((failed + 1))
            assert_metrics_threshold "$metrics" "$agent" "tokens.input" "$METRICS_THRESHOLD_MANAGER_TOKENS_INPUT" || failed=$((failed + 1))
            assert_metrics_threshold "$metrics" "$agent" "tokens.output" "$METRICS_THRESHOLD_MANAGER_TOKENS_OUTPUT" || failed=$((failed + 1))
        else
            assert_metrics_threshold "$metrics" "$agent" "llm_calls" "$METRICS_THRESHOLD_WORKER_LLM_CALLS" || failed=$((failed + 1))
            assert_metrics_threshold "$metrics" "$agent" "tokens.input" "$METRICS_THRESHOLD_WORKER_TOKENS_INPUT" || failed=$((failed + 1))
            assert_metrics_threshold "$metrics" "$agent" "tokens.output" "$METRICS_THRESHOLD_WORKER_TOKENS_OUTPUT" || failed=$((failed + 1))
        fi
    done
    
    return $failed
}

# ============================================================
# Metrics File Operations
# ============================================================

# Save metrics to a JSON file
# Usage: save_metrics_file <metrics_json> <test_name>
save_metrics_file() {
    local metrics="$1"
    local test_name="$2"
    
    mkdir -p "${TEST_OUTPUT_DIR}"
    local output_file="${TEST_OUTPUT_DIR}/metrics-${test_name}.json"
    
    echo "$metrics" > "$output_file"
    log_info "Metrics saved to: ${output_file}" >&2
    
    echo "$output_file"
}

# Load metrics from a JSON file
# Usage: load_metrics_file <test_name>
load_metrics_file() {
    local test_name="$1"
    local input_file="${TEST_OUTPUT_DIR}/metrics-${test_name}.json"
    
    if [ -f "$input_file" ]; then
        cat "$input_file"
    else
        echo '{"error": "file not found", "path": "'"${input_file}"'"}'
        return 1
    fi
}

# Generate a summary JSON combining all test metrics
# Usage: generate_metrics_summary [test_names...]
# Output includes totals and per-test breakdown
generate_metrics_summary() {
    local test_names=("$@")
    local summary='{"tests": [], "totals": {"llm_calls": 0, "tokens": {"input": 0, "output": 0, "cache_read": 0, "cache_write": 0, "total": 0}}}'
    
    for test_name in "${test_names[@]}"; do
        local metrics
        metrics=$(load_metrics_file "$test_name" 2>/dev/null)
        
        if [ $? -eq 0 ] && [ -n "$metrics" ]; then
            # Add to tests array (simplified version with just totals per test)
            local test_summary
            test_summary=$(echo "$metrics" | jq '{
                test_name: .test_name,
                timestamp: .timestamp,
                llm_calls: .totals.llm_calls,
                tokens: .totals.tokens,
                agents: (.agents | keys)
            }')
            
            summary=$(echo "$summary" | jq --argjson t "$test_summary" '.tests += [$t]')
            
            # Accumulate totals
            summary=$(echo "$summary" | jq '
                .totals.llm_calls += (.tests[-1].llm_calls // 0)
                | .totals.tokens.input += (.tests[-1].tokens.input // 0)
                | .totals.tokens.output += (.tests[-1].tokens.output // 0)
                | .totals.tokens.cache_read += (.tests[-1].tokens.cache_read // 0)
                | .totals.tokens.cache_write += (.tests[-1].tokens.cache_write // 0)
                | .totals.tokens.total = (.totals.tokens.input + .totals.tokens.output)
            ')
        fi
    done
    
    echo "$summary"
}

# ============================================================
# Metrics Comparison (Current vs Baseline)
# ============================================================

# Compare current metrics with baseline and generate delta
# Usage: compare_metrics_with_baseline <current_summary_json> <baseline_summary_json>
# Output: JSON with comparison results including deltas and trends
compare_metrics_with_baseline() {
    local current="$1"
    local baseline="$2"
    
    # If no baseline, return current as-is with no comparison
    if [ -z "$baseline" ] || [ "$baseline" = "null" ] || echo "$baseline" | jq -e '.error' >/dev/null 2>&1; then
        echo "$current" | jq '. + {baseline_available: false, totals: {current: .totals}}'
        return 0
    fi
    
    # Calculate deltas for each test
    local comparison
    comparison=$(echo "$current" "$baseline" | jq -s '
        {
            baseline_available: true,
            tests: (
                .[0].tests | map(
                    . as $curr |
                    (.[1].tests | map(select(.test_name == $curr.test_name)) | .[0]) as $base |
                    if $base then
                        {
                            test_name: $curr.test_name,
                            current: $curr,
                            baseline: $base,
                            delta: {
                                llm_calls: ($curr.llm_calls - $base.llm_calls),
                                tokens_input: ($curr.tokens.input - $base.tokens.input),
                                tokens_output: ($curr.tokens.output - $base.tokens.output),
                                tokens_total: (($curr.tokens.input - $base.tokens.input) + ($curr.tokens.output - $base.tokens.output))
                            },
                            trend: (
                                if $curr.llm_calls < $base.llm_calls then "improved"
                                elif $curr.llm_calls > $base.llm_calls then "regressed"
                                else "unchanged"
                                end
                            )
                        }
                    else
                        {
                            test_name: $curr.test_name,
                            current: $curr,
                            baseline: null,
                            delta: null,
                            trend: "new_test"
                        }
                    end
                )
            ),
            totals: {
                current: .[0].totals,
                baseline: .[1].totals,
                delta: {
                    llm_calls: (.[0].totals.llm_calls - .[1].totals.llm_calls),
                    tokens_input: (.[0].totals.tokens.input - .[1].totals.tokens.input),
                    tokens_output: (.[0].totals.tokens.output - .[1].totals.tokens.output),
                    tokens_total: ((.[0].totals.tokens.input - .[1].totals.tokens.input) + (.[0].totals.tokens.output - .[1].totals.tokens.output))
                }
            }
        }
    ')
    
    echo "$comparison"
}

# Format a number with +/- sign for delta display
_format_delta() {
    local value="$1"
    if [ "$value" -gt 0 ]; then
        echo "+${value}"
    elif [ "$value" -lt 0 ]; then
        echo "${value}"
    else
        echo "0"
    fi
}

# Generate a Markdown comparison report for PR comments
# Usage: generate_comparison_markdown <comparison_json>
# Output: Markdown formatted report
generate_comparison_markdown() {
    local comparison="$1"
    local baseline_available
    baseline_available=$(echo "$comparison" | jq -r '.baseline_available // false')
    
    echo "## 📊 CI Metrics Report"
    echo ""
    
    if [ "$baseline_available" = "false" ]; then
        echo "> ℹ️ **No baseline available** - This is the first run or baseline data was not found."
        echo ""
    fi
    
    # Summary totals section
    echo "### Summary"
    echo ""
    
    if [ "$baseline_available" = "true" ]; then
        local curr_calls base_calls delta_calls
        local curr_in base_in delta_in
        local curr_out base_out delta_out
        
        curr_calls=$(echo "$comparison" | jq -r '.totals.current.llm_calls // 0')
        base_calls=$(echo "$comparison" | jq -r '.totals.baseline.llm_calls // 0')
        delta_calls=$(echo "$comparison" | jq -r '.totals.delta.llm_calls // 0')
        
        curr_in=$(echo "$comparison" | jq -r '.totals.current.tokens.input // 0')
        base_in=$(echo "$comparison" | jq -r '.totals.baseline.tokens.input // 0')
        delta_in=$(echo "$comparison" | jq -r '.totals.delta.tokens_input // 0')
        
        curr_out=$(echo "$comparison" | jq -r '.totals.current.tokens.output // 0')
        base_out=$(echo "$comparison" | jq -r '.totals.baseline.tokens.output // 0')
        delta_out=$(echo "$comparison" | jq -r '.totals.delta.tokens_output // 0')
        
        echo "| Metric | Current | Baseline | Delta |"
        echo "|--------|---------|----------|-------|"
        echo "| **LLM Calls** | ${curr_calls} | ${base_calls} | $(_format_delta "$delta_calls") |"
        echo "| **Input Tokens** | ${curr_in} | ${base_in} | $(_format_delta "$delta_in") |"
        echo "| **Output Tokens** | ${curr_out} | ${base_out} | $(_format_delta "$delta_out") |"
    else
        local curr_calls curr_in curr_out
        curr_calls=$(echo "$comparison" | jq -r '.totals.current.llm_calls // .totals.llm_calls // 0')
        curr_in=$(echo "$comparison" | jq -r '.totals.current.tokens.input // .totals.tokens.input // 0')
        curr_out=$(echo "$comparison" | jq -r '.totals.current.tokens.output // .totals.tokens.output // 0')
        
        echo "| Metric | Value |"
        echo "|--------|-------|"
        echo "| **LLM Calls** | ${curr_calls} |"
        echo "| **Input Tokens** | ${curr_in} |"
        echo "| **Output Tokens** | ${curr_out} |"
    fi
    
    echo ""
    
    # Per-test breakdown
    local test_count
    test_count=$(echo "$comparison" | jq '.tests | length // 0')
    
    if [ "$test_count" -gt 0 ]; then
        echo "### Per-Test Breakdown"
        echo ""
        
        if [ "$baseline_available" = "true" ]; then
            echo "| Test | LLM Calls (Δ) | Input Tokens (Δ) | Output Tokens (Δ) | Trend |"
            echo "|------|---------------|-----------------|-------------------|-------|"
            
            echo "$comparison" | jq -r '.tests[] | 
                .test_name as $name |
                .current.llm_calls as $calls |
                (.delta.llm_calls // "N/A") as $calls_delta |
                .current.tokens.input as $in |
                (.delta.tokens_input // "N/A") as $in_delta |
                .current.tokens.output as $out |
                (.delta.tokens_output // "N/A") as $out_delta |
                .trend as $trend |
                "\($name) | \($calls) (\($calls_delta)) | \($in) (\($in_delta)) | \($out) (\($out_delta)) | \($trend)"' | while IFS= read -r line; do
                echo "| $line |"
            done
        else
            echo "| Test | LLM Calls | Input Tokens | Output Tokens |"
            echo "|------|-----------|--------------|---------------|"
            
            echo "$comparison" | jq -r '.tests[] | 
                "\(.test_name) | \(.current.llm_calls // .llm_calls) | \(.current.tokens.input // .tokens.input) | \(.current.tokens.output // .tokens.output)"' | while IFS= read -r line; do
                echo "| $line |"
            done
        fi
        echo ""
    fi
    
    # Trend indicators
    if [ "$baseline_available" = "true" ]; then
        local improved regressed
        improved=$(echo "$comparison" | jq '[.tests[]? | select(.trend == "improved")] | length // 0')
        regressed=$(echo "$comparison" | jq '[.tests[]? | select(.trend == "regressed")] | length // 0')
        
        if [ "$improved" -gt 0 ] || [ "$regressed" -gt 0 ]; then
            echo "### Trends"
            echo ""
            if [ "$improved" -gt 0 ]; then
                echo "✅ **${improved}** test(s) improved (fewer LLM calls)"
            fi
            if [ "$regressed" -gt 0 ]; then
                echo "⚠️ **${regressed}** test(s) regressed (more LLM calls)"
            fi
            echo ""
        fi
    fi
    
    echo "---"
    echo "*Generated by HiClaw CI on $(date -u +"%Y-%m-%d %H:%M:%S UTC")*"
}
