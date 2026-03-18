#!/bin/bash
# hiclaw-import.sh - Import a Worker package into HiClaw
#
# Imports a Worker package (ZIP file or URL) into HiClaw as a fully managed Worker.
# Use cases: migrating a standalone OpenClaw, importing a pre-built Worker template,
# or deploying a community-shared Worker configuration.
#
# The package can include a Dockerfile for custom image builds, or omit it to use
# the standard HiClaw Worker image.
#
# Usage:
#   ./hiclaw-import.sh --zip <path-or-url> [options]
#
# Options:
#   --zip <path|url>      Worker package ZIP file path or download URL (required)
#   --name <name>         Worker name (default: from manifest)
#   --proxy <url>         HTTP proxy for worker runtime (e.g., http://proxy:port)
#   --no-proxy <domains>  Additional no-proxy domains
#   --env-file <path>     HiClaw env file (default: ~/hiclaw-manager.env)
#   --base-image <image>  Override base image for Dockerfile build
#   --skip-build          Skip Docker image build (use existing image)
#   --yes                 Skip interactive confirmations
#
# Environment variables (for automation):
#   HICLAW_IMPORT_ZIP            Path or URL to Worker package ZIP
#   HICLAW_IMPORT_WORKER_NAME   Worker name override
#   HICLAW_IMPORT_PROXY         HTTP proxy URL
#   HICLAW_IMPORT_NO_PROXY      Additional no-proxy domains
#   HICLAW_NON_INTERACTIVE       Skip all prompts (same as --yes)

set -e

# ============================================================
# Utility functions
# ============================================================

log() {
    echo -e "\033[36m[HiClaw Import]\033[0m $1"
}

warn() {
    echo -e "\033[33m[HiClaw Import WARNING]\033[0m $1"
}

error() {
    echo -e "\033[31m[HiClaw Import ERROR]\033[0m $1" >&2
    exit 1
}

# Generate random hex string
generate_key() {
    local len="${1:-32}"
    openssl rand -hex "${len}" 2>/dev/null || head -c "${len}" /dev/urandom | xxd -p | tr -d '\n' | head -c "$((len * 2))"
}

# Execute command inside the Manager container
mgr_exec() {
    ${CONTAINER_CMD} exec hiclaw-manager "$@"
}

# Execute bash command inside the Manager container
mgr_bash() {
    ${CONTAINER_CMD} exec hiclaw-manager bash -c "$1"
}

# Pipe stdin into Manager container
mgr_pipe() {
    ${CONTAINER_CMD} exec -i hiclaw-manager sh -c "$1"
}

# ============================================================
# Timezone and language detection (same pattern as hiclaw-install.sh)
# ============================================================

detect_timezone() {
    local tz=""
    if [ -f /etc/timezone ]; then
        tz=$(cat /etc/timezone 2>/dev/null | tr -d '[:space:]')
    fi
    if [ -z "${tz}" ] && [ -L /etc/localtime ]; then
        tz=$(ls -l /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||')
    fi
    if [ -z "${tz}" ]; then
        tz=$(timedatectl show --value -p Timezone 2>/dev/null || true)
    fi
    echo "${tz:-Asia/Shanghai}"
}

HICLAW_TIMEZONE="${HICLAW_TIMEZONE:-$(detect_timezone)}"

detect_language() {
    case "${HICLAW_TIMEZONE}" in
        Asia/Shanghai|Asia/Chongqing|Asia/Harbin|Asia/Urumqi|Asia/Taipei|Asia/Hong_Kong|Asia/Macau)
            echo "zh" ;;
        *) echo "en" ;;
    esac
}

HICLAW_LANGUAGE="${HICLAW_LANGUAGE:-$(detect_language)}"

# ============================================================
# i18n message dictionary
# ============================================================

