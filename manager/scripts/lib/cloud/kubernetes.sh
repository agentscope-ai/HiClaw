#!/bin/bash
# k8s.sh - Kubernetes (ACK) provider: **Worker Pod lifecycle only** (kubernetes-api.py)
#
# Sourced by container-api.sh when the file exists.
#
# ---------------------------------------------------------------------------
# Comparison with aliyun-sae.sh + aliyun-api.py (read this if you miss "consumer")
# ---------------------------------------------------------------------------
#
# | Concern              | aliyun-sae.sh              | kubernetes.sh (this file)   |
# |----------------------|----------------------------|-----------------------------|
# | AI Gateway Consumer  | cloud_create_consumer      | **(none — not duplicated)** |
# |                      | cloud_bind_consumer        | Same functions live in      |
# |                      | → aliyun-api.py gw-*       | **aliyun-sae.sh**;          |
# |                      |                            | gateway-api.sh calls them   |
# |                      |                            | for Runtime=k8s **and**     |
# |                      |                            | aliyun (see gateway-api.sh).|
# | Spawn Worker workload| sae_create_worker          | k8s_create_worker           |
# |                      | → aliyun-api.py sae-create | → kubernetes-api.py k8s-create |
#
# Consumer/bind runs in create-worker.sh Steps 3–5 **before** Step 9; both SAE and ACK
# use the same Python entrypoints (gw-*) on the **Manager** process.
#
# Cloud Worker flow (create-worker.sh, HICLAW_ALIYUN_WORKER_BACKEND=k8s): Steps 1–8 same as SAE;
# Step 9 only: k8s_create_worker ↔ aliyun-api.py sae-create.
#   - SAE: oidc_role_name on application.
#   - ACK: Worker Pod SA + ack-pod-identity-webhook.
#
# Prerequisites:
#   - /opt/hiclaw/scripts/lib/cloud/kubernetes-api.py available
#   - In-Cluster ServiceAccount token (Manager) OR kubeconfig for API calls
 
K8S_WORKER_API="/opt/hiclaw/scripts/lib/cloud/kubernetes-api.py"
K8S_SA_TOKEN_FILE="/var/run/secrets/kubernetes.io/serviceaccount/token"
 
cloud_k8s_available() {
    # Check if K8s API script exists and either:
    # 1. In-Cluster mode: ServiceAccount token file exists
    # 2. Kubeconfig mode: KUBECONFIG or ~/.kube/config exists
    if [ ! -f "${K8S_WORKER_API}" ]; then
        return 1
    fi
 
    if [ -f "${K8S_SA_TOKEN_FILE}" ]; then
        return 0  # In-Cluster mode
    fi
 
    if [ -n "${KUBECONFIG:-}" ] && [ -f "${KUBECONFIG}" ]; then
        return 0  # Kubeconfig mode
    fi
 
    if [ -f "${HOME}/.kube/config" ]; then
        return 0  # Default kubeconfig
    fi
 
    return 1
}
 
# ── K8s Worker lifecycle (ACK Step 9 — counterpart to sae_create_worker / aliyun-api sae-create) ──
 
k8s_create_worker() {
    local worker_name="$1"
    local extra_envs_json="$2"
    local image_override="${3:-}"
    extra_envs_json="${extra_envs_json:-"{}"}"
    _log "Creating K8s Pod for worker (ACK Step 9, parity with SAE sae-create): ${worker_name}"
    local envs_file
    envs_file=$(mktemp /tmp/k8s-envs-XXXXXX.json)
    printf '%s' "${extra_envs_json}" > "${envs_file}"
    local image_arg=""
    if [ -n "${image_override}" ]; then
        image_arg="--image ${image_override}"
    fi
    python3 "${K8S_WORKER_API}" k8s-create --name "${worker_name}" --envs "@${envs_file}" ${image_arg}
    local rc=$?
    rm -f "${envs_file}"
    return ${rc}
}
 
k8s_create_copaw_worker() {
    local worker_name="$1"
    local extra_envs_json="$2"
    local image_override="${3:-}"
    extra_envs_json="${extra_envs_json:-"{}"}"
    _log "Creating K8s Pod for CoPaw worker: ${worker_name}"
    local envs_file
    envs_file=$(mktemp /tmp/k8s-envs-XXXXXX.json)
    printf '%s' "${extra_envs_json}" > "${envs_file}"
    local image_arg=""
    if [ -n "${image_override}" ]; then
        image_arg="--image ${image_override}"
    else
        # Use CoPaw worker image by default
        local copaw_image="${HICLAW_K8S_COPAW_WORKER_IMAGE:-${HICLAW_COPAW_WORKER_IMAGE:-hiclaw/copaw-worker:latest}}"
        image_arg="--image ${copaw_image}"
    fi
    python3 "${K8S_WORKER_API}" k8s-create --name "${worker_name}" --envs "@${envs_file}" ${image_arg} --runtime copaw
    local rc=$?
    rm -f "${envs_file}"
    return ${rc}
}
 
k8s_delete_worker() {
    local worker_name="$1"
    _log "Deleting K8s Pod for worker: ${worker_name}"
    python3 "${K8S_WORKER_API}" k8s-delete --name "${worker_name}"
}
 
k8s_stop_worker() {
    local worker_name="$1"
    _log "Stopping K8s Pod for worker: ${worker_name}"
    python3 "${K8S_WORKER_API}" k8s-stop --name "${worker_name}"
}
 
k8s_start_worker() {
    local worker_name="$1"
    _log "Starting K8s Pod for worker: ${worker_name}"
    python3 "${K8S_WORKER_API}" k8s-start --name "${worker_name}"
}
 
k8s_status_worker() {
    local worker_name="$1"
    local result
    result=$(python3 "${K8S_WORKER_API}" k8s-status --name "${worker_name}" 2>/dev/null)
    echo "${result}" | jq -r '.status // "unknown"' 2>/dev/null
}
 
k8s_list_workers() {
    python3 "${K8S_WORKER_API}" k8s-list
}
 
# ── K8s Worker readiness check ────────────────────────────────────────────────
 
k8s_wait_worker_ready() {
    local worker_name="$1"
    local timeout="${2:-120}"
    _log "Waiting for K8s Worker ${worker_name} to be ready (timeout: ${timeout}s)..."
    python3 "${K8S_WORKER_API}" k8s-wait-ready --name "${worker_name}" --timeout "${timeout}"
}
 
k8s_wait_copaw_worker_ready() {
    local worker_name="$1"
    local timeout="${2:-120}"
    _log "Waiting for K8s CoPaw Worker ${worker_name} to be ready (timeout: ${timeout}s)..."
    python3 "${K8S_WORKER_API}" k8s-wait-ready --name "${worker_name}" --timeout "${timeout}" --runtime copaw
}
 
# ── K8s Pod exec ──────────────────────────────────────────────────────────────
 
k8s_exec_worker() {
    local worker_name="$1"
    shift
    python3 "${K8S_WORKER_API}" k8s-exec --name "${worker_name}" -- "$@"
}