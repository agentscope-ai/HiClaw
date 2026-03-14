#!/bin/bash
# worker-entrypoint.sh - Worker Agent startup
# Pulls config from centralized file system, starts file sync, launches OpenClaw.
#
# HOME is set to the Worker workspace so all agent-generated files are synced to MinIO:
#   ~/ = /root/hiclaw-fs/agents/<WORKER_NAME>/  (SOUL.md, openclaw.json, memory/)
#   /root/hiclaw-fs/shared/                     = Shared tasks, knowledge, collaboration data

set -e

WORKER_NAME="${HICLAW_WORKER_NAME:?HICLAW_WORKER_NAME is required}"
FS_ENDPOINT="${HICLAW_FS_ENDPOINT:?HICLAW_FS_ENDPOINT is required}"
FS_ACCESS_KEY="${HICLAW_FS_ACCESS_KEY:?HICLAW_FS_ACCESS_KEY is required}"
FS_SECRET_KEY="${HICLAW_FS_SECRET_KEY:?HICLAW_FS_SECRET_KEY is required}"

log() {
    echo "[hiclaw-worker $(date '+%Y-%m-%d %H:%M:%S')] $1"
}

# ============================================================
# Step 0: Set timezone from TZ env var
# ============================================================
if [ -n "${TZ}" ] && [ -f "/usr/share/zoneinfo/${TZ}" ]; then
    ln -sf "/usr/share/zoneinfo/${TZ}" /etc/localtime
    echo "${TZ}" > /etc/timezone
    log "Timezone set to ${TZ}"
fi

# Use absolute path because HOME is set to the workspace directory via docker run
HICLAW_ROOT="/root/hiclaw-fs"
WORKSPACE="${HICLAW_ROOT}/agents/${WORKER_NAME}"

# ============================================================
# Step 1: Configure mc alias for centralized file system
# ============================================================
log "Configuring mc alias..."
mc alias set hiclaw "${FS_ENDPOINT}" "${FS_ACCESS_KEY}" "${FS_SECRET_KEY}"

# ============================================================
# Step 2: Pull Worker config and shared data from centralized storage
# ============================================================
mkdir -p "${WORKSPACE}" "${HICLAW_ROOT}/shared"

log "Pulling Worker config from centralized storage..."
mc mirror "hiclaw/hiclaw-storage/agents/${WORKER_NAME}/" "${WORKSPACE}/" --overwrite
mc mirror "hiclaw/hiclaw-storage/shared/" "${HICLAW_ROOT}/shared/" --overwrite 2>/dev/null || true

# Verify essential files exist, retry if sync is still in progress
RETRY=0
while [ ! -f "${WORKSPACE}/openclaw.json" ] || [ ! -f "${WORKSPACE}/SOUL.md" ] \
      || [ ! -f "${WORKSPACE}/AGENTS.md" ]; do
    RETRY=$((RETRY + 1))
    if [ "${RETRY}" -gt 6 ]; then
        log "ERROR: openclaw.json, SOUL.md or AGENTS.md not found after retries. Manager may not have created this Worker's config yet."
        exit 1
    fi
    log "Waiting for config files to appear in MinIO (attempt ${RETRY}/6)..."
    sleep 5
    mc mirror "hiclaw/hiclaw-storage/agents/${WORKER_NAME}/" "${WORKSPACE}/" --overwrite 2>/dev/null || true
done

# HOME is already set to WORKSPACE via docker run -e HOME=...
# Symlink to default OpenClaw config path so CLI commands find the config
mkdir -p "${HOME}/.openclaw"
ln -sf "${WORKSPACE}/openclaw.json" "${HOME}/.openclaw/openclaw.json"