msg() {
    local key="$1"
    shift
    local lang="${HICLAW_LANGUAGE:-en}"
    local text=""
    case "${key}.${lang}" in
        "title.zh") text="=== 导入 Worker 到 HiClaw ===" ;;
        "title.en") text="=== Import Worker into HiClaw ===" ;;
        "preflight.zh") text="预检查..." ;;
        "preflight.en") text="Pre-flight checks..." ;;
        "preflight.runtime.zh") text="检测容器运行时..." ;;
        "preflight.runtime.en") text="Detecting container runtime..." ;;
        "preflight.runtime.found.zh") text="容器运行时: %s" ;;
        "preflight.runtime.found.en") text="Container runtime: %s" ;;
        "preflight.runtime.none.zh") text="未找到 Docker 或 Podman。请先安装容器运行时。" ;;
        "preflight.runtime.none.en") text="Docker or Podman not found. Please install a container runtime first." ;;
        "preflight.env.zh") text="读取 HiClaw 配置: %s" ;;
        "preflight.env.en") text="Reading HiClaw config: %s" ;;
        "preflight.env.missing.zh") text="HiClaw 环境文件未找到: %s\n请先安装 HiClaw Manager，或使用 --env-file 指定路径。" ;;
        "preflight.env.missing.en") text="HiClaw env file not found: %s\nPlease install HiClaw Manager first, or use --env-file to specify the path." ;;
        "preflight.manager.zh") text="检查 Manager 容器..." ;;
        "preflight.manager.en") text="Checking Manager container..." ;;
        "preflight.manager.not_running.zh") text="HiClaw Manager 容器未运行。请先启动 Manager。" ;;
        "preflight.manager.not_running.en") text="HiClaw Manager container is not running. Please start the Manager first." ;;
        "preflight.download.zh") text="下载 Worker 包: %s" ;;
        "preflight.download.en") text="Downloading Worker package: %s" ;;
        "preflight.download.fail.zh") text="下载失败: %s" ;;
        "preflight.download.fail.en") text="Download failed: %s" ;;
        "preflight.zip.zh") text="验证 Worker 包: %s" ;;
        "preflight.zip.en") text="Validating Worker package: %s" ;;
        "preflight.zip.missing.zh") text="ZIP 文件未找到: %s" ;;
        "preflight.zip.missing.en") text="ZIP file not found: %s" ;;
        "preflight.zip.invalid.zh") text="无效的 Worker 包: 缺少 manifest.json" ;;
        "preflight.zip.invalid.en") text="Invalid Worker package: missing manifest.json" ;;
        "summary.title.zh") text="--- 导入摘要 ---" ;;
        "summary.title.en") text="--- Import Summary ---" ;;
        "summary.name.zh") text="  Worker 名称: %s" ;;
        "summary.name.en") text="  Worker name: %s" ;;
        "summary.source.zh") text="  来源: %s" ;;
        "summary.source.en") text="  Source: %s" ;;
        "summary.image.zh") text="  镜像: %s" ;;
        "summary.image.en") text="  Image: %s" ;;
        "summary.image.standard.zh") text="  镜像: 标准 Worker 镜像（包中无 Dockerfile）" ;;
        "summary.image.standard.en") text="  Image: Standard Worker image (no Dockerfile in package)" ;;
        "summary.apt.zh") text="  APT 包: %s" ;;
        "summary.apt.en") text="  APT packages: %s" ;;
        "summary.pip.zh") text="  pip 包: %s" ;;
        "summary.pip.en") text="  pip packages: %s" ;;
        "summary.npm.zh") text="  npm 包: %s" ;;
        "summary.npm.en") text="  npm packages: %s" ;;
        "summary.proxy.zh") text="  运行时代理: %s" ;;
        "summary.proxy.en") text="  Runtime proxy: %s" ;;
        "summary.confirm.zh") text="是否继续? [Y/n]" ;;
        "summary.confirm.en") text="Continue? [Y/n]" ;;
        "step.build.zh") text="步骤 1/7: 构建自定义 Worker 镜像..." ;;
        "step.build.en") text="Step 1/7: Building custom Worker image..." ;;
        "step.build.skip.zh") text="步骤 1/7: 跳过镜像构建" ;;
        "step.build.skip.en") text="Step 1/7: Skipping image build" ;;
        "step.build.standard.zh") text="步骤 1/7: 包中无 Dockerfile，使用标准 Worker 镜像" ;;
        "step.build.standard.en") text="Step 1/7: No Dockerfile in package, using standard Worker image" ;;
        "step.build.done.zh") text="镜像构建完成: %s" ;;
        "step.build.done.en") text="Image built: %s" ;;
        "step.matrix.zh") text="步骤 2/7: 注册 Matrix 账号并创建 Room..." ;;
        "step.matrix.en") text="Step 2/7: Registering Matrix account and creating Room..." ;;
        "step.minio.zh") text="步骤 3/7: 创建 MinIO 用户..." ;;
        "step.minio.en") text="Step 3/7: Creating MinIO user..." ;;
        "step.gateway.zh") text="步骤 4/7: 配置 Higress Gateway..." ;;
        "step.gateway.en") text="Step 4/7: Configuring Higress Gateway..." ;;
        "step.config.zh") text="步骤 5/7: 生成配置并推送到 MinIO..." ;;
        "step.config.en") text="Step 5/7: Generating config and pushing to MinIO..." ;;
        "step.registry.zh") text="步骤 6/7: 更新 Manager 注册表..." ;;
        "step.registry.en") text="Step 6/7: Updating Manager registry..." ;;
        "step.start.zh") text="步骤 7/7: 通知 Manager 启动 Worker..." ;;
        "step.start.en") text="Step 7/7: Notifying Manager to start Worker..." ;;
        "done.title.zh") text="=== 导入完成 ===" ;;
        "done.title.en") text="=== Import Complete ===" ;;
        "done.worker.zh") text="  Worker: %s" ;;
        "done.worker.en") text="  Worker: %s" ;;
        "done.image.zh") text="  镜像: %s" ;;
        "done.image.en") text="  Image: %s" ;;
        "done.room.zh") text="  Matrix Room: %s" ;;
        "done.room.en") text="  Matrix Room: %s" ;;
        "done.hint.zh") text="已通知 Manager 启动 Worker。请在 Element Web 中查看 Worker 状态。" ;;
        "done.hint.en") text="Manager has been notified to start the Worker. Check Worker status in Element Web." ;;
        "error.abort.zh") text="导入已取消。" ;;
        "error.abort.en") text="Import aborted." ;;
        *) text="${key}" ;;
    esac
    # shellcheck disable=SC2059
    printf "${text}\n" "$@"
}

