#!/bin/bash
# export-matrix-messages.sh — Export Matrix room messages for debugging.
# Uses the MatrixCredential from debug-config.json to authenticate.
#
# Usage:
#   bash ~/skills/debug-analysis/scripts/export-matrix-messages.sh --room-id '!roomid:server' --hours 24
#   bash ~/skills/debug-analysis/scripts/export-matrix-messages.sh --room-name 'Worker' --hours 6
#   bash ~/skills/debug-analysis/scripts/export-matrix-messages.sh --list-rooms
#
# Output: JSONL to stdout (one JSON object per message line)

set -e

WORKSPACE="${HOME}"
DEBUG_CONFIG="${WORKSPACE}/debug-config.json"

if [ ! -f "${DEBUG_CONFIG}" ]; then
    echo "ERROR: debug-config.json not found at ${DEBUG_CONFIG}" >&2
    exit 1
fi

# Read Matrix credentials from debug-config.json
MATRIX_USER_ID=$(jq -r '.matrixCredential.userID // empty' "${DEBUG_CONFIG}")
MATRIX_ACCESS_TOKEN=$(jq -r '.matrixCredential.accessToken // empty' "${DEBUG_CONFIG}")

# Fallback to env vars (set by worker-entrypoint.sh)
MATRIX_USER_ID="${MATRIX_USER_ID:-${HICLAW_DEBUG_MATRIX_USER_ID:-}}"
MATRIX_ACCESS_TOKEN="${MATRIX_ACCESS_TOKEN:-${HICLAW_DEBUG_MATRIX_ACCESS_TOKEN:-}}"

if [ -z "${MATRIX_ACCESS_TOKEN}" ]; then
    echo "ERROR: No Matrix access token found in debug-config.json or environment" >&2
    exit 1
fi

# Determine Matrix homeserver URL from openclaw.json
MATRIX_HOMESERVER=""
if [ -f "${WORKSPACE}/openclaw.json" ]; then
    MATRIX_HOMESERVER=$(jq -r '.channels.matrix.homeserver // empty' "${WORKSPACE}/openclaw.json")
fi
# Fallback to env var
MATRIX_HOMESERVER="${MATRIX_HOMESERVER:-${HICLAW_MATRIX_URL:-}}"

if [ -z "${MATRIX_HOMESERVER}" ]; then
    echo "ERROR: Cannot determine Matrix homeserver URL" >&2
    exit 1
fi

# Parse arguments
ROOM_ID=""
ROOM_NAME=""
HOURS=24
LIST_ROOMS=false

while [ $# -gt 0 ]; do
    case "$1" in
        --room-id)
            ROOM_ID="$2"; shift 2 ;;
        --room-name)
            ROOM_NAME="$2"; shift 2 ;;
        --hours)
            HOURS="$2"; shift 2 ;;
        --list-rooms)
            LIST_ROOMS=true; shift ;;
        *)
            echo "Unknown option: $1" >&2
            echo "Usage: $0 [--room-id ID | --room-name NAME | --list-rooms] [--hours N]" >&2
            exit 1 ;;
    esac
done

AUTH_HEADER="Authorization: Bearer ${MATRIX_ACCESS_TOKEN}"

# Helper: Matrix API GET request
matrix_get() {
    local endpoint="$1"
    curl -sf -H "${AUTH_HEADER}" "${MATRIX_HOMESERVER}/_matrix/client/v3/${endpoint}" 2>/dev/null
}

# List joined rooms
list_rooms() {
    local rooms
    rooms=$(matrix_get "joined_rooms" | jq -r '.joined_rooms[]' 2>/dev/null)
    if [ -z "${rooms}" ]; then
        echo "No joined rooms found or API error" >&2
        return 1
    fi

    echo "Joined rooms:" >&2
    for room_id in ${rooms}; do
        local encoded
        encoded=$(python3 -c "import urllib.parse; print(urllib.parse.quote('${room_id}'))" 2>/dev/null || echo "${room_id}")
        local name
        name=$(matrix_get "rooms/${encoded}/state/m.room.name" | jq -r '.name // "unnamed"' 2>/dev/null || echo "unnamed")
        echo "  ${name}  ${room_id}" >&2
    done
}

