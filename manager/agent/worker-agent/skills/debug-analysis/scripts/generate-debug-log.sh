#!/bin/bash
# generate-debug-log.sh — Aggregate session logs, Matrix messages, and state
# files into a structured Markdown debug report.
#
# Usage:
#   bash ~/skills/debug-analysis/scripts/generate-debug-log.sh --worker alpha-dev --hours 24
#   bash ~/skills/debug-analysis/scripts/generate-debug-log.sh --worker alpha-dev --hours 6 \
#       --include-sessions --include-matrix --include-state
#   bash ~/skills/debug-analysis/scripts/generate-debug-log.sh --worker alpha-dev --output /tmp/report.md
#
# By default all sections are included. Use --include-* flags to select specific sections only.
# Output goes to stdout unless --output is specified.

set -e

WORKSPACE="${HOME}"
DEBUG_CONFIG="${WORKSPACE}/debug-config.json"
DEBUG_TARGETS_DIR="${WORKSPACE}/debug-targets"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ ! -f "${DEBUG_CONFIG}" ]; then
    echo "ERROR: debug-config.json not found at ${DEBUG_CONFIG}" >&2
    echo "This script is only available on DebugWorkers." >&2
    exit 1
fi

# Defaults
WORKER=""
HOURS=24
OUTPUT=""
INC_SESSIONS=""
INC_MATRIX=""
INC_STATE=""
ANY_FLAG=""

while [ $# -gt 0 ]; do
    case "$1" in
        --worker)           WORKER="$2"; shift 2 ;;
        --hours)            HOURS="$2"; shift 2 ;;
        --output)           OUTPUT="$2"; shift 2 ;;
        --include-sessions) INC_SESSIONS=1; ANY_FLAG=1; shift ;;
        --include-matrix)   INC_MATRIX=1; ANY_FLAG=1; shift ;;
        --include-state)    INC_STATE=1; ANY_FLAG=1; shift ;;
        *)
            echo "Unknown option: $1" >&2
            echo "Usage: $0 --worker <name> [--hours N] [--output FILE] [--include-sessions] [--include-matrix] [--include-state]" >&2
            exit 1 ;;
    esac
done

if [ -z "${WORKER}" ]; then
    echo "ERROR: --worker is required" >&2
    exit 1
fi

# Verify worker is a valid target
TARGETS=$(jq -r '.targets[]' "${DEBUG_CONFIG}")
if ! echo "${TARGETS}" | grep -qx "${WORKER}"; then
    echo "ERROR: '${WORKER}' is not in the target list. Available targets:" >&2
    echo "${TARGETS}" | sed 's/^/  - /' >&2
    exit 1
fi

# If no --include-* flags, include everything
if [ -z "${ANY_FLAG}" ]; then
    INC_SESSIONS=1
    INC_MATRIX=1
    INC_STATE=1
fi

TARGET_DIR="${DEBUG_TARGETS_DIR}/${WORKER}"
if [ ! -d "${TARGET_DIR}" ]; then
    echo "WARNING: Target directory ${TARGET_DIR} not found. Run sync-workspace.sh first." >&2
fi

# Calculate cutoff timestamp in seconds since epoch
CUTOFF_EPOCH=$(python3 -c "import time; print(int(time.time() - ${HOURS} * 3600))" 2>/dev/null || echo $(( $(date +%s) - HOURS * 3600 )))

