#!/bin/bash
# matrix-client.sh - Matrix API wrapper for integration tests
#
# All requests are sent via exec_in_manager() (docker exec into the Manager container)
# so that Matrix (port 6167) does not need to be exposed to the host.

# Source test-helpers for environment vars
_MATRIX_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${_MATRIX_LIB_DIR}/test-helpers.sh" 2>/dev/null || true

# ============================================================
# User Management
# ============================================================

# Register a Matrix user
# Usage: matrix_register <username> <password>
# Returns: JSON response with access_token
matrix_register() {
    local username="$1"
    local password="$2"
    local token="${TEST_REGISTRATION_TOKEN}"

    exec_in_manager curl -sf -X POST "${TEST_MATRIX_DIRECT_URL}/_matrix/client/v3/register" \
        -H 'Content-Type: application/json' \
        -d '{
            "username": "'"${username}"'",
            "password": "'"${password}"'",
            "auth": {
                "type": "m.login.registration_token",
                "token": "'"${token}"'"
            }
        }'
}

# Login to Matrix
# Usage: matrix_login <username> <password>
# Returns: JSON with access_token
matrix_login() {
    local username="$1"
    local password="$2"

    exec_in_manager curl -sf -X POST "${TEST_MATRIX_DIRECT_URL}/_matrix/client/v3/login" \
        -H 'Content-Type: application/json' \
        -d '{
            "type": "m.login.password",
            "identifier": {"type": "m.id.user", "user": "'"${username}"'"},
            "password": "'"${password}"'"
        }'
}

# ============================================================
# Room Management
# ============================================================

# Get list of joined rooms
# Usage: matrix_joined_rooms <access_token>
matrix_joined_rooms() {
    local token="$1"
    exec_in_manager curl -sf "${TEST_MATRIX_DIRECT_URL}/_matrix/client/v3/joined_rooms" \
        -H "Authorization: Bearer ${token}"
}

# URL-encode a room ID for use in URL paths (! -> %21)
_encode_room_id() {
    echo "${1//!/%21}"
}

# ============================================================
# Messaging
# ============================================================

# Send a message to a room
# Usage: matrix_send_message <access_token> <room_id> <message_body>
# Returns: JSON with event_id
matrix_send_message() {
    local token="$1"
    local room_id="$2"
    local body="$3"
    local txn_id="$(date +%s%N)"
    local room_enc
    room_enc="$(_encode_room_id "${room_id}")"

    # Escape newlines and special chars for JSON
    local escaped_body
    escaped_body=$(echo "$body" | jq -Rs '.' | sed 's/^"//;s/"$//')

    exec_in_manager curl -sf -X PUT "${TEST_MATRIX_DIRECT_URL}/_matrix/client/v3/rooms/${room_enc}/send/m.room.message/${txn_id}" \
        -H "Authorization: Bearer ${token}" \
        -H 'Content-Type: application/json' \
        -d '{
            "msgtype": "m.text",
            "body": "'"${escaped_body}"'"
        }'
}

# Read recent messages from a room
# Usage: matrix_read_messages <access_token> <room_id> [limit]
# Returns: JSON with messages
matrix_read_messages() {
    local token="$1"
    local room_id="$2"
    local limit="${3:-20}"
    local room_enc
    room_enc="$(_encode_room_id "${room_id}")"

    exec_in_manager curl -sf "${TEST_MATRIX_DIRECT_URL}/_matrix/client/v3/rooms/${room_enc}/messages?dir=b&limit=${limit}" \
        -H "Authorization: Bearer ${token}"
}

