#!/bin/bash
#
# Export HiClaw debug logs: Matrix messages + agent session logs
# Shell version of scripts/export-debug-log.py
#
# Usage:
#   ./export-debug-log.sh -r 1h              # Export last 1 hour
#   ./export-debug-log.sh -r 1d              # Export last 1 day
#   ./export-debug-log.sh -r 1h -c hiclaw-manager --room Worker
#   ./export-debug-log.sh -r 1h --no-redact
#

set -e

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
TIME_RANGE=""
CONTAINER_FILTER=""
ROOM_FILTER=""
HOMESERVER=""
TOKEN=""
ENV_FILE="${HOME}/hiclaw-manager.env"
MESSAGES_ONLY=false
NO_REDACT=false

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        -r|--range)
            TIME_RANGE="$2"
            shift 2
            ;;
        -c|--container)
            CONTAINER_FILTER="$2"
            shift 2
            ;;
        --room)
            ROOM_FILTER="$2"
            shift 2
            ;;
        -s|--homeserver)
            HOMESERVER="$2"
            shift 2
            ;;
        -t|--token)
            TOKEN="$2"
            shift 2
            ;;
        --env-file)
            ENV_FILE="$2"
            shift 2
            ;;
        --messages-only)
            MESSAGES_ONLY=true
            shift
            ;;
        --no-redact)
            NO_REDACT=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 -r <range> [options]"
            echo "  -r, --range       Time range (e.g. 10m, 1h, 1d) [required]"
            echo "  -c, --container   Filter containers by substring"
            echo "  --room            Filter Matrix rooms by substring"
            echo "  -s, --homeserver  Matrix homeserver URL"
            echo "  -t, --token       Matrix access token"
            echo "  --env-file        Path to hiclaw-manager.env"
            echo "  --messages-only   Only export m.room.message events"
            echo "  --no-redact       Disable PII redaction"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

if [[ -z "$TIME_RANGE" ]]; then
    echo "Error: --range is required (e.g. -r 1h, -r 1d)"
    exit 1
fi

# ---------------------------------------------------------------------------
# Helper functions
# ---------------------------------------------------------------------------

# Parse time range like "1h", "10m", "1d" to seconds
parse_range() {
    local range="$1"
    local num="${range//[mhds]/}"
    local unit="${range//[0-9]/}"
    
    case "${unit:0:1}" in
        m) echo "$((num * 60))" ;;
        h) echo "$((num * 3600))" ;;
        d) echo "$((num * 86400))" ;;
        s) echo "$num" ;;
        *) echo "0" ;;
    esac
}

# Sanitize filename
sanitize_filename() {
    local name="$1"
    echo "$name" | sed 's/[^\w\-. ]/_/g' | head -c 80
}

# Docker exec helper
docker_exec() {
    local container="$1"
    local cmd="$2"
    docker exec "$container" sh -c "$cmd" 2>/dev/null || echo ""
}