# ---- Report generation ----
generate_report() {
    echo "# Debug Report: ${WORKER}"
    echo ""
    echo "Generated: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
    echo "Time range: last ${HOURS} hours"
    echo ""

    # -- Section: Agent Configuration --
    echo "## 1. Agent Configuration"
    echo ""

    if [ -f "${TARGET_DIR}/SOUL.md" ]; then
        echo "### SOUL.md"
        echo '```'
        head -50 "${TARGET_DIR}/SOUL.md"
        local soul_lines
        soul_lines=$(wc -l < "${TARGET_DIR}/SOUL.md" 2>/dev/null || echo 0)
        if [ "${soul_lines}" -gt 50 ]; then
            echo "... (${soul_lines} lines total, truncated)"
        fi
        echo '```'
        echo ""
    else
        echo "_SOUL.md not found_"
        echo ""
    fi

    if [ -f "${TARGET_DIR}/openclaw.json" ]; then
        echo "### openclaw.json (key fields)"
        echo '```json'
        jq '{model: .model, plugins: (.plugins.load.paths // []), channels: (.channels | keys)}' \
            "${TARGET_DIR}/openclaw.json" 2>/dev/null || cat "${TARGET_DIR}/openclaw.json"
        echo '```'
        echo ""
    fi

    # -- Section: State Files --
    if [ -n "${INC_STATE}" ]; then
        echo "## 2. State Files"
        echo ""

        for state_file in "team-state.json" "state.json"; do
            if [ -f "${TARGET_DIR}/${state_file}" ]; then
                echo "### ${state_file}"
                echo '```json'
                jq '.' "${TARGET_DIR}/${state_file}" 2>/dev/null || cat "${TARGET_DIR}/${state_file}"
                echo '```'
                echo ""
            fi
        done

        # Identity metadata
        if [ -d "${TARGET_DIR}/.openclaw/identity" ]; then
            echo "### Identity"
            echo '```'
            for f in "${TARGET_DIR}/.openclaw/identity/"*; do
                [ -f "$f" ] && echo "$(basename "$f"): $(cat "$f")"
            done
            echo '```'
            echo ""
        fi

        # Memory files
        if [ -d "${TARGET_DIR}/memory" ]; then
            local mem_count
            mem_count=$(find "${TARGET_DIR}/memory" -type f 2>/dev/null | wc -l)
            echo "### Memory (${mem_count} files)"
            echo ""
            find "${TARGET_DIR}/memory" -type f -name "*.md" 2>/dev/null | head -5 | while read -r mf; do
                echo "#### $(basename "$mf")"
                echo '```'
                head -30 "$mf"
                echo '```'
                echo ""
            done
        fi
    fi

    # -- Section: LLM Session Logs --
    if [ -n "${INC_SESSIONS}" ]; then
        echo "## 3. LLM Session Logs"
        echo ""

        SESSION_DIR="${TARGET_DIR}/.openclaw/agents/main/sessions"
        if [ -d "${SESSION_DIR}" ]; then
            # Find recent session files (modified within the time window)
            local session_files
            session_files=$(find "${SESSION_DIR}" -name "*.jsonl" -type f 2>/dev/null | sort -r)

            if [ -z "${session_files}" ]; then
                echo "_No session log files found_"
                echo ""
            else
                local shown=0
                for sf in ${session_files}; do
                    # Check modification time
                    local file_mtime
                    file_mtime=$(stat -f %m "$sf" 2>/dev/null || stat -c %Y "$sf" 2>/dev/null || echo 0)
                    if [ "${file_mtime}" -lt "${CUTOFF_EPOCH}" ]; then
                        continue
                    fi

                    local session_name
                    session_name=$(basename "$sf" .jsonl)
                    local line_count
                    line_count=$(wc -l < "$sf" 2>/dev/null || echo 0)

                    echo "### Session: ${session_name} (${line_count} entries)"
                    echo ""

                    # Show last 20 exchanges (assistant + user messages)
                    echo "**Last 20 messages:**"
                    echo '```'
                    tail -40 "$sf" | jq -r '
                        select(.role == "user" or .role == "assistant") |
                        "[" + .role + "] " + (
                            if .content | type == "string" then
                                .content[:200]
                            elif .content | type == "array" then
                                ([.content[] | select(.type == "text") | .text[:200]] | join(" "))
                            else
                                "(non-text)"
                            end
                        )
                    ' 2>/dev/null | tail -20
                    echo '```'
                    echo ""

                    # Show any tool errors
                    local error_count
                    error_count=$(grep -c '"error"' "$sf" 2>/dev/null || echo 0)
                    if [ "${error_count}" -gt 0 ]; then
                        echo "**Errors found: ${error_count}**"
                        echo '```'
                        grep '"error"' "$sf" | tail -5 | jq -r '.error // .content // .' 2>/dev/null || true
                        echo '```'
                        echo ""
                    fi

                    shown=$((shown + 1))
                    if [ "${shown}" -ge 3 ]; then
                        echo "_... (showing most recent 3 sessions)_"
                        echo ""
                        break
                    fi
                done

                if [ "${shown}" -eq 0 ]; then
                    echo "_No sessions modified in the last ${HOURS} hours_"
                    echo ""
                fi
            fi
        else
            echo "_Session directory not found at ${SESSION_DIR}_"
            echo ""
        fi
    fi

    # -- Section: Matrix Messages --
    if [ -n "${INC_MATRIX}" ]; then
        echo "## 4. Matrix Messages"
        echo ""

        # Check if Matrix credentials are available
        local has_matrix
        has_matrix=$(jq -r '.matrixCredential.accessToken // empty' "${DEBUG_CONFIG}")
        if [ -z "${has_matrix}" ] && [ -z "${HICLAW_DEBUG_MATRIX_ACCESS_TOKEN:-}" ]; then
            echo "_Matrix credentials not configured. Skipping message export._"
            echo ""
        else
            # Try to find and export messages from rooms matching the worker name
            echo "Searching for Matrix rooms related to '${WORKER}'..." >&2
            local room_messages
            room_messages=$(bash "${SCRIPT_DIR}/export-matrix-messages.sh" \
                --room-name "${WORKER}" --hours "${HOURS}" 2>/dev/null || true)

            if [ -n "${room_messages}" ]; then
                local msg_count
                msg_count=$(echo "${room_messages}" | wc -l)
                echo "### Room messages matching '${WORKER}' (${msg_count} messages, last ${HOURS}h)"
                echo ""
                echo '```'
                echo "${room_messages}" | jq -r '
                    .time + " [" + .sender + "] " + (.body // "(no body)")[:300]
                ' 2>/dev/null | tail -50
                echo '```'
                echo ""
                if [ "${msg_count}" -gt 50 ]; then
                    echo "_... (showing last 50 of ${msg_count} messages)_"
                    echo ""
                fi
            else
                echo "_No Matrix messages found for rooms matching '${WORKER}'_"
                echo ""
            fi
        fi
    fi

    # -- Section: Summary --
    echo "## 5. Summary"
    echo ""
    echo "| Item | Status |"
    echo "|------|--------|"

    if [ -f "${TARGET_DIR}/openclaw.json" ]; then
        local model
        model=$(jq -r '.model // "unknown"' "${TARGET_DIR}/openclaw.json" 2>/dev/null)
        echo "| Model | ${model} |"
    fi

    if [ -d "${TARGET_DIR}/.openclaw/agents/main/sessions" ]; then
        local total_sessions
        total_sessions=$(find "${TARGET_DIR}/.openclaw/agents/main/sessions" -name "*.jsonl" 2>/dev/null | wc -l)
        echo "| Total sessions | ${total_sessions} |"
    fi

    if [ -f "${TARGET_DIR}/SOUL.md" ]; then
        echo "| SOUL.md | Present |"
    else
        echo "| SOUL.md | **Missing** |"
    fi

    if [ -d "${TARGET_DIR}/skills" ]; then
        local skill_count
        skill_count=$(find "${TARGET_DIR}/skills" -name "SKILL.md" 2>/dev/null | wc -l)
        echo "| Active skills | ${skill_count} |"
    fi

    echo ""
    echo "---"
    echo "_Report generated by debug-analysis skill_"
}

# Write output
if [ -n "${OUTPUT}" ]; then
    mkdir -p "$(dirname "${OUTPUT}")"
    generate_report > "${OUTPUT}"
    echo "Debug report written to ${OUTPUT}" >&2
else
    generate_report
fi