# Wait for a reply from a specific user in a room
# Usage: matrix_wait_for_reply <access_token> <room_id> <from_user_prefix> [timeout_seconds] [after_event_id]
# Returns: the reply message body, or empty string on timeout
#
# This function only returns messages that appear AFTER a baseline event.
# If after_event_id is provided (e.g. the event_id of the message you just sent),
# it is used as the baseline — this is more reliable than snapshotting, because it
# avoids a race where a delayed response to a *previous* request lands between
# your send and the snapshot and is then mistaken for the reply you're waiting for.
# When after_event_id is omitted, the function falls back to snapshotting the
# latest event_id from the target user (legacy behaviour).
matrix_wait_for_reply() {
    local token="$1"
    local room_id="$2"
    local from_user="$3"
    local timeout="${4:-180}"
    local after_event="${5:-}"
    local elapsed=0

    # Determine baseline: prefer the caller-supplied event_id, fall back to snapshot
    local baseline_event
    if [ -n "${after_event}" ]; then
        baseline_event="${after_event}"
    else
        baseline_event=$(matrix_read_messages "${token}" "${room_id}" 5 2>/dev/null | \
            jq -r --arg user "${from_user}" \
            '[.chunk[] | select(.sender | startswith($user)) | .event_id] | first // ""' 2>/dev/null)
    fi

    while [ "${elapsed}" -lt "${timeout}" ]; do
        sleep 10
        elapsed=$((elapsed + 10))

        local messages
        messages=$(matrix_read_messages "${token}" "${room_id}" 10 2>/dev/null) || continue

        if [ -n "${after_event}" ]; then
            # Strict mode: only consider messages whose origin_server_ts is strictly
            # greater than the after_event's timestamp. This filters out delayed
            # responses to earlier requests that happen to arrive during our poll window.
            local after_ts latest_body
            after_ts=$(echo "${messages}" | jq -r --arg eid "${after_event}" \
                '[.chunk[] | select(.event_id == $eid) | .origin_server_ts] | first // 0' 2>/dev/null)
            # If the after_event isn't in this batch, fetch its timestamp directly
            if [ "${after_ts}" = "0" ] || [ -z "${after_ts}" ]; then
                local room_enc
                room_enc="$(_encode_room_id "${room_id}")"
                after_ts=$(exec_in_manager curl -sf \
                    "${TEST_MATRIX_DIRECT_URL}/_matrix/client/v3/rooms/${room_enc}/event/$(_encode_event_id "${after_event}")" \
                    -H "Authorization: Bearer ${token}" 2>/dev/null | jq -r '.origin_server_ts // 0' 2>/dev/null)
            fi
            latest_body=$(echo "${messages}" | jq -r --arg user "${from_user}" --argjson ts "${after_ts:-0}" \
                '[.chunk[] | select(.sender | startswith($user)) | select(.origin_server_ts > $ts) | .content.body] | first // empty' 2>/dev/null)
            if [ -n "${latest_body}" ]; then
                echo "${latest_body}"
                return 0
            fi
        else
            # Legacy mode: compare event_id against baseline snapshot
            local latest_event latest_body
            latest_event=$(echo "${messages}" | jq -r --arg user "${from_user}" \
                '[.chunk[] | select(.sender | startswith($user)) | .event_id] | first // ""' 2>/dev/null)
            latest_body=$(echo "${messages}" | jq -r --arg user "${from_user}" \
                '[.chunk[] | select(.sender | startswith($user)) | .content.body] | first // empty' 2>/dev/null)

            if [ -n "${latest_body}" ] && [ "${latest_event}" != "${baseline_event}" ]; then
                echo "${latest_body}"
                return 0
            fi
        fi
    done

    return 1
}

# URL-encode a Matrix event ID for use in URL paths ($ -> %24, etc.)
_encode_event_id() {
    local eid="$1"
    eid="${eid//\$/%24}"
    eid="${eid//\//%2F}"
    eid="${eid//+/%2B}"
    echo "${eid}"
}