# Load env file
load_env_file() {
    local path="$1"
    if [[ -f "$path" ]]; then
        while IFS='=' read -r key value; do
            [[ -z "$key" || "$key" =~ ^# ]] && continue
            eval "$key=\"$value\""
        done < "$path"
    fi
}

# PII redaction patterns
redact_pii() {
    local text="$1"
    if [[ "$NO_REDACT" == "true" ]]; then
        echo "$text"
        return
    fi
    
    # ID Card (Chinese)
    text=$(echo "$text" | sed -E 's/[1-9][0-9]{5}(19|20)[0-9]{2}(0[1-9]|1[0-2])(0[1-9]|[12][0-9]|3[01])[0-9]{3}[0-9Xx]/****/g')
    # Phone (Chinese mobile)
    text=$(echo "$text" | sed -E 's/(86[- ]?)?1[3-9][0-9]{9}/****/g')
    # Email
    text=$(echo "$text" | sed -E 's/[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}/****/g')
    # IP address
    text=$(echo "$text" | sed -E 's/[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}/****/g')
    # Aliyun AccessKey
    text=$(echo "$text" | sed -E 's/LTAI[A-Za-z0-9]{12,30}/****/g')
    # AWS AccessKey
    text=$(echo "$text" | sed -E 's/(AKIA|ASIA)[A-Z0-9]{16}/****/g')
    # OpenAI key
    text=$(echo "$text" | sed -E 's/sk-[A-Za-z0-9]{20,}/****/g')
    # Matrix token
    text=$(echo "$text" | sed -E 's/syt_[A-Za-z0-9_\-]{10,}/****/g')
    # Bearer token
    text=$(echo "$text" | sed -E 's/Bearer [A-Za-z0-9\-_.]{20,}/Bearer ****/g')
    # Generic secret patterns
    text=$(echo "$text" | sed -E 's/(password|passwd|pwd|secret|token|api_key|access_key|secret_key|private_key|credential)[:=][^ ,}]*/\1=****/gI')
    # Hex secret (32+ chars)
    text=$(echo "$text" | sed -E 's/[A-Fa-f0-9]{32,}/****/g')
    
    echo "$text"
}

# Redact JSON string values
redact_json() {
    local json="$1"
    if [[ "$NO_REDACT" == "true" ]]; then
        echo "$json"
        return
    fi
    # Use jq to walk through JSON and redact string values
    echo "$json" | jq -r '
        def redact:
            if type == "string" then
                # Apply basic redaction for common patterns
                gsub("[1-9][0-9]{5}(19|20)[0-9]{2}(0[1-9]|1[0-2])(0[1-9]|[12][0-9]|3[01])[0-9]{3}[0-9Xx]"; "****")
                | gsub("(86[- ]?)?1[3-9][0-9]{9}"; "****")
                | gsub("[A-Za-z0-9._%+\\-]+@[A-Za-z0-9.\\-]+\\.[A-Za-z]{2,}"; "****")
                | gsub("[0-9]{1,3}\\.[0-9]{1,3}\\.[0-9]{1,3}\\.[0-9]{1,3}"; "****")
                | gsub("LTAI[A-Za-z0-9]{12,30}"; "****")
                | gsub("(AKIA|ASIA)[A-Z0-9]{16}"; "****")
                | gsub("sk-[A-Za-z0-9]{20,}"; "****")
                | gsub("syt_[A-Za-z0-9_\\-]{10,}"; "****")
                | gsub("[A-Fa-f0-9]{32,}"; "****")
            elif type == "object" then
                with_entries(.value = (.value | redact))
            elif type == "array" then
                map(redact)
            else
                .
            end;
        redact
    ' 2>/dev/null || echo "$json"
}

# ---------------------------------------------------------------------------
# Matrix API helpers
# ---------------------------------------------------------------------------

matrix_login() {
    local homeserver="$1"
    local user="$2"
    local password="$3"
    
    curl -s -X POST "${homeserver}/_matrix/client/v3/login" \
        -H "Content-Type: application/json" \
        -d '{"type":"m.login.password","identifier":{"type":"m.id.user","user":"${user}"},"password":"${password}"}' \
        | jq -r '.access_token' 2>/dev/null || echo ""
}

matrix_api() {
    local homeserver="$1"
    local token="$2"
    local endpoint="$3"
    local params="$4"
    
    local url="${homeserver}/_matrix/client/v3/${endpoint}"
    if [[ -n "$params" ]]; then
        url="${url}?${params}"
    fi
    
    curl -s -H "Authorization: Bearer ${token}" "${url}" 2>/dev/null || echo "{}"
}

fetch_room_messages() {
    local homeserver="$1"
    local token="$2"
    local room_id="$3"
    local since_ts="$4"
    
    local encoded_room_id=$(echo "$room_id" | sed 's/!/%21/g; s/#/%23/g')
    local messages=""
    local from_token=""
    local hit_boundary=false
    
    while [[ "$hit_boundary" == "false" ]]; do
        local params="dir=b&limit=100"
        if [[ -n "$from_token" ]]; then
            params="${params}&from=${from_token}"
        fi
        
        local data=$(matrix_api "$homeserver" "$token" "rooms/${encoded_room_id}/messages" "$params")
        local chunk=$(echo "$data" | jq -r '.chunk // []' 2>/dev/null)
        
        if [[ -z "$chunk" || "$chunk" == "[]" ]]; then
            break
        fi
        
        # Check for messages older than since_ts
        local oldest_ts=$(echo "$chunk" | jq -r '[.[].origin_server_ts // 0] | min' 2>/dev/null)
        if [[ -n "$oldest_ts" && "$oldest_ts" -lt "$since_ts" ]]; then
            hit_boundary=true
            # Filter messages within range
            messages=$(echo "$chunk" | jq -r --argjson since "$since_ts" \
                '[.[] | select((.origin_server_ts // 0) >= $since)]' 2>/dev/null)
            break
        fi
        
        messages="${messages}${chunk}"
        
        local next_token=$(echo "$data" | jq -r '.end // empty' 2>/dev/null)
        if [[ -z "$next_token" || "$next_token" == "$from_token" ]]; then
            break
        fi
        from_token="$next_token"
    done
    
    # Reverse order (oldest first)
    echo "$messages" | jq -r 'reverse' 2>/dev/null || echo "[]"
}

# ---------------------------------------------------------------------------
# Export Matrix messages
# ---------------------------------------------------------------------------
export_matrix_messages() {
    local out_dir="$1"
    local since_epoch="$2"
    
    mkdir -p "$out_dir"
    
    # Load env for credentials
    load_env_file "$ENV_FILE"
    
    # Determine homeserver
    if [[ -z "$HOMESERVER" ]]; then
        local port="${HICLAW_PORT_GATEWAY:-18080}"
        HOMESERVER="http://127.0.0.1:${port}"
    fi
    
    # Get token if not provided
    if [[ -z "$TOKEN" ]]; then
        # Try manager login first
        if [[ -n "${HICLAW_MANAGER_PASSWORD:-}" ]]; then
            TOKEN=$(matrix_login "$HOMESERVER" "manager" "$HICLAW_MANAGER_PASSWORD")
        fi
        
        # Fallback to admin login
        if [[ -z "$TOKEN" && -n "${HICLAW_ADMIN_PASSWORD:-}" ]]; then
            local admin_user="${HICLAW_ADMIN_USER:-admin}"
            TOKEN=$(matrix_login "$HOMESERVER" "$admin_user" "$HICLAW_ADMIN_PASSWORD")
        fi
        
        if [[ -z "$TOKEN" ]]; then
            echo "  [matrix] No usable credentials, skipping"
            return 0 0
        fi
    fi
    
    local since_ts=$((since_epoch * 1000))
    
    # Get joined rooms
    local rooms=$(matrix_api "$HOMESERVER" "$TOKEN" "joined_rooms" | jq -r '.joined_rooms[]' 2>/dev/null)
    
    if [[ -z "$rooms" ]]; then
        echo "  [matrix] No rooms found"
        return 0 0
    fi
    
    local total_messages=0
    local total_rooms=0
    
    while IFS= read -r room_id; do
        [[ -z "$room_id" ]] && continue
        
        # Get room name
        local encoded_room_id=$(echo "$room_id" | sed 's/!/%21/g; s/#/%23/g')
        local room_name=$(matrix_api "$HOMESERVER" "$TOKEN" "rooms/${encoded_room_id}/state/m.room.name" \
            | jq -r '.name // ""' 2>/dev/null)
        
        # Filter by room
        if [[ -n "$ROOM_FILTER" ]]; then
            if [[ "$room_id" != *"$ROOM_FILTER"* && "$room_name" != *"$ROOM_FILTER"* ]]; then
                continue
            fi
        fi
        
        local display="${room_name:-$room_id}"
        echo -n "  ${display} ... "
        
        # Fetch messages
        local messages=$(fetch_room_messages "$HOMESERVER" "$TOKEN" "$room_id" "$since_ts")
        
        if [[ "$MESSAGES_ONLY" == "true" ]]; then
            messages=$(echo "$messages" | jq -r '[.[] | select(.type == "m.room.message")]' 2>/dev/null)
        fi
        
        local count=$(echo "$messages" | jq -r 'length' 2>/dev/null)
        
        if [[ -z "$count" || "$count" == "0" ]]; then
            echo "0 messages, skipped"
            continue
        fi
        
        # Build filename
        local name_part=$(sanitize_filename "$room_name")
        local id_part=$(sanitize_filename "$room_id")
        local filename="${name_part}_${id_part}.jsonl"
        [[ -z "$name_part" ]] && filename="${id_part}.jsonl"
        
        # Format and write messages
        echo "$messages" | jq -c '.[] | {
            event_id: .event_id,
            type: .type,
            sender: .sender,
            timestamp: .origin_server_ts,
            time: (.origin_server_ts / 1000 | strftime("%Y-%m-%dT%H:%M:%SZ")),
            msgtype: .content.msgtype,
            body: .content.body
        }' 2>/dev/null | while IFS= read -r line; do
            if [[ "$NO_REDACT" != "true" ]]; then
                line=$(redact_json "$line")
            fi
            echo "$line"
        done > "${out_dir}/${filename}"
        
        echo "${count} messages -> ${filename}"
        total_messages=$((total_messages + count))
        total_rooms=$((total_rooms + 1))
    done <<< "$rooms"
    
    echo "$total_rooms $total_messages"
}

# ---------------------------------------------------------------------------
# Export agent sessions
# ---------------------------------------------------------------------------
export_agent_sessions() {
    local out_dir="$1"
    local since_epoch="$2"
    
    mkdir -p "$out_dir"
    
    # List hiclaw containers
    local containers=$(docker ps --format '{{.Names}}' --filter 'name=hiclaw-' 2>/dev/null)
    
    if [[ -n "$CONTAINER_FILTER" ]]; then
        containers=$(echo "$containers" | grep "$CONTAINER_FILTER")
    fi
    
    if [[ -z "$containers" ]]; then
        echo "  [sessions] No matching containers"
        return 0 0
    fi
    
    local total_sessions=0
    local total_events=0
    
    while IFS= read -r container; do
        [[ -z "$container" ]] && continue
        
        # Detect runtime
        local runtime=""
        local sessions_dir=""
        
        # Check OpenClaw standard path
        if docker_exec "$container" "test -d .openclaw/agents/main/sessions && echo yes" | grep -q yes; then
            runtime="openclaw"
            sessions_dir=".openclaw/agents/main/sessions"
        else
            # Check CoPaw path
            local worker_name=$(docker_exec "$container" "echo \$HICLAW_WORKER_NAME")
            if [[ -n "$worker_name" ]]; then
                local copaw_dir="${worker_name}/.copaw/sessions"
                if docker_exec "$container" "test -d '${copaw_dir}' && echo yes" | grep -q yes; then
                    runtime="copaw"
                    sessions_dir="$copaw_dir"
                fi
            fi
        fi
        
        if [[ -z "$runtime" ]]; then
            echo "  ${container}: no sessions directory, skipped"
            continue
        fi
        
        local container_dir="${out_dir}/${container}"
        mkdir -p "$container_dir"
        
        if [[ "$runtime" == "openclaw" ]]; then
            # Export OpenClaw sessions (.jsonl files)
            local session_files=$(docker_exec "$container" "ls '${sessions_dir}'/*.jsonl 2>/dev/null")
            
            local session_count=0
            local event_count=0
            
            while IFS= read -r session_path; do
                [[ -z "$session_path" ]] && continue
                
                local filename=$(basename "$session_path")
                local raw=$(docker_exec "$container" "cat '${session_path}'")
                
                [[ -z "$raw" ]] && continue
                
                # Filter events by time range
                local filtered=$(echo "$raw" | jq -c --argjson since "$since_epoch" \
                    'lines | .[] | select((.timestamp | fromdateiso8601 // 0) >= $since or .type == "session")' 2>/dev/null)
                
                if [[ -z "$filtered" ]]; then
                    continue
                fi
                
                if [[ "$NO_REDACT" != "true" ]]; then
                    filtered=$(echo "$filtered" | while IFS= read -r line; do
                        redact_json "$line"
                    done)
                fi
                
                echo "$filtered" > "${container_dir}/${filename}"
                
                local events=$(echo "$filtered" | wc -l)
                event_count=$((event_count + events))
                session_count=$((session_count + 1))
                
                echo "  ${container}/${filename} (${runtime}): ${events} events"
            done <<< "$session_files"
            
            if [[ "$session_count" -gt 0 ]]; then
                total_sessions=$((total_sessions + session_count))
                total_events=$((total_events + event_count))
            else
                rmdir "$container_dir" 2>/dev/null || true
                echo "  ${container} (${runtime}): no sessions in range"
            fi
        else
            # Export CoPaw sessions (.json files)
            local session_files=$(docker_exec "$container" "find '${sessions_dir}' -name '*.json' -type f 2>/dev/null")
            
            local session_count=0
            local event_count=0
            
            while IFS= read -r session_path; do
                [[ -z "$session_path" ]] && continue
                
                local raw=$(docker_exec "$container" "cat '${session_path}'")
                [[ -z "$raw" ]] && continue
                
                local basename=$(basename "$session_path" .json)
                
                # Parse CoPaw session format
                local header=$(echo "$raw" | jq -c '{
                    type: "session",
                    runtime: "copaw",
                    agent_name: .agent.name,
                    session_key: "'"$basename"'",
                    compressed_summary: .agent.memory._compressed_summary
                }' 2>/dev/null)
                
                # Extract messages in range
                local content=$(echo "$raw" | jq -c --argjson since "$since_epoch" \
                    '[.agent.memory.content[][] | select((.timestamp | fromdateiso8601 // 0) >= $since)]' 2>/dev/null)
                
                local count=$(echo "$content" | jq -r 'length' 2>/dev/null)
                [[ -z "$count" || "$count" == "0" ]] && continue
                
                # Write output
                {
                    if [[ "$NO_REDACT" != "true" ]]; then
                        redact_json "$header"
                    else
                        echo "$header"
                    fi
                    echo "$content" | jq -c '.[] | {
                        type: "message",
                        turn: .turn,
                        id: .id,
                        role: .role,
                        name: .name,
                        timestamp: .timestamp,
                        content: .content
                    }' | while IFS= read -r line; do
                        if [[ "$NO_REDACT" != "true" ]]; then
                            redact_json "$line"
                        else
                            echo "$line"
                        fi
                    done
                } > "${container_dir}/${basename}.jsonl"
                
                event_count=$((event_count + count))
                session_count=$((session_count + 1))
                
                echo "  ${container}/${basename}.jsonl (${runtime}): ${count} events"
            done <<< "$session_files"
            
            if [[ "$session_count" -gt 0 ]]; then
                total_sessions=$((total_sessions + session_count))
                total_events=$((total_events + event_count))
            else
                rmdir "$container_dir" 2>/dev/null || true
                echo "  ${container} (${runtime}): no sessions in range"
            fi
        fi
    done <<< "$containers"
    
    echo "$total_sessions $total_events"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
    local range_seconds=$(parse_range "$TIME_RANGE")
    local since_epoch=$(( $(date +%s) - range_seconds ))
    local since_human=$(date -u -d "@$since_epoch" +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -r "$since_epoch" +"%Y-%m-%dT%H:%M:%SZ")
    local now_str=$(date +"%Y%m%d-%H%M%S")
    
    local run_dir="debug-log/${now_str}"
    mkdir -p "$run_dir"
    
    echo "HiClaw Debug Log Export"
    echo "  Range: last ${TIME_RANGE} (since ${since_human})"
    echo "  Output: ${run_dir}"
    echo "  PII redaction: $([[ '$NO_REDACT' == 'true' ]] && echo 'off' || echo 'on')"
    echo ""
    
    # --- Matrix messages ---
    echo "=== Matrix Messages ==="
    local matrix_dir="${run_dir}/matrix-messages"
    local result=$(export_matrix_messages "$matrix_dir" "$since_epoch")
    local rooms=$(echo "$result" | cut -d' ' -f1)
    local messages=$(echo "$result" | cut -d' ' -f2)
    echo ""
    
    # --- Agent sessions ---
    echo "=== Agent Sessions ==="
    local sessions_dir="${run_dir}/agent-sessions"
    local result=$(export_agent_sessions "$sessions_dir" "$since_epoch")
    local sessions=$(echo "$result" | cut -d' ' -f1)
    local events=$(echo "$result" | cut -d' ' -f2)
    echo ""
    
    # --- Summary ---
    {
        echo "HiClaw Debug Log"
        echo "Exported at: ${now_str}"
        echo "Range: last ${TIME_RANGE} (since ${since_human})"
        echo "PII redaction: $([[ '$NO_REDACT' == 'true' ]] && echo 'off' || echo 'on')"
        echo ""
        echo "Matrix messages: ${messages} messages from ${rooms} rooms"
        echo "Agent sessions: ${events} events from ${sessions} sessions"
    } > "${run_dir}/summary.txt"
    
    echo "Done. ${messages} messages from ${rooms} rooms, ${events} events from ${sessions} sessions"
    echo "Output: ${run_dir}"
}

main