# ============================================================
# Parse arguments
# ============================================================

ZIP_FILE="${HICLAW_IMPORT_ZIP:-}"
WORKER_NAME="${HICLAW_IMPORT_WORKER_NAME:-}"
PROXY="${HICLAW_IMPORT_PROXY:-}"
EXTRA_NO_PROXY="${HICLAW_IMPORT_NO_PROXY:-}"
ENV_FILE="${HICLAW_ENV_FILE:-${HOME}/hiclaw-manager.env}"
BASE_IMAGE_OVERRIDE=""
SKIP_BUILD=false
AUTO_YES="${HICLAW_NON_INTERACTIVE:-0}"

while [ $# -gt 0 ]; do
    case "$1" in
        --zip)        ZIP_FILE="$2"; shift 2 ;;
        --name)       WORKER_NAME="$2"; shift 2 ;;
        --proxy)      PROXY="$2"; shift 2 ;;
        --no-proxy)   EXTRA_NO_PROXY="$2"; shift 2 ;;
        --env-file)   ENV_FILE="$2"; shift 2 ;;
        --base-image) BASE_IMAGE_OVERRIDE="$2"; shift 2 ;;
        --skip-build) SKIP_BUILD=true; shift ;;
        --yes)        AUTO_YES=1; shift ;;
        -h|--help)
            echo "Usage: $0 --zip <path-or-url> [--name <worker>] [--proxy <url>] [--env-file <path>] [--skip-build] [--yes]"
            exit 0 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

if [ -z "${ZIP_FILE}" ]; then
    echo "Usage: $0 --zip <path-or-url> [--name <worker>] [--proxy <url>] [--env-file <path>] [--skip-build] [--yes]"
    exit 1
fi

# ============================================================
# Title
# ============================================================
echo ""
msg "title"
echo ""

# ============================================================
# Step 0: Pre-flight checks
# ============================================================
msg "preflight"

# Detect container runtime
msg "preflight.runtime"
CONTAINER_CMD=""
if command -v docker &>/dev/null && docker info &>/dev/null 2>&1; then
    CONTAINER_CMD="docker"
elif command -v podman &>/dev/null && podman info &>/dev/null 2>&1; then
    CONTAINER_CMD="podman"
fi

if [ -z "${CONTAINER_CMD}" ]; then
    msg "preflight.runtime.none"
    exit 1
fi
msg "preflight.runtime.found" "${CONTAINER_CMD}"

# Read env file
msg "preflight.env" "${ENV_FILE}"
if [ ! -f "${ENV_FILE}" ]; then
    msg "preflight.env.missing" "${ENV_FILE}"
    exit 1
fi
# shellcheck disable=SC1090
source "${ENV_FILE}"

# Check Manager container is running
msg "preflight.manager"
if ! ${CONTAINER_CMD} ps --filter name=hiclaw-manager --format '{{.Names}}' 2>/dev/null | grep -q 'hiclaw-manager'; then
    msg "preflight.manager.not_running"
    exit 1
fi
log "  OK"