# Find room ID by name substring
find_room_by_name() {
    local search="$1"
    local rooms
    rooms=$(matrix_get "joined_rooms" | jq -r '.joined_rooms[]' 2>/dev/null)

    for room_id in ${rooms}; do
        local encoded
        encoded=$(python3 -c "import urllib.parse; print(urllib.parse.quote('${room_id}'))" 2>/dev/null || echo "${room_id}")
        local name
        name=$(matrix_get "rooms/${encoded}/state/m.room.name" | jq -r '.name // ""' 2>/dev/null || echo "")
        if echo "${name}" | grep -qi "${search}"; then
            echo "${room_id}"
            return 0
        fi
    done

    echo "ERROR: No room found matching '${search}'" >&2
    return 1
}

# Export messages from a room
export_room_messages() {
    local room_id="$1"
    local since_ts="$2"
    local encoded
    encoded=$(python3 -c "import urllib.parse; print(urllib.parse.quote('${room_id}'))" 2>/dev/null || echo "${room_id}")

    local from_token=""
    local all_messages="[]"

    while true; do
        local params="dir=b&limit=100"
        if [ -n "${from_token}" ]; then
            local encoded_token
            encoded_token=$(python3 -c "import urllib.parse; print(urllib.parse.quote('${from_token}'))" 2>/dev/null || echo "${from_token}")
            params="${params}&from=${encoded_token}"
        fi

        local response
        response=$(curl -sf -H "${AUTH_HEADER}" \
            "${MATRIX_HOMESERVER}/_matrix/client/v3/rooms/${encoded}/messages?${params}" 2>/dev/null)

        if [ -z "${response}" ]; then
            echo "WARNING: Empty response from messages API" >&2
            break
        fi

        local chunk
        chunk=$(echo "${response}" | jq '.chunk // []')
        local chunk_len
        chunk_len=$(echo "${chunk}" | jq 'length')

        if [ "${chunk_len}" = "0" ]; then
            break
        fi

        # Filter messages by timestamp and format
        local filtered
        filtered=$(echo "${chunk}" | jq --argjson since "${since_ts}" '[
            .[] | select(.origin_server_ts >= $since and .type == "m.room.message") |
            {
                event_id: .event_id,
                type: .type,
                sender: .sender,
                timestamp: .origin_server_ts,
                time: (.origin_server_ts / 1000 | todate),
                msgtype: .content.msgtype,
                body: .content.body
            }
        ]')

        all_messages=$(echo "${all_messages}" "${filtered}" | jq -s '.[0] + .[1]')

        # Check if we've gone past the time boundary
        local oldest_ts
        oldest_ts=$(echo "${chunk}" | jq '[.[].origin_server_ts] | min')
        if [ "${oldest_ts}" != "null" ] && [ "${oldest_ts}" -lt "${since_ts}" ]; then
            break
        fi

        local next_token
        next_token=$(echo "${response}" | jq -r '.end // empty')
        if [ -z "${next_token}" ] || [ "${next_token}" = "${from_token}" ]; then
            break
        fi
        from_token="${next_token}"
    done

    # Reverse to chronological order and output as JSONL
    echo "${all_messages}" | jq -r 'sort_by(.timestamp) | .[] | @json'
}

# Main logic
if [ "${LIST_ROOMS}" = "true" ]; then
    list_rooms
    exit 0
fi

# Resolve room ID from name if needed
if [ -z "${ROOM_ID}" ] && [ -n "${ROOM_NAME}" ]; then
    ROOM_ID=$(find_room_by_name "${ROOM_NAME}")
    if [ -z "${ROOM_ID}" ]; then
        exit 1
    fi
    echo "Found room: ${ROOM_ID}" >&2
fi

if [ -z "${ROOM_ID}" ]; then
    echo "ERROR: Either --room-id or --room-name is required" >&2
    echo "Use --list-rooms to see available rooms" >&2
    exit 1
fi

# Calculate since timestamp (milliseconds)
SINCE_TS=$(python3 -c "import time; print(int((time.time() - ${HOURS} * 3600) * 1000))" 2>/dev/null)
if [ -z "${SINCE_TS}" ]; then
    # Fallback: use date command
    SINCE_TS=$(( ($(date +%s) - HOURS * 3600) * 1000 ))
fi

echo "Exporting messages from room ${ROOM_ID} (last ${HOURS} hours)..." >&2
export_room_messages "${ROOM_ID}" "${SINCE_TS}"
