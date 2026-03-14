# ============================================================
# HiClaw Manager Agent - All-in-One Container
# ============================================================
# Based on openclaw-base (which includes all-in-one + Node.js + OpenClaw)
# and layers on Tuwunel, MinIO, Element Web, and AI CLI tools.
#
# Build args:
#   HIGRESS_REGISTRY  - base image registry (default: cn-hangzhou)
#   OPENCLAW_BASE_IMAGE - openclaw-base image (default: hiclaw/openclaw-base:latest)
# ============================================================

ARG HIGRESS_REGISTRY=higress-registry.cn-hangzhou.cr.aliyuncs.com
ARG OPENCLAW_BASE_IMAGE=hiclaw/openclaw-base:20260313-5a0df68

# ============ Stage 1: Tuwunel Matrix Server ============
FROM ${HIGRESS_REGISTRY}/higress/tuwunel:20260216 AS tuwunel

# ============ Stage 2: MinIO Object Storage ============
FROM ${HIGRESS_REGISTRY}/higress/minio:20260216 AS minio

# ============ Stage 3: MinIO Client (mc) ============
FROM ${HIGRESS_REGISTRY}/higress/mc:20260216 AS mc

# ============ Stage 4: Element Web ============
FROM ${HIGRESS_REGISTRY}/higress/element-web:20260216 AS element-web

# ============ Final Image: Based on openclaw-base ============
# openclaw-base already includes: all-in-one + Node.js 22 + OpenClaw + mcporter + common tools
FROM ${OPENCLAW_BASE_IMAGE}

# Create log directory
RUN mkdir -p /var/log/hiclaw

# ---- Copy binaries from build stages (HiClaw-specific components) ----

# Tuwunel (single static binary)
COPY --from=tuwunel /usr/bin/tuwunel /usr/local/bin/tuwunel

# MinIO
COPY --from=minio /usr/bin/minio /usr/local/bin/minio

# mc (MinIO Client)
COPY --from=mc /usr/bin/mc /usr/local/bin/mc

# Element Web static files
COPY --from=element-web /app /opt/element-web

# ---- Install AI coding CLI tools (Manager-only) ----
RUN npm install -g @google/gemini-cli @anthropic-ai/claude-code

# gemini: symlink to dist/index.js so relative ESM imports resolve correctly
RUN ln -sf /usr/local/lib/node_modules/@google/gemini-cli/dist/index.js /usr/local/bin/gemini

# qodercli via official install script (best-effort; failure does not block build)
RUN curl -fsSL https://qoder.com/install.sh | bash || true

# Ensure openclaw binary is on PATH and config is found
ENV PATH="/opt/openclaw/packages/clawdbot/node_modules/.bin:${PATH}"
ENV OPENCLAW_CONFIG_PATH="/root/manager-workspace/openclaw.json"

# ---- Copy HiClaw configuration and scripts ----
# Our supervisord.conf replaces the Higress one; our scripts handle all components
COPY supervisord.conf /etc/supervisor/conf.d/supervisord.conf
COPY scripts/ /opt/hiclaw/scripts/

# Install systemctl shim so 'openclaw gateway restart' works without systemd
RUN cp /opt/hiclaw/scripts/systemctl-shim.sh /usr/local/bin/systemctl && \
    chmod +x /usr/local/bin/systemctl
COPY agent/ /opt/hiclaw/agent/
COPY configs/ /opt/hiclaw/configs/

ARG BUILTIN_VERSION=latest
RUN echo "${BUILTIN_VERSION}" > /opt/hiclaw/agent/.builtin-version

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
# 18888: OpenClaw Console (via nginx reverse proxy to gateway loopback 18799)
EXPOSE 8080 8001 9001 18888

# ---- Persistent data ----
# /data: MinIO object storage, secrets, worker credentials
# /root/manager-workspace: Manager's working directory (host-mounted via install script)
VOLUME ["/data"]

# ---- Start ----
ENTRYPOINT ["supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