# Validate ZIP — support both local path and URL
# If ZIP_FILE looks like a URL, download it first
if [[ "${ZIP_FILE}" =~ ^https?:// ]]; then
    msg "preflight.download" "${ZIP_FILE}"
    DOWNLOADED_ZIP=$(mktemp /tmp/hiclaw-import-XXXXXX.zip)
    if ! curl -fSL -o "${DOWNLOADED_ZIP}" "${ZIP_FILE}" 2>/dev/null; then
        rm -f "${DOWNLOADED_ZIP}"
        msg "preflight.download.fail" "${ZIP_FILE}"
        exit 1
    fi
    ZIP_FILE="${DOWNLOADED_ZIP}"
    log "  Downloaded to: ${ZIP_FILE}"
fi

msg "preflight.zip" "${ZIP_FILE}"
if [ ! -f "${ZIP_FILE}" ]; then
    msg "preflight.zip.missing" "${ZIP_FILE}"
    exit 1
fi

# Extract ZIP to temp directory
TMP_DIR=$(mktemp -d /tmp/hiclaw-import-XXXXXX)
trap 'rm -rf "${TMP_DIR}"; rm -f "${DOWNLOADED_ZIP:-}"' EXIT

unzip -q "${ZIP_FILE}" -d "${TMP_DIR}"

if [ ! -f "${TMP_DIR}/manifest.json" ]; then
    msg "preflight.zip.invalid"
    exit 1
fi

# Read manifest
MANIFEST="${TMP_DIR}/manifest.json"
SUGGESTED_NAME=$(jq -r '.worker.suggested_name // "migrated-worker"' "${MANIFEST}")
SOURCE_HOST=$(jq -r '.source.hostname // "unknown"' "${MANIFEST}")
MANIFEST_BASE_IMAGE=$(jq -r '.worker.base_image // "hiclaw/worker-agent:latest"' "${MANIFEST}")
APT_DISPLAY=$(jq -r '.worker.apt_packages | join(", ")' "${MANIFEST}" 2>/dev/null || echo "none")
PIP_DISPLAY=$(jq -r '.worker.pip_packages | join(", ")' "${MANIFEST}" 2>/dev/null || echo "none")
NPM_DISPLAY=$(jq -r '.worker.npm_packages | join(", ")' "${MANIFEST}" 2>/dev/null || echo "none")

# Determine worker name
if [ -z "${WORKER_NAME}" ]; then
    WORKER_NAME="${SUGGESTED_NAME}"
fi
WORKER_NAME=$(echo "${WORKER_NAME}" | tr 'A-Z' 'a-z' | tr -cd 'a-z0-9-')

# Determine image tag — only custom if Dockerfile exists
HAS_DOCKERFILE=false
if [ -f "${TMP_DIR}/Dockerfile" ]; then
    HAS_DOCKERFILE=true
fi

if [ "${HAS_DOCKERFILE}" = true ]; then
    IMAGE_TAG="hiclaw/worker-imported-${WORKER_NAME}:latest"
else
    IMAGE_TAG=""  # will use standard worker image
fi

# Determine base image
BASE_IMAGE="${BASE_IMAGE_OVERRIDE:-${MANIFEST_BASE_IMAGE}}"

# Read HiClaw config values
MATRIX_DOMAIN="${HICLAW_MATRIX_DOMAIN:-matrix-local.hiclaw.io:8080}"
ADMIN_USER="${HICLAW_ADMIN_USER:-admin}"
ADMIN_PASSWORD="${HICLAW_ADMIN_PASSWORD:-}"
GATEWAY_PORT="${HICLAW_PORT_GATEWAY:-18080}"
REGISTRATION_TOKEN="${HICLAW_REGISTRATION_TOKEN:-}"
MANAGER_PASSWORD="${HICLAW_MANAGER_PASSWORD:-}"
STORAGE_PREFIX="hiclaw/hiclaw-storage"
MATRIX_SERVER="http://127.0.0.1:6167"

# ============================================================
# Show summary and confirm
# ============================================================
echo ""
msg "summary.title"
msg "summary.name" "${WORKER_NAME}"
msg "summary.source" "${SOURCE_HOST}"
if [ "${HAS_DOCKERFILE}" = true ]; then
    msg "summary.image" "${IMAGE_TAG}"
else
    msg "summary.image.standard"
fi
msg "summary.apt" "${APT_DISPLAY}"
msg "summary.pip" "${PIP_DISPLAY}"
msg "summary.npm" "${NPM_DISPLAY}"
if [ -n "${PROXY}" ]; then
    msg "summary.proxy" "${PROXY}"
fi
echo ""

if [ "${AUTO_YES}" != "1" ]; then
    read -r -p "$(msg "summary.confirm") " confirm
    case "${confirm}" in
        [nN]*) msg "error.abort"; exit 0 ;;
    esac
fi

# ============================================================
# Step 1: Build custom Worker image (only if Dockerfile exists)
# ============================================================
if [ "${SKIP_BUILD}" = true ]; then
    msg "step.build.skip"
elif [ "${HAS_DOCKERFILE}" = true ]; then
    msg "step.build"

    BUILD_ARGS="--build-arg BASE_IMAGE=${BASE_IMAGE}"

    # APT mirror for China
    case "${HICLAW_TIMEZONE}" in
        Asia/Shanghai|Asia/Chongqing|Asia/Harbin|Asia/Urumqi)
            BUILD_ARGS="${BUILD_ARGS} --build-arg APT_MIRROR=mirrors.aliyun.com" ;;
    esac

    if [ -n "${PROXY}" ]; then
        BUILD_ARGS="${BUILD_ARGS} --build-arg HTTP_PROXY=${PROXY} --build-arg HTTPS_PROXY=${PROXY}"
        BUILD_ARGS="${BUILD_ARGS} --build-arg http_proxy=${PROXY} --build-arg https_proxy=${PROXY}"
    fi

    # shellcheck disable=SC2086
    ${CONTAINER_CMD} build -t "${IMAGE_TAG}" ${BUILD_ARGS} -f "${TMP_DIR}/Dockerfile" "${TMP_DIR}"

    msg "step.build.done" "${IMAGE_TAG}"
else
    msg "step.build.standard"
    # No Dockerfile — use standard worker image, no custom image in registry
    IMAGE_TAG=""
fi

