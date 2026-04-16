#!/bin/bash
# sync-workspace.sh — Pull target Workers' workspace files from centralized storage.
# Used by DebugWorker to get latest state of target Workers.
#
# Usage:
#   bash ~/skills/debug-analysis/scripts/sync-workspace.sh --all
#   bash ~/skills/debug-analysis/scripts/sync-workspace.sh --worker <name>

set -e

WORKSPACE="${HOME}"
DEBUG_CONFIG="${WORKSPACE}/debug-config.json"
DEBUG_TARGETS_DIR="${WORKSPACE}/debug-targets"
STORAGE_PREFIX="${HICLAW_STORAGE_PREFIX:-}"

log() {
    echo "[debug-sync $(date '+%H:%M:%S')] $1"
}

if [ ! -f "${DEBUG_CONFIG}" ]; then
    echo "ERROR: debug-config.json not found at ${DEBUG_CONFIG}"
    echo "This script is only available on DebugWorkers."
    exit 1
fi

if [ -z "${STORAGE_PREFIX}" ]; then
    echo "ERROR: HICLAW_STORAGE_PREFIX is not set"
    exit 1
fi

TARGETS=$(jq -r '.targets[]' "${DEBUG_CONFIG}")

sync_target() {
    local target="$1"
    log "Syncing workspace for target: ${target}"
    mkdir -p "${DEBUG_TARGETS_DIR}/${target}"
    if mc mirror "${STORAGE_PREFIX}/agents/${target}/" \
        "${DEBUG_TARGETS_DIR}/${target}/" --overwrite 2>&1; then
        log "Synced: ${target}"
    else
        log "WARNING: Failed to sync ${target} (may not have read access)"
    fi
}

case "${1:-}" in
    --all)
        for target in ${TARGETS}; do
            sync_target "${target}"
        done
        log "All targets synced"
        ;;
    --worker)
        if [ -z "${2:-}" ]; then
            echo "Usage: $0 --worker <name>"
            exit 1
        fi
        # Verify target is in our allowed list
        if echo "${TARGETS}" | grep -qx "$2"; then
            sync_target "$2"
        else
            echo "ERROR: '$2' is not in the target list. Available targets:"
            echo "${TARGETS}" | sed 's/^/  - /'
            exit 1
        fi
        ;;
    *)
        echo "Usage:"
        echo "  $0 --all              Sync all target workspaces"
        echo "  $0 --worker <name>    Sync a specific target workspace"
        echo ""
        echo "Available targets:"
        echo "${TARGETS}" | sed 's/^/  - /'
        ;;
esac