# Create symlink for skills CLI: ~/.agents/skills -> ~/skills
# This makes `skills add -g` install skills directly into ~/skills/ (same as file-sync)
# Skills in ~/skills/ will be synced to MinIO and persist across container restarts
mkdir -p "${HOME}/skills"
mkdir -p "${HOME}/.agents"
# Clean up circular symlink from previous buggy ln -sf (which followed
# the existing symlink-to-directory and created skills/skills -> skills inside it).
[ -L "${HOME}/skills/skills" ] && rm -f "${HOME}/skills/skills"
# Use -n (--no-dereference) so ln replaces an existing symlink-to-directory
# instead of creating a nested symlink inside the target directory.
ln -sfn "${HOME}/skills" "${HOME}/.agents/skills"

log "Worker config pulled successfully"

# Restore skills from MinIO if skills directory is empty but skills-lock.json exists
if [ -f "${WORKSPACE}/skills-lock.json" ] && [ -z "$(ls -A ${WORKSPACE}/skills 2>/dev/null | grep -v file-sync)" ]; then
    log "Found skills-lock.json but skills directory is empty, restoring skills..."
    cd "${WORKSPACE}" && skills experimental_install -y 2>/dev/null || log "Warning: skills restore failed, will need to reinstall"
fi

# Ensure hiclaw-sync symlink is functional (wrapper script calls workspace path)
ln -sf "${WORKSPACE}/skills/file-sync/scripts/hiclaw-sync.sh" /usr/local/bin/hiclaw-sync 2>/dev/null || true

log "HOME set to ${HOME} (workspace files will be synced to MinIO)"

# ============================================================
# Step 3: Start file sync
# ============================================================

# Local -> Remote: change-triggered sync (avoids mc mirror --watch TOCTOU crash bug)
# Note: mc mirror --watch has a bug where it crashes when source files are deleted
# during atomic operations (e.g., npm install, skills add). This approach:
# - Uses find to detect recent file changes (lightweight, local filesystem only)
# - Only runs mc mirror when changes are detected (avoids unnecessary network IO)
# - mc mirror itself only transfers changed files (incremental)
(
    while true; do
        # Check for files modified in the last 10 seconds
        CHANGED=$(find "${WORKSPACE}/" -type f -newermt "10 seconds ago" 2>/dev/null | head -1)
        if [ -n "${CHANGED}" ]; then
            if ! mc mirror "${WORKSPACE}/" "hiclaw/hiclaw-storage/agents/${WORKER_NAME}/" --overwrite \
                --exclude "openclaw.json" --exclude "AGENTS.md" --exclude "SOUL.md" \
                --exclude "mcporter-servers.json" --exclude ".agents/**" \
                --exclude ".cache/**" --exclude ".npm/**" \
                --exclude ".local/**" --exclude ".mc/**" 2>&1; then
                log "WARNING: Local->Remote sync failed"
            fi
        fi
        sleep 5
    done
) &
log "Local->Remote change-triggered sync started (PID: $!)"

# Remote -> Local: periodic pull (configs from Manager + shared data)
# On-demand pull via file-sync skill when Manager notifies
(
    while true; do
        sleep 300
        mc mirror "hiclaw/hiclaw-storage/agents/${WORKER_NAME}/" "${WORKSPACE}/" --overwrite --newer-than "5m" 2>/dev/null || true
        mc mirror "hiclaw/hiclaw-storage/shared/" "${HICLAW_ROOT}/shared/" --overwrite --newer-than "5m" 2>/dev/null || true
    done
) &
log "Remote->Local periodic sync started (every 5m, PID: $!)"

# ============================================================
# Step 4: Configure mcporter (MCP tool CLI)
# ============================================================
if [ -f "${WORKSPACE}/mcporter-servers.json" ]; then
    log "Configuring mcporter with MCP Server endpoints..."
    export MCPORTER_CONFIG="${WORKSPACE}/mcporter-servers.json"
fi

# ============================================================
# Step 5: Launch OpenClaw Worker Agent
# ============================================================
log "Starting Worker Agent: ${WORKER_NAME}"
export OPENCLAW_CONFIG_PATH="${WORKSPACE}/openclaw.json"
cd "${WORKSPACE}"
exec openclaw gateway run --verbose --force