# ============================================================
# Step 2: Matrix account registration + Room creation
# ============================================================
msg "step.matrix"

# Generate credentials
WORKER_PASSWORD=$(generate_key 16)
WORKER_MINIO_PASSWORD=$(generate_key 24)
WORKER_GATEWAY_KEY=$(generate_key 32)

# Register Matrix account
REG_RESP=$(mgr_bash "curl -sf -X POST ${MATRIX_SERVER}/_matrix/client/v3/register \
    -H 'Content-Type: application/json' \
    -d '{\"username\":\"${WORKER_NAME}\",\"password\":\"${WORKER_PASSWORD}\",\"auth\":{\"type\":\"m.login.registration_token\",\"token\":\"${REGISTRATION_TOKEN}\"}}' 2>/dev/null" || true)

WORKER_MATRIX_TOKEN=$(echo "${REG_RESP}" | jq -r '.access_token // empty' 2>/dev/null)

if [ -z "${WORKER_MATRIX_TOKEN}" ]; then
    # Account may already exist, try login
    LOGIN_RESP=$(mgr_bash "curl -sf -X POST ${MATRIX_SERVER}/_matrix/client/v3/login \
        -H 'Content-Type: application/json' \
        -d '{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"${WORKER_NAME}\"},\"password\":\"${WORKER_PASSWORD}\"}' 2>/dev/null" || true)
    WORKER_MATRIX_TOKEN=$(echo "${LOGIN_RESP}" | jq -r '.access_token // empty' 2>/dev/null)

    if [ -z "${WORKER_MATRIX_TOKEN}" ]; then
        error "Failed to register or login Matrix account for ${WORKER_NAME}"
    fi
    log "  Logged into existing account: @${WORKER_NAME}:${MATRIX_DOMAIN}"
else
    log "  Registered: @${WORKER_NAME}:${MATRIX_DOMAIN}"
fi

# Get Manager token
MANAGER_MATRIX_TOKEN=$(mgr_bash "curl -sf -X POST ${MATRIX_SERVER}/_matrix/client/v3/login \
    -H 'Content-Type: application/json' \
    -d '{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"manager\"},\"password\":\"${MANAGER_PASSWORD}\"}' 2>/dev/null" | jq -r '.access_token // empty')

if [ -z "${MANAGER_MATRIX_TOKEN}" ]; then
    error "Failed to obtain Manager Matrix token"
fi

# Create 3-party room
MANAGER_MATRIX_ID="@manager:${MATRIX_DOMAIN}"
ADMIN_MATRIX_ID="@${ADMIN_USER}:${MATRIX_DOMAIN}"
WORKER_MATRIX_ID="@${WORKER_NAME}:${MATRIX_DOMAIN}"

# Build E2EE initial state if enabled
ROOM_E2EE=""
if [ "${HICLAW_MATRIX_E2EE:-0}" = "1" ] || [ "${HICLAW_MATRIX_E2EE:-}" = "true" ]; then
    ROOM_E2EE=',\"initial_state\":[{\"type\":\"m.room.encryption\",\"state_key\":\"\",\"content\":{\"algorithm\":\"m.megolm.v1.aes-sha2\"}}]'
fi

ROOM_RESP=$(mgr_bash "curl -sf -X POST ${MATRIX_SERVER}/_matrix/client/v3/createRoom \
    -H 'Authorization: Bearer ${MANAGER_MATRIX_TOKEN}' \
    -H 'Content-Type: application/json' \
    -d '{\"name\":\"Worker: ${WORKER_NAME}\",\"topic\":\"Communication channel for ${WORKER_NAME}\",\"invite\":[\"${ADMIN_MATRIX_ID}\",\"${WORKER_MATRIX_ID}\"],\"preset\":\"trusted_private_chat\",\"power_level_content_override\":{\"users\":{\"${MANAGER_MATRIX_ID}\":100,\"${ADMIN_MATRIX_ID}\":100,\"${WORKER_MATRIX_ID}\":0}}${ROOM_E2EE}}' 2>/dev/null")

ROOM_ID=$(echo "${ROOM_RESP}" | jq -r '.room_id // empty' 2>/dev/null)
if [ -z "${ROOM_ID}" ]; then
    error "Failed to create Matrix room: ${ROOM_RESP}"
fi
log "  Room created: ${ROOM_ID}"

# ============================================================
# Step 3: MinIO user creation
# ============================================================
msg "step.minio"

# Create MinIO user and policy
POLICY_JSON=$(cat <<POLICY
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket"],
      "Resource": ["arn:aws:s3:::hiclaw-storage"],
      "Condition": {
        "StringLike": {
          "s3:prefix": [
            "agents/${WORKER_NAME}", "agents/${WORKER_NAME}/*",
            "shared", "shared/*"
          ]
        }
      }
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
      "Resource": [
        "arn:aws:s3:::hiclaw-storage/agents/${WORKER_NAME}/*",
        "arn:aws:s3:::hiclaw-storage/shared/*"
      ]
    }
  ]
}
POLICY
)

