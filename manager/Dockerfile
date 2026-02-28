# ============================================================
# HiClaw Manager Agent - All-in-One Container
# ============================================================
# Uses Higress all-in-one as the final base (keeps all Higress scripts/binaries
# at their original paths) and layers on Tuwunel, MinIO, Element Web, OpenClaw.
#
# Build args:
#   HIGRESS_REGISTRY  - base image registry (default: cn-hangzhou)
#   APT_MIRROR        - APT mirror host (default: mirrors.aliyun.com, set to empty to use official)
# ============================================================

ARG HIGRESS_REGISTRY=higress-registry.cn-hangzhou.cr.aliyuncs.com

# ============ Stage 1: Tuwunel Matrix Server ============
FROM ${HIGRESS_REGISTRY}/higress/tuwunel:20260216 AS tuwunel

# ============ Stage 2: MinIO Object Storage ============
FROM ${HIGRESS_REGISTRY}/higress/minio:20260216 AS minio

# ============ Stage 3: MinIO Client (mc) ============
FROM ${HIGRESS_REGISTRY}/higress/mc:20260216 AS mc

# ============ Stage 4: Element Web ============
FROM ${HIGRESS_REGISTRY}/higress/element-web:20260216 AS element-web

# ============ Stage 5: OpenClaw + mcporter (shared base image) ============
ARG OPENCLAW_BASE_TAG=latest
FROM ${HIGRESS_REGISTRY}/higress/openclaw-base:${OPENCLAW_BASE_TAG} AS openclaw-build

# Install AI coding CLI tools here (npm works correctly in this stage)
RUN npm install -g @google/gemini-cli @anthropic-ai/claude-code

# ============ Final Image: Higress All-in-One as base ============
# This preserves all Higress binaries, scripts, and configs at their original paths
FROM ${HIGRESS_REGISTRY}/higress/all-in-one:latest

ARG APT_MIRROR=mirrors.aliyun.com
RUN if [ -n "${APT_MIRROR}" ]; then \
        sed -i "s|archive.ubuntu.com|${APT_MIRROR}|g; s|security.ubuntu.com|${APT_MIRROR}|g" \
            /etc/apt/sources.list 2>/dev/null || true; \
    fi && \
    apt-get update && apt-get install -y \
    jq nginx gettext-base ca-certificates procps \
    && rm -rf /var/lib/apt/lists/*

# Create log directory
RUN mkdir -p /var/log/hiclaw

# ---- Copy binaries from build stages ----

# Tuwunel (single static binary)
COPY --from=tuwunel /usr/bin/tuwunel /usr/local/bin/tuwunel

# MinIO
COPY --from=minio /usr/bin/minio /usr/local/bin/minio

# mc (MinIO Client)
COPY --from=mc /usr/bin/mc /usr/local/bin/mc

# Element Web static files
COPY --from=element-web /app /opt/element-web

# Node.js 20 from build stage (Ubuntu's apt nodejs is v12, too old for OpenClaw)
COPY --from=openclaw-build /usr/local/bin/node /usr/local/bin/node
COPY --from=openclaw-build /usr/local/bin/npm /usr/local/bin/npm
COPY --from=openclaw-build /usr/local/bin/npx /usr/local/bin/npx
COPY --from=openclaw-build /usr/local/lib/node_modules /usr/local/lib/node_modules
# Create pnpm symlinks
RUN ln -sf /usr/local/lib/node_modules/pnpm/bin/pnpm.cjs /usr/local/bin/pnpm && ln -sf /usr/local/lib/node_modules/pnpm/bin/pnpx.cjs /usr/local/bin/pnpx

# OpenClaw (full source build)
COPY --from=openclaw-build /opt/openclaw /opt/openclaw
# mcporter (use dist/cli.js directly to ensure relative imports work)
RUN ln -sf /usr/local/lib/node_modules/mcporter/dist/cli.js /usr/local/bin/mcporter

# AI coding CLI binaries (installed in openclaw-build where npm works correctly)
# node_modules already copied above.
# claude: COPY works (uses absolute require paths internally)
# gemini: uses relative ESM imports (import './src/gemini.js'), so symlink is required
#         to preserve the correct base directory for module resolution
COPY --from=openclaw-build /usr/local/bin/claude /usr/local/bin/claude

# Ensure openclaw binary is on PATH and config is found
ENV PATH="/opt/openclaw/packages/clawdbot/node_modules/.bin:${PATH}"
ENV OPENCLAW_CONFIG_PATH="/root/manager-workspace/openclaw.json"

# ---- Copy HiClaw configuration and scripts ----
# Our supervisord.conf replaces the Higress one; our scripts handle all components
COPY supervisord.conf /etc/supervisor/conf.d/supervisord.conf
COPY scripts/ /opt/hiclaw/scripts/
COPY agent/ /opt/hiclaw/agent/
COPY configs/ /opt/hiclaw/configs/

ARG BUILTIN_VERSION=latest
RUN echo "${BUILTIN_VERSION}" > /opt/hiclaw/agent/.builtin-version

# ---- Install AI coding CLI tools ----
# gemini: symlink to dist/index.js so relative ESM imports resolve correctly
# qodercli via official install script (best-effort; failure does not block build).
RUN ln -sf /usr/local/lib/node_modules/@google/gemini-cli/dist/index.js /usr/local/bin/gemini
RUN curl -fsSL https://qoder.com/install.sh | bash || true

# Make scripts executable
RUN chmod +x /opt/hiclaw/scripts/init/*.sh /opt/hiclaw/scripts/lib/*.sh && \
    chmod +x /opt/hiclaw/scripts/setup-host-symlinks.sh && \
    find /opt/hiclaw/agent/skills/*/scripts -name '*.sh' -exec chmod +x {} + 2>/dev/null || true && \
    find /opt/hiclaw/agent/worker-skills/*/scripts -name '*.sh' -exec chmod +x {} + 2>/dev/null || true && \
    find /opt/hiclaw/agent/worker-agent/skills/*/scripts -name '*.sh' -exec chmod +x {} + 2>/dev/null || true

# ---- Ports ----
# 8080: Higress Gateway (unified entry point)
# 8001: Higress Console
# 9001: MinIO Console
EXPOSE 8080 8001 9001

# ---- Persistent data ----
# /data: MinIO object storage, secrets, worker credentials
# /root/manager-workspace: Manager's working directory (host-mounted via install script)
VOLUME ["/data"]

# ---- Start ----
ENTRYPOINT ["supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
