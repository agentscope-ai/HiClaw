# ============================================================
# HiClaw Worker Agent - Lightweight Container
# ============================================================
# Based on openclaw-base (which includes all-in-one + Node.js + OpenClaw + mcporter)
# Workers are stateless -- all config/state stored in centralized MinIO.
#
# Build args:
#   HIGRESS_REGISTRY           - base image registry (default: cn-hangzhou)
#   OPENCLAW_BASE_IMAGE        - openclaw-base image (default: hiclaw/openclaw-base:latest)
#   OPENCLAW_CMS_PLUGIN_ENABLED - bundle openclaw-cms-plugin at build time (default: 0)
#   OPENCLAW_CMS_PLUGIN_URL    - plugin tarball URL (used only when ENABLED=1)
# ============================================================

ARG HIGRESS_REGISTRY=higress-registry.cn-hangzhou.cr.aliyuncs.com
ARG OPENCLAW_BASE_IMAGE=hiclaw/openclaw-base:20260314-be855ee

# ============ Stage 1: mc (MinIO Client) ============
FROM ${HIGRESS_REGISTRY}/higress/mc:20260216 AS mc

# ============ Final Image: Based on openclaw-base ============
# openclaw-base already includes: all-in-one + Node.js 22 + OpenClaw + mcporter + common tools
FROM ${OPENCLAW_BASE_IMAGE}
ARG OPENCLAW_CMS_PLUGIN_ENABLED=0
ARG OPENCLAW_CMS_PLUGIN_URL=https://arms-apm-cn-hangzhou-pre.oss-cn-hangzhou.aliyuncs.com/openclaw-cms-plugin/openclaw-cms-plugin.tar.gz

# mc (MinIO Client) - not included in openclaw-base
COPY --from=mc /usr/bin/mc /usr/local/bin/mc

# Ensure openclaw binary is on PATH
# OPENCLAW_CONFIG_PATH is intentionally NOT set here:
# - HOME is set to the workspace via docker run -e HOME=...
# - entrypoint creates ~/.openclaw/openclaw.json symlink in that workspace
# - openclaw falls back to ~/.openclaw/openclaw.json when OPENCLAW_CONFIG_PATH is unset
#   which correctly resolves to <workspace>/.openclaw/openclaw.json
ENV PATH="/opt/openclaw/packages/clawdbot/node_modules/.bin:${PATH}"
ENV OPENCLAW_CMS_PLUGIN_DIR="/opt/openclaw/extensions/openclaw-cms-plugin"

# ---- Built-in observability plugin (optional, must match Manager build arg) ----
RUN if [ "${OPENCLAW_CMS_PLUGIN_ENABLED}" = "1" ]; then \
      echo "Installing openclaw-cms-plugin from ${OPENCLAW_CMS_PLUGIN_URL}..." && \
      tmp_dir="$(mktemp -d)" && \
      curl -fsSL "${OPENCLAW_CMS_PLUGIN_URL}" -o "${tmp_dir}/plugin.tar.gz" && \
      mkdir -p "${OPENCLAW_CMS_PLUGIN_DIR}" && \
      tar -xzf "${tmp_dir}/plugin.tar.gz" -C "${tmp_dir}" && \
      if [ -d "${tmp_dir}/openclaw-cms-plugin" ]; then \
        cp -rf "${tmp_dir}/openclaw-cms-plugin/." "${OPENCLAW_CMS_PLUGIN_DIR}/"; \
      else \
        cp -rf "${tmp_dir}/." "${OPENCLAW_CMS_PLUGIN_DIR}/"; \
      fi && \
      cd "${OPENCLAW_CMS_PLUGIN_DIR}" && \
      npm install --omit=dev --ignore-scripts && \
      rm -rf "${tmp_dir}" && \
      echo "openclaw-cms-plugin installed at ${OPENCLAW_CMS_PLUGIN_DIR}"; \
    else \
      echo "Skipping openclaw-cms-plugin installation (OPENCLAW_CMS_PLUGIN_ENABLED=${OPENCLAW_CMS_PLUGIN_ENABLED})"; \
    fi

# Shared environment bootstrap and credential management
COPY --from=shared . /opt/hiclaw/scripts/lib/

# Worker entrypoint and sync scripts
COPY --chmod=755 scripts/ /opt/hiclaw/scripts/
RUN printf '#!/bin/bash\nexec "/root/hiclaw-fs/agents/${HICLAW_WORKER_NAME}/skills/file-sync/scripts/hiclaw-sync.sh" "$@"\n' \
    > /usr/local/bin/hiclaw-sync && chmod +x /usr/local/bin/hiclaw-sync

# hiclaw-fs root (mirrors Manager's ~/hiclaw-fs/ layout)
RUN mkdir -p /root/hiclaw-fs /root/.openclaw

WORKDIR /root/hiclaw-fs

ENTRYPOINT ["/opt/hiclaw/scripts/worker-entrypoint.sh"]