POLICY_NAME="worker-${WORKER_NAME}"
echo "${POLICY_JSON}" | mgr_pipe "cat > /tmp/migrate-policy.json"
mgr_bash "mc admin user add hiclaw '${WORKER_NAME}' '${WORKER_MINIO_PASSWORD}' 2>/dev/null || true"
mgr_bash "mc admin policy remove hiclaw '${POLICY_NAME}' 2>/dev/null || true"
mgr_bash "mc admin policy create hiclaw '${POLICY_NAME}' /tmp/migrate-policy.json"
mgr_bash "mc admin policy attach hiclaw '${POLICY_NAME}' --user '${WORKER_NAME}'"
mgr_bash "rm -f /tmp/migrate-policy.json"
log "  MinIO user ${WORKER_NAME} created"

# ============================================================
# Step 4: Higress Gateway consumer + route authorization
# ============================================================
msg "step.gateway"

mgr_bash "
    source /opt/hiclaw/scripts/lib/hiclaw-env.sh
    source /opt/hiclaw/scripts/lib/gateway-api.sh
    gateway_ensure_session
    gateway_create_consumer 'worker-${WORKER_NAME}' '${WORKER_GATEWAY_KEY}' >/dev/null 2>&1
    gateway_authorize_routes 'worker-${WORKER_NAME}' >/dev/null 2>&1
    gateway_authorize_mcp 'worker-${WORKER_NAME}' '' >/dev/null 2>&1
"
log "  Gateway consumer and routes configured"

# ============================================================
# Step 5: Generate config and push to MinIO
# ============================================================
msg "step.config"

# 5a: Generate openclaw.json via Manager's script
mgr_bash "bash /opt/hiclaw/agent/skills/worker-management/scripts/generate-worker-config.sh \
    '${WORKER_NAME}' '${WORKER_MATRIX_TOKEN}' '${WORKER_GATEWAY_KEY}'"
log "  openclaw.json generated"

# 5b: Ensure SOUL.md exists in local fs before syncing
if [ -f "${TMP_DIR}/config/SOUL.md" ]; then
    cat "${TMP_DIR}/config/SOUL.md" | mgr_pipe "cat > /root/hiclaw-fs/agents/${WORKER_NAME}/SOUL.md"
    log "  SOUL.md copied"
fi

# 5c: Sync local agent dir to MinIO
mgr_bash "mc mirror '/root/hiclaw-fs/agents/${WORKER_NAME}/' '${STORAGE_PREFIX}/agents/${WORKER_NAME}/' --overwrite 2>&1 | tail -3"
log "  Agent config synced to MinIO"

# 5d: Merge AGENTS.md (builtin + user content)
if [ -f "${TMP_DIR}/config/AGENTS.md" ]; then
    cat "${TMP_DIR}/config/AGENTS.md" | mgr_pipe "cat > /tmp/migrate-agents-user.md"
    mgr_bash "
        source /opt/hiclaw/scripts/lib/builtin-merge.sh
        update_builtin_section_minio \
            '${STORAGE_PREFIX}/agents/${WORKER_NAME}/AGENTS.md' \
            '/opt/hiclaw/agent/worker-agent/AGENTS.md'
        mc cp '${STORAGE_PREFIX}/agents/${WORKER_NAME}/AGENTS.md' /tmp/migrate-agents-merged.md 2>/dev/null
        echo '' >> /tmp/migrate-agents-merged.md
        cat /tmp/migrate-agents-user.md >> /tmp/migrate-agents-merged.md
        mc cp /tmp/migrate-agents-merged.md '${STORAGE_PREFIX}/agents/${WORKER_NAME}/AGENTS.md' 2>/dev/null
        rm -f /tmp/migrate-agents-user.md /tmp/migrate-agents-merged.md
    "
    log "  AGENTS.md merged (builtin + user content)"
fi

# 5e: Push MEMORY.md
if [ -f "${TMP_DIR}/config/MEMORY.md" ]; then
    cat "${TMP_DIR}/config/MEMORY.md" | mgr_pipe "cat > /tmp/migrate-memory.md"
    mgr_bash "mc cp /tmp/migrate-memory.md '${STORAGE_PREFIX}/agents/${WORKER_NAME}/MEMORY.md' 2>/dev/null; rm -f /tmp/migrate-memory.md"
    log "  MEMORY.md pushed"
fi

# 5f: Push memory files
if [ -d "${TMP_DIR}/config/memory" ]; then
    tar -C "${TMP_DIR}/config/memory" -cf - . 2>/dev/null | mgr_pipe "
        mkdir -p /tmp/migrate-memory-dir && \
        tar -C /tmp/migrate-memory-dir -xf - && \
        mc mirror /tmp/migrate-memory-dir/ '${STORAGE_PREFIX}/agents/${WORKER_NAME}/memory/' --overwrite 2>/dev/null; \
        rm -rf /tmp/migrate-memory-dir
    "
    log "  Memory files pushed"
