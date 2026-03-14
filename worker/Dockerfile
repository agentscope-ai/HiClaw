# ============================================================
# HiClaw Worker Agent - Lightweight Container
# ============================================================
# Based on openclaw-base (which includes all-in-one + Node.js + OpenClaw + mcporter)
# Workers are stateless -- all config/state stored in centralized MinIO.
#
# Build args:
#   HIGRESS_REGISTRY  - base image registry (default: cn-hangzhou)
#   OPENCLAW_BASE_IMAGE - openclaw-base image (default: hiclaw/openclaw-base:latest)
# ============================================================

ARG HIGRESS_REGISTRY=higress-registry.cn-hangzhou.cr.aliyuncs.com
ARG OPENCLAW_BASE_IMAGE=hiclaw/openclaw-base:20260313-5a0df68

# ============ Stage 1: mc (MinIO Client) ============
FROM ${HIGRESS_REGISTRY}/higress/mc:20260216 AS mc

# ============ Final Image: Based on openclaw-base ============
# openclaw-base already includes: all-in-one + Node.js 22 + OpenClaw + mcporter + common tools
FROM ${OPENCLAW_BASE_IMAGE}

# mc (MinIO Client) - not included in openclaw-base
COPY --from=mc /usr/bin/mc /usr/local/bin/mc

# Ensure openclaw binary is on PATH
# OPENCLAW_CONFIG_PATH is intentionally NOT set here:
# - HOME is set to the workspace via docker run -e HOME=...
# - entrypoint creates ~/.openclaw/openclaw.json symlink in that workspace
# - openclaw falls back to ~/.openclaw/openclaw.json when OPENCLAW_CONFIG_PATH is unset
#   which correctly resolves to <workspace>/.openclaw/openclaw.json
ENV PATH="/opt/openclaw/packages/clawdbot/node_modules/.bin:${PATH}"

# Worker entrypoint and sync scripts
COPY scripts/ /opt/hiclaw/scripts/
RUN chmod +x /opt/hiclaw/scripts/*.sh && \
    printf '#!/bin/bash\nexec "/root/hiclaw-fs/agents/${HICLAW_WORKER_NAME}/skills/file-sync/scripts/hiclaw-sync.sh" "$@"\n' \
    > /usr/local/bin/hiclaw-sync && chmod +x /usr/local/bin/hiclaw-sync

# hiclaw-fs root (mirrors Manager's ~/hiclaw-fs/ layout)
RUN mkdir -p /root/hiclaw-fs /root/.openclaw

WORKDIR /root/hiclaw-fs

ENTRYPOINT ["/opt/hiclaw/scripts/worker-entrypoint.sh"]
