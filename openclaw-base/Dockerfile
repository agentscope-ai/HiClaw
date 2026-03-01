# ============================================================
# OpenClaw Base Image - Shared build for Manager and Worker
# ============================================================
# Pre-compiles OpenClaw + mcporter so that Manager and Worker
# can share the same base layer, avoiding ~900MB duplicate downloads.
#
# Build args:
#   HIGRESS_REGISTRY  - base image registry (default: cn-hangzhou)
#   APT_MIRROR        - APT mirror host (default: mirrors.aliyun.com, set to empty to use official)
# ============================================================

ARG HIGRESS_REGISTRY=higress-registry.cn-hangzhou.cr.aliyuncs.com

FROM ${HIGRESS_REGISTRY}/higress/node:20-slim

# Build args (must be declared BEFORE any RUN that uses them)
ARG APT_MIRROR=mirrors.aliyun.com
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG http_proxy
ARG https_proxy
ARG NPM_REGISTRY=https://registry.npmjs.org/

# Set proxy as environment variables to ensure all tools can use them
ENV HTTP_PROXY=${HTTP_PROXY} \
    HTTPS_PROXY=${HTTPS_PROXY} \
    http_proxy=${http_proxy} \
    https_proxy=${https_proxy}

RUN if [ -n "${APT_MIRROR}" ]; then \
        sed -i "s|deb.debian.org|${APT_MIRROR}|g" /etc/apt/sources.list.d/debian.sources 2>/dev/null || \
        sed -i "s|deb.debian.org|${APT_MIRROR}|g" /etc/apt/sources.list 2>/dev/null || true; \
    fi && \
    apt-get update && apt-get install -y git python3 make g++ curl && \
    npm install -g pnpm n && n 22 && \
    hash -r && node --version

RUN git clone https://github.com/johnlanni/openclaw.git /opt/openclaw && \
    cd /opt/openclaw && git checkout df5225e1fc310158f84f888b74ba2ea815190181

RUN cd /opt/openclaw && pnpm config set registry ${NPM_REGISTRY} && pnpm install && pnpm build

RUN npm install -g mcporter
