#!/bin/bash
# start-mc-mirror.sh - Initialize MinIO storage and start bidirectional file sync
#
# Manager's own workspace (/root/manager-workspace/) is LOCAL ONLY and not synced to MinIO.
# MinIO only stores shared data and worker configs (/root/hiclaw-fs/).

source /opt/hiclaw/scripts/lib/base.sh
waitForService "MinIO" "127.0.0.1" 9000

# Configure mc alias (local access, not through Higress)
mc alias set hiclaw http://127.0.0.1:9000 "${HICLAW_MINIO_USER:-${HICLAW_ADMIN_USER:-admin}}" "${HICLAW_MINIO_PASSWORD:-${HICLAW_ADMIN_PASSWORD:-admin}}"

# Create default bucket
mc mb hiclaw/hiclaw-storage --ignore-existing

# Initialize placeholder directories for shared data and worker artifacts
for dir in shared/knowledge shared/tasks workers; do
    echo "" | mc pipe "hiclaw/hiclaw-storage/${dir}/.gitkeep" 2>/dev/null || true
done

# Create local mirror directory (for shared + worker data only)
# Use absolute path because HOME may point to manager-workspace
HICLAW_FS_ROOT="/root/hiclaw-fs"
mkdir -p "${HICLAW_FS_ROOT}"

# Initial full sync to local (workers + shared)
mc mirror hiclaw/hiclaw-storage/ "${HICLAW_FS_ROOT}/" --overwrite

# Signal that initialization is complete
touch "${HICLAW_FS_ROOT}/.initialized"

log "MinIO storage initialized and synced to ${HICLAW_FS_ROOT}/"

# Store PID file for cleanup on restart
PID_FILE="/var/run/mc-mirror-watch.pid"

# Clean up any existing watch process from previous runs
if [ -f "${PID_FILE}" ]; then
    OLD_PID=$(cat "${PID_FILE}" 2>/dev/null)
    if [ -n "${OLD_PID}" ] && kill -0 "${OLD_PID}" 2>/dev/null; then
        log "Cleaning up previous watch process (PID: ${OLD_PID})"
        kill "${OLD_PID}" 2>/dev/null || true
        # Wait for process to terminate
        for i in $(seq 1 10); do
            if ! kill -0 "${OLD_PID}" 2>/dev/null; then
                break
            fi
            sleep 1
        done
        # Force kill if still running
        if kill -0 "${OLD_PID}" 2>/dev/null; then
            kill -9 "${OLD_PID}" 2>/dev/null || true
        fi
    fi
    rm -f "${PID_FILE}"
fi

# Start bidirectional sync (shared + worker data only — manager workspace excluded)
# Local -> Remote: real-time watch (filesystem notify)
mc mirror --watch "${HICLAW_FS_ROOT}/" hiclaw/hiclaw-storage/ --overwrite &
LOCAL_TO_REMOTE_PID=$!

# Store PID for cleanup
echo "${LOCAL_TO_REMOTE_PID}" > "${PID_FILE}"

log "Local->Remote sync started (PID: ${LOCAL_TO_REMOTE_PID})"

# Cleanup function to terminate background process on exit
_cleanup() {
    log "Stopping mc mirror watch (PID: ${LOCAL_TO_REMOTE_PID})..."
    if [ -n "${LOCAL_TO_REMOTE_PID}" ] && kill -0 "${LOCAL_TO_REMOTE_PID}" 2>/dev/null; then
        kill "${LOCAL_TO_REMOTE_PID}" 2>/dev/null || true
        # Wait for graceful termination
        for i in $(seq 1 5); do
            if ! kill -0 "${LOCAL_TO_REMOTE_PID}" 2>/dev/null; then
                log "mc mirror watch terminated gracefully"
                break
            fi
            sleep 1
        done
        # Force kill if still running
        if kill -0 "${LOCAL_TO_REMOTE_PID}" 2>/dev/null; then
            kill -9 "${LOCAL_TO_REMOTE_PID}" 2>/dev/null || true
            log "mc mirror watch force terminated"
        fi
    fi
    rm -f "${PID_FILE}"
    exit 0
}

# Register cleanup function for common exit signals
trap _cleanup EXIT INT TERM HUP QUIT

# Remote -> Local: periodic pull every 5 minutes (aligned with heartbeat)
while true; do
    sleep 300
    mc mirror hiclaw/hiclaw-storage/ "${HICLAW_FS_ROOT}/" --overwrite --newer-than "5m" 2>/dev/null || true
done