fi

# 5g: Push custom skills
if [ -d "${TMP_DIR}/skills" ] && [ "$(ls -A "${TMP_DIR}/skills" 2>/dev/null)" ]; then
    for skill_dir in "${TMP_DIR}/skills"/*/; do
        [ -d "${skill_dir}" ] || continue
        skill_name=$(basename "${skill_dir}")
        tar -C "${skill_dir}" -cf - . 2>/dev/null | mgr_pipe "
            mkdir -p /tmp/migrate-skill && \
            tar -C /tmp/migrate-skill -xf - && \
            mc mirror /tmp/migrate-skill/ '${STORAGE_PREFIX}/agents/${WORKER_NAME}/custom-skills/${skill_name}/' --overwrite 2>/dev/null; \
            rm -rf /tmp/migrate-skill
        "
        log "  Skill pushed: ${skill_name}"
    done
fi

# 5h: Push Matrix password for E2EE re-login
echo -n "${WORKER_PASSWORD}" | mgr_pipe "
    cat > /tmp/migrate-pw && \
    mc cp /tmp/migrate-pw '${STORAGE_PREFIX}/agents/${WORKER_NAME}/credentials/matrix/password' 2>/dev/null; \
    rm -f /tmp/migrate-pw
"

# 5i: Push file-sync skill
mgr_bash "mc mirror '/opt/hiclaw/agent/worker-agent/skills/file-sync/' \
    '${STORAGE_PREFIX}/agents/${WORKER_NAME}/skills/file-sync/' --overwrite 2>/dev/null || true"
log "  file-sync skill pushed"

# 5j: Persist worker credentials
mgr_bash "
    mkdir -p /data/worker-creds
    cat > '/data/worker-creds/${WORKER_NAME}.env' <<CREDS
WORKER_PASSWORD=\"${WORKER_PASSWORD}\"
WORKER_MINIO_PASSWORD=\"${WORKER_MINIO_PASSWORD}\"
WORKER_GATEWAY_KEY=\"${WORKER_GATEWAY_KEY}\"
CREDS
    chmod 600 '/data/worker-creds/${WORKER_NAME}.env'
"
log "  Credentials persisted"

# ============================================================
# Step 6: Update Manager registry and config
# ============================================================
msg "step.registry"

# 6a: Update Manager groupAllowFrom
mgr_bash "
    CONFIG=\"\${HOME}/openclaw.json\"
    WORKER_ID='${WORKER_MATRIX_ID}'
    if [ -f \"\${CONFIG}\" ]; then
        ALREADY=\$(jq -r --arg w \"\${WORKER_ID}\" \
            '.channels.matrix.groupAllowFrom // [] | map(select(. == \$w)) | length' \
            \"\${CONFIG}\" 2>/dev/null || echo '0')
        if [ \"\${ALREADY}\" = '0' ]; then
            jq --arg w \"\${WORKER_ID}\" \
                '.channels.matrix.groupAllowFrom += [\$w]' \
                \"\${CONFIG}\" > /tmp/manager-config-updated.json
            mv /tmp/manager-config-updated.json \"\${CONFIG}\"
        fi
    fi
"
log "  Manager groupAllowFrom updated"

# 6b: Update workers-registry.json
NOW_TS=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
SKILLS_JSON='["file-sync","mcporter"]'

mgr_bash "
    REGISTRY=\"\${HOME}/workers-registry.json\"
    if [ ! -f \"\${REGISTRY}\" ]; then
        echo '{\"version\":1,\"updated_at\":\"\",\"workers\":{}}' > \"\${REGISTRY}\"
    fi
    jq --arg w '${WORKER_NAME}' \
       --arg uid '${WORKER_MATRIX_ID}' \
       --arg rid '${ROOM_ID}' \
       --arg ts '${NOW_TS}' \
       --arg runtime 'openclaw' \
       --arg deployment 'local' \
       --arg image '${IMAGE_TAG}' \
       --argjson skills '${SKILLS_JSON}' \
       '.workers[\$w] = {
         \"matrix_user_id\": \$uid,
         \"room_id\": \$rid,
         \"runtime\": \$runtime,
         \"deployment\": \$deployment,
         \"skills\": \$skills,
         \"image\": \$image,
         \"created_at\": \$ts,
         \"skills_updated_at\": \$ts
       } | .updated_at = \$ts' \
       \"\${REGISTRY}\" > /tmp/workers-registry-updated.json
    mv /tmp/workers-registry-updated.json \"\${REGISTRY}\"
"
log "  workers-registry.json updated"

# 6c: Push worker skills
mgr_bash "bash /opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh \
    --worker '${WORKER_NAME}' --no-notify 2>/dev/null || true"
log "  Worker skills pushed"

# ============================================================
# Step 7: DM Manager to start the Worker
# ============================================================
msg "step.start"

# Login as admin via external gateway port
ADMIN_TOKEN=$(curl -sf -X POST "http://127.0.0.1:${GATEWAY_PORT}/_matrix/client/v3/login" \
    -H 'Content-Type: application/json' \
    -d '{"type":"m.login.password","identifier":{"type":"m.id.user","user":"'"${ADMIN_USER}"'"},"password":"'"${ADMIN_PASSWORD}"'"}' \
    2>/dev/null | jq -r '.access_token // empty')

if [ -z "${ADMIN_TOKEN}" ]; then
    error "Failed to login as admin to Matrix"
fi

# Find admin's DM room with Manager
# List joined rooms and find the one with Manager
JOINED_ROOMS=$(curl -sf "http://127.0.0.1:${GATEWAY_PORT}/_matrix/client/v3/joined_rooms" \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" 2>/dev/null | jq -r '.joined_rooms[]' 2>/dev/null)

DM_ROOM_ID=""
for rid in ${JOINED_ROOMS}; do
    # URL-encode the room ID
    ENCODED_RID=$(echo -n "${rid}" | jq -sRr @uri)
    ROOM_MEMBERS=$(curl -sf "http://127.0.0.1:${GATEWAY_PORT}/_matrix/client/v3/rooms/${ENCODED_RID}/members" \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" 2>/dev/null | jq -r '.chunk[].state_key' 2>/dev/null || true)
    # Check if this room has Manager and admin but NOT a worker (DM room)
    if echo "${ROOM_MEMBERS}" | grep -q "@manager:${MATRIX_DOMAIN}" && \
       echo "${ROOM_MEMBERS}" | grep -q "@${ADMIN_USER}:${MATRIX_DOMAIN}"; then
        # Count members - DM should have exactly 2
        MEMBER_COUNT=$(echo "${ROOM_MEMBERS}" | grep -c '.' || true)
        if [ "${MEMBER_COUNT}" -le 2 ]; then
            DM_ROOM_ID="${rid}"
            break
        fi
    fi
done

if [ -z "${DM_ROOM_ID}" ]; then
    # Create a DM room with Manager
    DM_RESP=$(curl -sf -X POST "http://127.0.0.1:${GATEWAY_PORT}/_matrix/client/v3/createRoom" \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        -H 'Content-Type: application/json' \
        -d '{"is_direct":true,"invite":["@manager:'"${MATRIX_DOMAIN}"'"],"preset":"trusted_private_chat"}' \
        2>/dev/null)
    DM_ROOM_ID=$(echo "${DM_RESP}" | jq -r '.room_id // empty' 2>/dev/null)
    if [ -z "${DM_ROOM_ID}" ]; then
        warn "Could not find or create DM room with Manager. Worker config is ready but Manager needs to be notified manually."
    fi
fi

if [ -n "${DM_ROOM_ID}" ]; then
    # Build the message
    if [ -n "${IMAGE_TAG}" ]; then
        MESSAGE="@manager:${MATRIX_DOMAIN} Worker ${WORKER_NAME} 的所有配置已通过导入脚本创建完毕（Matrix 账号、Room、MinIO、Higress、openclaw.json、workers-registry 均已就绪）。请使用镜像 ${IMAGE_TAG} 启动此 Worker 的容器。"
    else
        MESSAGE="@manager:${MATRIX_DOMAIN} Worker ${WORKER_NAME} 的所有配置已通过导入脚本创建完毕（Matrix 账号、Room、MinIO、Higress、openclaw.json、workers-registry 均已就绪）。请使用标准 Worker 镜像启动此 Worker 的容器。"
    fi

    if [ -n "${PROXY}" ]; then
        NO_PROXY_LIST="*.hiclaw.io,127.0.0.1,localhost"
        if [ -n "${EXTRA_NO_PROXY}" ]; then
            NO_PROXY_LIST="${NO_PROXY_LIST},${EXTRA_NO_PROXY}"
        fi
        MESSAGE="${MESSAGE} 容器环境变量需设置 HTTP_PROXY=${PROXY} HTTPS_PROXY=${PROXY} NO_PROXY=${NO_PROXY_LIST}"
    fi

    TXN_ID="migrate-$(date +%s)-$$"
    ENCODED_DM_RID=$(echo -n "${DM_ROOM_ID}" | jq -sRr @uri)

    curl -sf -X PUT \
        "http://127.0.0.1:${GATEWAY_PORT}/_matrix/client/v3/rooms/${ENCODED_DM_RID}/send/m.room.message/${TXN_ID}" \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        -H 'Content-Type: application/json' \
        -d '{"msgtype":"m.text","body":"'"$(echo "${MESSAGE}" | sed 's/"/\\"/g')"'"}' \
        >/dev/null 2>&1

    log "  Message sent to Manager"
fi

# ============================================================
# Done
# ============================================================
echo ""
msg "done.title"
msg "done.worker" "${WORKER_NAME}"
msg "done.image" "${IMAGE_TAG:-standard}"
msg "done.room" "${ROOM_ID}"
echo ""
msg "done.hint"
echo ""
