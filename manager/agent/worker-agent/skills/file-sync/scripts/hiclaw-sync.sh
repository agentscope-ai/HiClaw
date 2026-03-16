#!/bin/sh
# hiclaw-sync.sh - Pull latest config from centralized storage
# Called by the Worker agent when Manager notifies of config updates.
# Uses /root/hiclaw-fs/ layout — same absolute path as the Manager's MinIO mirror.

WORKER_NAME="${HICLAW_WORKER_NAME:?HICLAW_WORKER_NAME is required}"
HICLAW_ROOT="/root/hiclaw-fs"
WORKSPACE="${HICLAW_ROOT}/agents/${WORKER_NAME}"

mc mirror "hiclaw/hiclaw-storage/agents/${WORKER_NAME}/" "${WORKSPACE}/" --overwrite \
    --exclude ".openclaw/matrix/**" --exclude ".openclaw/canvas/**" 2>&1
mc mirror "hiclaw/hiclaw-storage/shared/" "${HICLAW_ROOT}/shared/" --overwrite 2>/dev/null || true

# Restore +x on scripts (MinIO does not preserve Unix permission bits)
find "${WORKSPACE}/skills" -name '*.sh' -exec chmod +x {} + 2>/dev/null || true

echo "Config sync completed at $(date)"
