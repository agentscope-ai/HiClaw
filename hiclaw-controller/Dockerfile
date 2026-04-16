# ============================================================
# hiclaw-controller - Standalone build for Go binaries
# ============================================================
# Produces: /hiclaw-controller, /hiclaw, /kube-apiserver, /opt/hiclaw/config/crd/
# Manager Dockerfile copies these artifacts via: COPY --from=hiclaw-controller ...
#
# Since v0.2: includes merged worker lifecycle, auth, credentials, and proxy.

ARG HIGRESS_REGISTRY=higress-registry.cn-hangzhou.cr.aliyuncs.com

# ============ Stage 1: Build Go binaries ============
FROM ${HIGRESS_REGISTRY}/higress/golang:1.25-alpine AS builder

ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
RUN apk add --no-cache gcc musl-dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download || true

COPY . .
RUN CGO_ENABLED=1 go build -ldflags '-extldflags "-static"' -o /hiclaw-controller ./cmd/controller/
RUN CGO_ENABLED=0 go build -o /hiclaw ./cmd/hiclaw/

# ============ Stage 2: Download kube-apiserver ============
FROM ${HIGRESS_REGISTRY}/higress/alpine:3.20 AS kube-bin
ARG KUBE_VERSION=v1.31.3
ARG TARGETARCH=amd64
RUN apk add --no-cache curl && \
    curl -fSL -o /kube-apiserver \
      "https://dl.k8s.io/release/${KUBE_VERSION}/bin/linux/${TARGETARCH}/kube-apiserver" && \
    chmod +x /kube-apiserver

# ============ Final: Minimal image with all artifacts ============
FROM ${HIGRESS_REGISTRY}/higress/alpine:3.20

RUN apk add --no-cache bash jq curl unzip && \
    curl -fSL -o /usr/local/bin/mc https://dl.min.io/client/mc/release/linux-amd64/mc && \
    chmod +x /usr/local/bin/mc

COPY --from=builder /hiclaw-controller /usr/local/bin/hiclaw-controller
COPY --from=builder /hiclaw /usr/local/bin/hiclaw
COPY --from=kube-bin /kube-apiserver /usr/local/bin/kube-apiserver
COPY config/crd/ /opt/hiclaw/config/crd/

# Agent template files (builtin skills, AGENTS.md, HEARTBEAT.md, etc.)
# Used by Deployer to push builtin worker config to OSS.
# Build context "agent" must be provided via --build-context agent=<path-to-manager/agent>
COPY --from=agent worker-agent/ /opt/hiclaw/agent/worker-agent/
COPY --from=agent team-leader-agent/ /opt/hiclaw/agent/team-leader-agent/

ENTRYPOINT ["hiclaw-controller"]
