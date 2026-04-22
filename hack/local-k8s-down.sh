#!/bin/bash
# local-k8s-down.sh — Tear down the local HiClaw kind cluster.
#
# Usage:
#   ./hack/local-k8s-down.sh

set -euo pipefail

CLUSTER_NAME="${HICLAW_CLUSTER_NAME:-hiclaw}"
NAMESPACE="${HICLAW_NAMESPACE:-hiclaw}"

log() { echo -e "\033[36m[HiClaw K8s]\033[0m $1"; }

# Uninstall Helm release (if exists)
if helm list -n "$NAMESPACE" 2>/dev/null | grep -q hiclaw; then
    log "Uninstalling Helm release 'hiclaw'..."
    helm uninstall hiclaw -n "$NAMESPACE" 2>/dev/null || true
fi

# Delete kind cluster
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    log "Deleting kind cluster '${CLUSTER_NAME}'..."
    kind delete cluster --name "$CLUSTER_NAME"
    log "Cluster deleted."
else
    log "kind cluster '${CLUSTER_NAME}' not found, nothing to delete."
fi
