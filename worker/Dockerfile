# ============================================================
# HiClaw Worker Agent - Lightweight Container
# ============================================================
# Contains: OpenClaw (Agent framework) + mc (MinIO Client) + mcporter (MCP CLI)
# Workers are stateless -- all config/state stored in centralized MinIO.
#
# Build args:
#   HIGRESS_REGISTRY  - base image registry (default: cn-hangzhou)
#   APT_MIRROR        - APT mirror host (default: mirrors.aliyun.com, set to empty to use official)
# ============================================================

ARG HIGRESS_REGISTRY=higress-registry.cn-hangzhou.cr.aliyuncs.com

# ============ Stage 1: mc (MinIO Client) ============
FROM ${HIGRESS_REGISTRY}/higress/mc:20260216 AS mc

# ============ Stage 2: OpenClaw + mcporter (shared base image) ============
ARG OPENCLAW_BASE_TAG=latest
FROM ${HIGRESS_REGISTRY}/higress/openclaw-base:${OPENCLAW_BASE_TAG} AS openclaw-build

# ============ Final Image ============
FROM ${HIGRESS_REGISTRY}/higress/ubuntu:24.04

ARG APT_MIRROR=mirrors.aliyun.com
RUN if [ -n "${APT_MIRROR}" ]; then \
        sed -i "s|archive.ubuntu.com|${APT_MIRROR}|g; s|security.ubuntu.com|${APT_MIRROR}|g" \
            /etc/apt/sources.list.d/ubuntu.sources 2>/dev/null || \
        sed -i "s|archive.ubuntu.com|${APT_MIRROR}|g; s|security.ubuntu.com|${APT_MIRROR}|g" \
            /etc/apt/sources.list 2>/dev/null || true; \
    fi && \
    apt-get update && apt-get install -y \
    curl jq git python3 python3-pip \
    openssh-client ca-certificates procps \
    && rm -rf /var/lib/apt/lists/*

# Node.js 22 from build stage (Ubuntu apt only has 18.x which lacks --disable-warning support)
COPY --from=openclaw-build /usr/local/bin/node /usr/local/bin/node
COPY --from=openclaw-build /usr/local/bin/npm /usr/local/bin/npm
COPY --from=openclaw-build /usr/local/bin/npx /usr/local/bin/npx
COPY --from=openclaw-build /usr/local/lib/node_modules /usr/local/lib/node_modules
# Create pnpm symlinks
RUN ln -sf /usr/local/lib/node_modules/pnpm/bin/pnpm.cjs /usr/local/bin/pnpm && ln -sf /usr/local/lib/node_modules/pnpm/bin/pnpx.cjs /usr/local/bin/pnpx

# mc (MinIO Client)
COPY --from=mc /usr/bin/mc /usr/local/bin/mc

# OpenClaw
COPY --from=openclaw-build /opt/openclaw /opt/openclaw

# mcporter (use dist/cli.js directly to ensure relative imports work)
RUN ln -sf /usr/local/lib/node_modules/mcporter/dist/cli.js /usr/local/bin/mcporter

# Ensure openclaw binary is on PATH and config is found
ENV PATH="/opt/openclaw/packages/clawdbot/node_modules/.bin:${PATH}"
ENV OPENCLAW_CONFIG_PATH="/root/.openclaw/openclaw.json"

# Worker entrypoint and sync scripts
COPY scripts/ /opt/hiclaw/scripts/
RUN chmod +x /opt/hiclaw/scripts/*.sh && \
    printf '#!/bin/bash\nexec "${HOME}/hiclaw-fs/agents/${HICLAW_WORKER_NAME}/skills/file-sync/scripts/hiclaw-sync.sh" "$@"\n' \
    > /usr/local/bin/hiclaw-sync && chmod +x /usr/local/bin/hiclaw-sync

# hiclaw-fs root (mirrors Manager's ~/hiclaw-fs/ layout)
RUN mkdir -p /root/hiclaw-fs /root/.openclaw

WORKDIR /root/hiclaw-fs

ENTRYPOINT ["/opt/hiclaw/scripts/worker-entrypoint.sh"]
