#!/bin/bash
# send-file.sh — Upload a local file to Matrix and send as m.file attachment
#
# Usage: send-file.sh <file_path> <room_id>
#
# Credentials (checked in order):
#   1. MATRIX_ACCESS_TOKEN env var
#   2. accessToken from openclaw.json (auto-detected path)
#
# Matrix server: HICLAW_MATRIX_SERVER env var (required)
#
# Output: mxc:// URI on success
# Exit:   0 on success, 1 on failure

set -euo pipefail

# ============================================================
# Arguments
# ============================================================
if [ $# -lt 2 ]; then
    echo "Usage: send-file.sh <file_path> <room_id>" >&2
    exit 1
fi

FILE_PATH="$1"
ROOM_ID="$2"

# ============================================================
# Validate file
# ============================================================
if [ ! -f "${FILE_PATH}" ]; then
    echo "ERROR: File not found: ${FILE_PATH}" >&2
    exit 1
fi

if [ ! -r "${FILE_PATH}" ]; then
    echo "ERROR: File not readable: ${FILE_PATH}" >&2
    exit 1
fi

FILENAME=$(basename "${FILE_PATH}")
FILE_SIZE=$(stat -c%s "${FILE_PATH}" 2>/dev/null || stat -f%z "${FILE_PATH}" 2>/dev/null || echo 0)

# ============================================================
# Resolve Matrix server URL
# ============================================================
MATRIX_URL="${HICLAW_MATRIX_SERVER:-}"
if [ -z "${MATRIX_URL}" ]; then
    echo "ERROR: HICLAW_MATRIX_SERVER environment variable not set" >&2
    exit 1
fi

# ============================================================
# Resolve access token
# ============================================================
TOKEN="${MATRIX_ACCESS_TOKEN:-}"

if [ -z "${TOKEN}" ]; then
    # Try to find openclaw.json (Worker environments)
    OPENCLAW_JSON=""

    # CoPaw Worker: ~/.copaw-worker/<name>/openclaw.json
    if [ -z "${OPENCLAW_JSON}" ]; then
        for f in ~/.copaw-worker/*/openclaw.json; do
            [ -f "$f" ] && OPENCLAW_JSON="$f" && break
        done
    fi

    # OpenClaw Worker: ~/openclaw.json or ~/.openclaw/openclaw.json
    [ -z "${OPENCLAW_JSON}" ] && [ -f ~/openclaw.json ] && OPENCLAW_JSON=~/openclaw.json
    [ -z "${OPENCLAW_JSON}" ] && [ -f ~/.openclaw/openclaw.json ] && OPENCLAW_JSON=~/.openclaw/openclaw.json

    if [ -n "${OPENCLAW_JSON}" ]; then
        TOKEN=$(jq -r '.channels.matrix.accessToken // empty' "${OPENCLAW_JSON}" 2>/dev/null)
    fi
fi

# Manager environment: try MANAGER_MATRIX_TOKEN
if [ -z "${TOKEN}" ]; then
    TOKEN="${MANAGER_MATRIX_TOKEN:-}"
fi

if [ -z "${TOKEN}" ]; then
    echo "ERROR: No Matrix access token found. Set MATRIX_ACCESS_TOKEN or ensure openclaw.json is available." >&2
    exit 1
fi

# ============================================================
# Detect MIME type
# ============================================================
MIME_TYPE=$(file --brief --mime-type "${FILE_PATH}" 2>/dev/null || echo "application/octet-stream")

# ============================================================
# Step 1: Upload file to Matrix media repo
# ============================================================
UPLOAD_RESP=$(curl -sf -X POST \
    "${MATRIX_URL}/_matrix/media/v3/upload?filename=$(printf '%s' "${FILENAME}" | jq -sRr @uri)" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Content-Type: ${MIME_TYPE}" \
    --data-binary "@${FILE_PATH}" 2>&1) || {
    echo "ERROR: File upload failed" >&2
    echo "${UPLOAD_RESP}" >&2
    exit 1
}

MXC_URI=$(echo "${UPLOAD_RESP}" | jq -r '.content_uri // empty' 2>/dev/null)
if [ -z "${MXC_URI}" ]; then
    echo "ERROR: Upload succeeded but no content_uri in response" >&2
    echo "${UPLOAD_RESP}" >&2
    exit 1
fi

# ============================================================
# Step 2: Send m.file event to room
# ============================================================
TXN_ID="sf-$(date +%s%N)"
ROOM_ENC="${ROOM_ID//!/%21}"

SEND_RESP=$(curl -sf -X PUT \
    "${MATRIX_URL}/_matrix/client/v3/rooms/${ROOM_ENC}/send/m.room.message/${TXN_ID}" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "$(jq -n \
        --arg body "${FILENAME}" \
        --arg url "${MXC_URI}" \
        --arg mime "${MIME_TYPE}" \
        --argjson size "${FILE_SIZE}" \
        '{
            msgtype: "m.file",
            body: $body,
            url: $url,
            info: { mimetype: $mime, size: $size }
        }')" 2>&1) || {
    echo "ERROR: Failed to send file event to room" >&2
    echo "${SEND_RESP}" >&2
    exit 1
}

EVENT_ID=$(echo "${SEND_RESP}" | jq -r '.event_id // empty' 2>/dev/null)
if [ -z "${EVENT_ID}" ]; then
    echo "ERROR: Send succeeded but no event_id in response" >&2
    echo "${SEND_RESP}" >&2
    exit 1
fi

# ============================================================
# Success
# ============================================================
echo "${MXC_URI}"
