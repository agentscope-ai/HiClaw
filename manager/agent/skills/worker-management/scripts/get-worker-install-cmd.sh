#!/bin/bash
# get-worker-install-cmd.sh - Output the install/start command for a remote Worker
#
# Reads workers-registry.json and worker credentials to build the command
# that the admin needs to run on the target machine.
#
# Usage:
#   get-worker-install-cmd.sh --worker <NAME>
#
# Output: JSON with install_cmd field on success, error JSON on failure.

set -euo pipefail

source /opt/hiclaw/scripts/lib/base.sh

WORKER_NAME=""

while [ $# -gt 0 ]; do
    case "$1" in
        --worker) WORKER_NAME="$2"; shift 2 ;;
        *) echo '{"error": "Unknown option: '"$1"'"}'; exit 1 ;;
    esac
done

if [ -z "${WORKER_NAME}" ]; then
    echo '{"error": "Usage: get-worker-install-cmd.sh --worker <NAME>"}'
    exit 1
fi

REGISTRY_FILE="${HOME}/workers-registry.json"

if [ ! -f "${REGISTRY_FILE}" ]; then
    echo '{"error": "workers-registry.json not found"}'
    exit 1
fi

# Check worker exists in registry
if ! jq -e --arg w "${WORKER_NAME}" '.workers[$w]' "${REGISTRY_FILE}" > /dev/null 2>&1; then
    echo '{"error": "Worker '"${WORKER_NAME}"' not found in registry"}'
    exit 1
fi

RUNTIME=$(jq -r --arg w "${WORKER_NAME}" '.workers[$w].runtime // "openclaw"' "${REGISTRY_FILE}")
DEPLOYMENT=$(jq -r --arg w "${WORKER_NAME}" '.workers[$w].deployment // "local"' "${REGISTRY_FILE}")

# Load credentials
CREDS_FILE="/data/worker-creds/${WORKER_NAME}.env"
if [ ! -f "${CREDS_FILE}" ]; then
    echo '{"error": "Credentials file not found: '"${CREDS_FILE}"'"}'
    exit 1
fi
source "${CREDS_FILE}"

# Build the install command
FS_DOMAIN="${HICLAW_FS_DOMAIN:-fs-local.hiclaw.io}"
FS_EXTERNAL_PORT="${HICLAW_PORT_GATEWAY:-18080}"
FS_EXTERNAL_ENDPOINT="http://${FS_DOMAIN}:${FS_EXTERNAL_PORT}"
FS_INTERNAL_ENDPOINT="http://${FS_DOMAIN%%:*}:9000"

if [ "${RUNTIME}" = "copaw" ]; then
    INSTALL_CMD="pip install -i https://mirrors.aliyun.com/pypi/simple/ copaw-worker && copaw-worker --name ${WORKER_NAME} --fs ${FS_EXTERNAL_ENDPOINT} --fs-key ${WORKER_NAME} --fs-secret ${WORKER_MINIO_PASSWORD} --console-port 8088"
else
    INSTALL_CMD="bash hiclaw-install.sh worker --name ${WORKER_NAME} --fs ${FS_INTERNAL_ENDPOINT} --fs-key ${WORKER_NAME} --fs-secret ${WORKER_MINIO_PASSWORD}"
fi

jq -n \
    --arg worker "${WORKER_NAME}" \
    --arg runtime "${RUNTIME}" \
    --arg deployment "${DEPLOYMENT}" \
    --arg install_cmd "${INSTALL_CMD}" \
    '{
        worker: $worker,
        runtime: $runtime,
        deployment: $deployment,
        install_cmd: $install_cmd
    }'