# Wait for a message containing a specific keyword from a user
# Usage: matrix_wait_for_message_containing <token> <room_id> <from_user_prefix> <keyword> [timeout_seconds]
# Returns: the matching message body, or empty string on timeout
# <keyword> is passed to grep -qi (supports regex like "done\|完成")
matrix_wait_for_message_containing() {
    local token="$1"
    local room_id="$2"
    local from_user="$3"
    local keyword="$4"
    local timeout="${5:-1800}"
    local elapsed=0

    # Snapshot the latest known event_id to avoid returning stale messages
    local baseline_event
    baseline_event=$(matrix_read_messages "${token}" "${room_id}" 5 2>/dev/null | \
        jq -r --arg user "${from_user}" \
        '[.chunk[] | select(.sender | startswith($user)) | .event_id] | first // ""' 2>/dev/null)

    while [ "${elapsed}" -lt "${timeout}" ]; do
        sleep 15
        elapsed=$((elapsed + 15))

        local messages all_bodies
        messages=$(matrix_read_messages "${token}" "${room_id}" 20 2>/dev/null) || continue

        # Check if there's any new message from the target user containing the keyword
        local latest_event
        latest_event=$(echo "${messages}" | jq -r --arg user "${from_user}" \
            '[.chunk[] | select(.sender | startswith($user)) | .event_id] | first // ""' 2>/dev/null)

        if [ "${latest_event}" != "${baseline_event}" ]; then
            # There are new messages; check if any match the keyword
            local matching_body
            matching_body=$(echo "${messages}" | jq -r --arg user "${from_user}" \
                '[.chunk[] | select(.sender | startswith($user)) | .content.body] | join("\n")' 2>/dev/null)
            if echo "${matching_body}" | grep -qi "${keyword}"; then
                echo "${matching_body}"
                return 0
            fi
        fi
    done

    return 1
}

# Create a DM room with another user
# Usage: matrix_create_dm_room <access_token> <other_user_id>
# Returns: room_id
matrix_create_dm_room() {
    local token="$1"
    local other_user="$2"

    local result
    result=$(exec_in_manager curl -sf -X POST "${TEST_MATRIX_DIRECT_URL}/_matrix/client/v3/createRoom" \
        -H "Authorization: Bearer ${token}" \
        -H 'Content-Type: application/json' \
        -d '{
            "preset": "trusted_private_chat",
            "invite": ["'"${other_user}"'"],
            "is_direct": true
        }' 2>/dev/null)

    echo "${result}" | jq -r '.room_id // empty'
}

# Find a room by name prefix
# Usage: matrix_find_room_by_name <access_token> <name_pattern>
# Returns: room_id of first matching room
matrix_find_room_by_name() {
    local token="$1"
    local name_pattern="$2"

    local rooms
    rooms=$(matrix_joined_rooms "${token}" | jq -r '.joined_rooms[]')

    for room_id in ${rooms}; do
        local room_enc name
        room_enc="$(_encode_room_id "${room_id}")"
        name=$(exec_in_manager curl -sf "${TEST_MATRIX_DIRECT_URL}/_matrix/client/v3/rooms/${room_enc}/state/m.room.name" \
            -H "Authorization: Bearer ${token}" 2>/dev/null | jq -r '.name // empty')
        if echo "${name}" | grep -qi "${name_pattern}"; then
            echo "${room_id}"
            return 0
        fi
    done

    return 1
}

# Find a DM room between two users
# Usage: matrix_find_dm_room <access_token> <other_user_id>
matrix_find_dm_room() {
    local token="$1"
    local other_user="$2"

    log_info "Looking for DM room with user: ${other_user}"

    local rooms
    rooms=$(matrix_joined_rooms "${token}" | jq -r '.joined_rooms[]')

    for room_id in ${rooms}; do
        local room_enc members member_count
        room_enc="$(_encode_room_id "${room_id}")"
        members=$(exec_in_manager curl -sf "${TEST_MATRIX_DIRECT_URL}/_matrix/client/v3/rooms/${room_enc}/members" \
            -H "Authorization: Bearer ${token}" 2>/dev/null | jq -r '.chunk[].state_key' 2>/dev/null)

        # DM rooms have exactly 2 members; skip group rooms (3+ members)
        member_count=$(echo "${members}" | grep -c '.' 2>/dev/null || echo 0)
        if [ "${member_count}" -eq 2 ] && echo "${members}" | grep -q "${other_user}"; then
            echo "${room_id}"
            return 0
        fi
    done

    return 1
}
