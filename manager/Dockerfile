# ============================================================
# HiClaw Manager Agent - All-in-One Container
# ============================================================
# Based on openclaw-base (which includes all-in-one + Node.js + OpenClaw)
# and layers on Tuwunel, MinIO, and Element Web.
#
# Build args:
#   HIGRESS_REGISTRY  - base image registry (default: cn-hangzhou)
#   OPENCLAW_BASE_IMAGE - openclaw-base image (default: hiclaw/openclaw-base:latest)
# ============================================================

ARG HIGRESS_REGISTRY=higress-registry.cn-hangzhou.cr.aliyuncs.com
ARG OPENCLAW_BASE_IMAGE=hiclaw/openclaw-base:20260314-be855ee

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
#
# Layer ordering: stable binaries → stable config → shared lib → scripts → agent
# This ensures that high-frequency changes (agent/, scripts/) only invalidate
# the smallest, topmost layers, so users pull minimal data on upgrades.
FROM ${OPENCLAW_BASE_IMAGE}

# ---- Stable binaries from build stages (~355MB, rarely change) ----

RUN mkdir -p /var/log/hiclaw

# Tuwunel (single static binary)
COPY --from=tuwunel /usr/bin/tuwunel /usr/local/bin/tuwunel

# MinIO
COPY --from=minio /usr/bin/minio /usr/local/bin/minio

# mc (MinIO Client)
COPY --from=mc /usr/bin/mc /usr/local/bin/mc

# Element Web static files
COPY --from=element-web /app /opt/element-web

# ---- Stable configuration (seldom change) ----

ENV PATH="/opt/openclaw/packages/clawdbot/node_modules/.bin:${PATH}"
ENV OPENCLAW_CONFIG_PATH="/root/manager-workspace/openclaw.json"

COPY supervisord.conf /etc/supervisor/conf.d/supervisord.conf
COPY configs/ /opt/hiclaw/configs/

# ---- Shared lib (low-frequency changes, ~8KB) ----

COPY --from=shared . /opt/hiclaw/scripts/lib/

# ---- Scripts (high-frequency changes, ~168KB) ----

COPY --chmod=755 scripts/ /opt/hiclaw/scripts/
RUN cp /opt/hiclaw/scripts/systemctl-shim.sh /usr/local/bin/systemctl

# ---- Agent definitions (highest-frequency changes, ~740KB) ----

COPY agent/ /opt/hiclaw/agent/

ARG BUILTIN_VERSION=latest
RUN echo "${BUILTIN_VERSION}" > /opt/hiclaw/agent/.builtin-version \
    && find /opt/hiclaw/agent/skills/*/scripts -name '*.sh' -exec chmod +x {} + 2>/dev/null || true \
    && find /opt/hiclaw/agent/worker-skills/*/scripts -name '*.sh' -exec chmod +x {} + 2>/dev/null || true \
    && find /opt/hiclaw/agent/worker-agent/skills/*/scripts -name '*.sh' -exec chmod +x {} + 2>/dev/null || true

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
