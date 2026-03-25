FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /hiclaw-controller ./cmd/controller/
RUN CGO_ENABLED=0 go build -o /hiclaw ./cmd/hiclaw/

# Download kube-apiserver for embedded mode
FROM alpine:3.20 AS kube-bin
ARG KUBE_VERSION=v1.31.3
ARG TARGETARCH=amd64
RUN apk add --no-cache curl && \
    curl -fSL -o /kube-apiserver \
      "https://dl.k8s.io/release/${KUBE_VERSION}/bin/linux/${TARGETARCH}/kube-apiserver" && \
    chmod +x /kube-apiserver

FROM alpine:3.20
RUN apk add --no-cache bash jq curl
COPY --from=builder /hiclaw-controller /usr/local/bin/hiclaw-controller
COPY --from=builder /hiclaw /usr/local/bin/hiclaw
COPY --from=kube-bin /kube-apiserver /usr/local/bin/kube-apiserver
COPY config/crd/ /opt/hiclaw/config/crd/
ENTRYPOINT ["hiclaw-controller"]
