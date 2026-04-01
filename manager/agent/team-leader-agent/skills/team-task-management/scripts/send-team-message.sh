#!/bin/bash
# send-team-message.sh - Send a message to a Matrix room with @mentions
#
# Usage:
#   send-team-message.sh --room-id <ROOM_ID> --to <@user:domain> --message <TEXT>
#
# The message will include proper m.mentions so the target user processes it.
# Matrix credentials are read from ~/openclaw.json.

set -euo pipefail

ROOM_ID=""
TO_USER=""
MESSAGE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --room-id)  ROOM_ID="$2";  shift 2 ;;
        --to)       TO_USER="$2";  shift 2 ;;
        --message)  MESSAGE="$2";  shift 2 ;;
        *) echo "Unknown argument: $1" >&2; exit 1 ;;
    esac
done

if [ -z "${ROOM_ID}" ] || [ -z "${TO_USER}" ] || [ -z "${MESSAGE}" ]; then
    echo "Usage: send-team-message.sh --room-id <ROOM_ID> --to <@user:domain> --message <TEXT>" >&2
    exit 1
fi

# Read Matrix credentials from openclaw.json
# CoPaw workers store openclaw.json in the working dir, not HOME
OPENCLAW=""
for _candidate in "./openclaw.json" "../openclaw.json" "${HOME}/openclaw.json"; do
    if [ -f "${_candidate}" ]; then
        OPENCLAW="${_candidate}"
        break
    fi
done
if [ -z "${OPENCLAW}" ]; then
    echo "ERROR: openclaw.json not found" >&2
    exit 1
fi

HOMESERVER=$(jq -r '.channels.matrix.homeserver // empty' "${OPENCLAW}")
ACCESS_TOKEN=$(jq -r '.channels.matrix.accessToken // empty' "${OPENCLAW}")

if [ -z "${HOMESERVER}" ] || [ -z "${ACCESS_TOKEN}" ]; then
    echo "ERROR: Matrix homeserver or accessToken not found in openclaw.json" >&2
    exit 1
fi

# Note: use homeserver URL as-is from openclaw.json
# CoPaw workers connect via the gateway (matrix-local.hiclaw.io:8080)

# Ensure we have joined the room (accept invite if pending)
ROOM_ENC=$(echo "${ROOM_ID}" | sed 's/!/%21/g')
curl -sf -X POST "${HOMESERVER}/_matrix/client/v3/rooms/${ROOM_ENC}/join" \
    -H "Authorization: Bearer ${ACCESS_TOKEN}" \
    -H 'Content-Type: application/json' -d '{}' > /dev/null 2>&1 || true

# Send message with m.mentions
TXN_ID="$(date +%s%N)$$"
ROOM_ENC=$(echo "${ROOM_ID}" | sed 's/!/%21/g')

# Escape message for JSON
ESCAPED_MSG=$(echo "${MESSAGE}" | jq -Rs '.' | sed 's/^"//;s/"$//')

RESP=$(curl -sf -X PUT \
    "${HOMESERVER}/_matrix/client/v3/rooms/${ROOM_ENC}/send/m.room.message/${TXN_ID}" \
    -H "Authorization: Bearer ${ACCESS_TOKEN}" \
    -H 'Content-Type: application/json' \
    -d '{
        "msgtype": "m.text",
        "body": "'"${ESCAPED_MSG}"'",
        "m.mentions": {
            "user_ids": ["'"${TO_USER}"'"]
        }
    }' 2>&1) || {
    echo "ERROR: Failed to send message to ${ROOM_ID}" >&2
    echo "${RESP}" >&2
    exit 1
}

EVENT_ID=$(echo "${RESP}" | jq -r '.event_id // empty' 2>/dev/null)
if [ -n "${EVENT_ID}" ]; then
    echo "OK: Message sent to ${ROOM_ID} (event: ${EVENT_ID})"
else
    echo "WARNING: Message may not have been sent. Response: ${RESP}" >&2
fi
