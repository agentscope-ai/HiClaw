#!/bin/sh
# hiclaw-sync.sh - Pull latest config from centralized storage
# Called by the Worker agent when coordinator notifies of config updates.
# Uses /root/hiclaw-fs/ layout — same absolute path as the Manager's MinIO mirror.

# Bootstrap env: provides HICLAW_STORAGE_PREFIX and ensure_mc_credentials
if [ -f /opt/hiclaw/scripts/lib/hiclaw-env.sh ]; then
    . /opt/hiclaw/scripts/lib/hiclaw-env.sh
else
    . /opt/hiclaw/scripts/lib/oss-credentials.sh 2>/dev/null || true
    ensure_mc_credentials 2>/dev/null || true
    HICLAW_FS_BUCKET="${HICLAW_FS_BUCKET:-hiclaw-storage}"
    HICLAW_STORAGE_PREFIX="${HICLAW_STORAGE_PREFIX:-hiclaw/${HICLAW_FS_BUCKET}}"
fi

# Merge helper for openclaw.json (remote base + local Worker additions)
. /opt/hiclaw/scripts/lib/merge-openclaw-config.sh

WORKER_NAME="${HICLAW_WORKER_NAME:?HICLAW_WORKER_NAME is required}"
HICLAW_ROOT="/root/hiclaw-fs"
WORKSPACE="${HICLAW_ROOT}/agents/${WORKER_NAME}"

ensure_mc_credentials 2>/dev/null || true

# Save local openclaw.json before mirror overwrites it
LOCAL_OPENCLAW="${WORKSPACE}/openclaw.json"
SAVED_LOCAL="/tmp/openclaw-local-sync.json"
if [ -f "${LOCAL_OPENCLAW}" ]; then
    cp "${LOCAL_OPENCLAW}" "${SAVED_LOCAL}"
fi

mc mirror "${HICLAW_STORAGE_PREFIX}/agents/${WORKER_NAME}/" "${WORKSPACE}/" --overwrite \
    --exclude ".openclaw/matrix/**" --exclude ".openclaw/canvas/**" 2>&1
mc mirror "${HICLAW_STORAGE_PREFIX}/shared/" "${HICLAW_ROOT}/shared/" --overwrite 2>/dev/null || true

# Update pull marker so the local→remote sync loop doesn't push back freshly-pulled files
touch "${WORKSPACE}/.last-pull"

# Merge openclaw.json: remote (MinIO, now in workspace) as base + local Worker additions
if [ -f "${SAVED_LOCAL}" ] && [ -f "${LOCAL_OPENCLAW}" ]; then
    merge_openclaw_config "${LOCAL_OPENCLAW}" "${SAVED_LOCAL}"
    rm -f "${SAVED_LOCAL}"
fi

# Restore +x on scripts (MinIO does not preserve Unix permission bits)
find "${WORKSPACE}/skills" -name '*.sh' -exec chmod +x {} + 2>/dev/null || true

echo "Config sync completed at $(date)"
