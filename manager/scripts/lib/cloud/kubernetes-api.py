#!/usr/bin/env python3
"""
kubernetes-api.py — Kubernetes (ACK) **Worker Pod** API only.

## What is NOT in this file (same as SAE: use aliyun-api.py)

  - **gw-create-consumer** / **gw-bind-consumer** — AI Gateway Consumer + route/MCP bind.
    Implemented in **aliyun-api.py**; shell entry: **aliyun-sae.sh** → cloud_create_consumer /
    cloud_bind_consumer. **gateway-api.sh** invokes those when HICLAW_RUNTIME=aliyun
    (_detect_gateway_backend → aliyun APIG path). See create-worker.sh Steps 3–5.

  - **sae-create** — SAE application. Only in aliyun-api.py; ACK uses **k8s-create** here instead.

## create-worker.sh (HICLAW_ALIYUN_WORKER_BACKEND=k8s)

  Steps 1–2: Matrix register, 3-party room
  Steps 3–5: gateway_* → **aliyun-api.py** (not kubernetes-api.py)
  Step 6–8: generate config, sync to OSS
  Step 9: **k8s-create** (this file) ↔ aliyun-api.py **sae-create**

Authentication here: Manager → Kubernetes API (in-cluster SA or kubeconfig).

Usage:
  kubernetes-api.py k8s-create …
  kubernetes-api.py k8s-delete …

Output: JSON to stdout. Logs to stderr.
"""

import argparse
import json
import os
import sys
import time

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

def log(msg):
    print(f"[k8s-api] {msg}", file=sys.stderr)


# ---------------------------------------------------------------------------
# Kubernetes client setup
# ---------------------------------------------------------------------------

def _get_k8s_client():
    """Build Kubernetes CoreV1Api client with auto-detected config."""
    from kubernetes import client, config

    # Try In-Cluster config first (ServiceAccount)
    sa_token_path = "/var/run/secrets/kubernetes.io/serviceaccount/token"
    if os.path.isfile(sa_token_path):
        log("Using In-Cluster ServiceAccount config")
        config.load_incluster_config()
    else:
        # Fallback to kubeconfig
        kubeconfig = os.environ.get("KUBECONFIG", os.path.expanduser("~/.kube/config"))
        log(f"Using kubeconfig: {kubeconfig}")
        config.load_kube_config(config_file=kubeconfig)

    return client.CoreV1Api()


def _get_namespace():
    """Get target namespace from env or In-Cluster namespace file."""
    ns = os.environ.get("HICLAW_K8S_NAMESPACE", "")
    if ns:
        return ns

    # Try to read from In-Cluster namespace file
    ns_file = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
    if os.path.isfile(ns_file):
        with open(ns_file, "r") as f:
            return f.read().strip()

    return "default"


def _pod_name(worker_name):
    """Generate Pod name from worker name."""
    return f"hiclaw-worker-{worker_name}"


def _find_worker_pod(api, worker_name):
    """Find a Worker Pod by name. Returns (pod, pod_name) or (None, pod_name)."""
    namespace = _get_namespace()
    pod_name = _pod_name(worker_name)

    try:
        pod = api.read_namespaced_pod(name=pod_name, namespace=namespace)
        return pod, pod_name
    except Exception:
        return None, pod_name


# ---------------------------------------------------------------------------
# K8s Pod operations
# ---------------------------------------------------------------------------

def _get_manager_pod(api, namespace):
    """Find the Manager Pod (the one running this script)."""
    hostname = os.environ.get("HOSTNAME", "")
    if hostname:
        try:
            return api.read_namespaced_pod(name=hostname, namespace=namespace)
        except Exception:
            pass
    # Fallback: find by label
    try:
        pods = api.list_namespaced_pod(
            namespace=namespace,
            label_selector="app=hiclaw-manager"
        )
        if pods.items:
            return pods.items[0]
    except Exception:
        pass
    return None


def _get_manager_tolerations(api, namespace):
    """Read tolerations from the Manager Pod to apply to Worker Pods."""
    pod = _get_manager_pod(api, namespace)
    if pod and pod.spec and pod.spec.tolerations:
        log(f"Inheriting {len(pod.spec.tolerations)} toleration(s) from Manager Pod")
        return pod.spec.tolerations
    return None


def _get_manager_image_pull_secrets(api, namespace):
    """Read imagePullSecrets from the Manager Pod to apply to Worker Pods."""
    pod = _get_manager_pod(api, namespace)
    if pod and pod.spec and pod.spec.image_pull_secrets:
        log(f"Inheriting imagePullSecrets from Manager Pod")
        return pod.spec.image_pull_secrets
    return None


def _worker_manual_rrsa_volume():
    """
    ACK manual RRSA for Worker Pods (same pattern as Aliyun doc — projected token + env).
    Enabled when Manager injects HICLAW_K8S_WORKER_RRSA_ROLE_ARN and ALIBABA_CLOUD_OIDC_PROVIDER_ARN.
    """
    from kubernetes import client

    worker_role = (os.environ.get("HICLAW_K8S_WORKER_RRSA_ROLE_ARN") or "").strip()
    oidc_arn = (os.environ.get("ALIBABA_CLOUD_OIDC_PROVIDER_ARN") or "").strip()
    if not worker_role or not oidc_arn:
        return None, None, None

    mount_path = "/var/run/secrets/ack.alibabacloud.com/rrsa-tokens"
    token_file = f"{mount_path}/token"
    try:
        exp = int(os.environ.get("HICLAW_RRSA_TOKEN_EXPIRATION_SECONDS", "3600"))
    except ValueError:
        exp = 3600

    vol = client.V1Volume(
        name="rrsa-oidc-token",
        projected=client.V1ProjectedVolumeSource(
            default_mode=420,
            sources=[
                client.V1VolumeProjection(
                    service_account_token=client.V1ServiceAccountTokenProjection(
                        audience="sts.aliyuncs.com",
                        expiration_seconds=exp,
                        path="token",
                    )
                )
            ],
        ),
    )
    vm = client.V1VolumeMount(
        name="rrsa-oidc-token",
        mount_path=mount_path,
        read_only=True,
    )
    extra_env = [
        client.V1EnvVar(name="ALIBABA_CLOUD_ROLE_ARN", value=worker_role),
        client.V1EnvVar(name="ALIBABA_CLOUD_OIDC_PROVIDER_ARN", value=oidc_arn),
        client.V1EnvVar(name="ALIBABA_CLOUD_OIDC_TOKEN_FILE", value=token_file),
    ]
    return extra_env, vol, vm


def k8s_create(args):
    """
    ACK Step 9: create Worker Pod (parity with aliyun-api.py sae-create).

    Preconditions: create-worker.sh has completed Matrix, gateway consumer/bind, config, OSS sync.
    Pod uses HICLAW_K8S_WORKER_SERVICE_ACCOUNT.
    RRSA: either ack-pod-identity-webhook (SA annotation) or manual projected token + env
    when HICLAW_K8S_WORKER_RRSA_ROLE_ARN and ALIBABA_CLOUD_OIDC_PROVIDER_ARN are set on the Manager Pod.
    """
    from kubernetes import client

    api = _get_k8s_client()
    namespace = _get_namespace()
    pod_name = _pod_name(args.name)

    # Check if already exists
    existing_pod, _ = _find_worker_pod(api, args.name)
    if existing_pod:
        phase = existing_pod.status.phase if existing_pod.status else "Unknown"
        log(f"Pod already exists: {pod_name} (phase: {phase})")
        print(json.dumps({
            "pod_name": pod_name,
            "status": "exists",
            "phase": phase.lower() if phase else "unknown"
        }))
        return

    # Parse extra envs (supports @/path/to/file or inline JSON)
    envs = {}
    if args.envs:
        raw = args.envs
        if raw.startswith("@"):
            with open(raw[1:], "r") as f:
                raw = f.read()
        envs = json.loads(raw)

    # Read config from environment
    runtime = getattr(args, "runtime", "openclaw") or "openclaw"
    if runtime == "copaw":
        default_image = os.environ.get(
            "HICLAW_K8S_COPAW_WORKER_IMAGE",
            os.environ.get("HICLAW_COPAW_WORKER_IMAGE", "hiclaw/copaw-worker:latest")
        )
    else:
        default_image = os.environ.get(
            "HICLAW_K8S_WORKER_IMAGE",
            os.environ.get("HICLAW_WORKER_IMAGE", "hiclaw/worker-agent:latest")
        )

    image = args.image or default_image
    cpu_limit = os.environ.get("HICLAW_K8S_WORKER_CPU", "1000m")
    memory_limit = os.environ.get("HICLAW_K8S_WORKER_MEMORY", "2Gi")
    service_host = os.environ.get("HICLAW_K8S_SERVICE_HOST", "hiclaw-manager")

    # Base environment variables for worker
    base_envs = {
        "HICLAW_WORKER_NAME": args.name,
        "TZ": os.environ.get("TZ", "Asia/Shanghai"),
    }

    # Cloud Worker (ACK + OSS, same as SAE): env JSON includes HICLAW_MATRIX_URL and HICLAW_RUNTIME=aliyun.
    # Do not inject in-cluster pseudo-MinIO URL on the Manager Service :9000 (that is OpenClaw, not S3).
    cloud_worker = (
        bool(envs.get("HICLAW_MATRIX_URL"))
        or envs.get("HICLAW_RUNTIME") in ("aliyun", "k8s")
    )
    if not cloud_worker:
        # In-cluster all-in-one: Worker reaches MinIO/Matrix/Gateway via Manager Service DNS
        service_dns = f"{service_host}.{namespace}.svc.cluster.local"
        fs_endpoint = f"http://{service_dns}:9000"
        base_envs["HICLAW_FS_ENDPOINT"] = fs_endpoint
        base_envs["HICLAW_FS_ACCESS_KEY"] = envs.get("HICLAW_FS_ACCESS_KEY", args.name)
        base_envs["HICLAW_FS_SECRET_KEY"] = envs.get("HICLAW_FS_SECRET_KEY", "")

    # Merge script-provided envs (cloud: full Matrix / gateway / OSS; local: overrides)
    base_envs.update(envs)

    # Set HOME for openclaw workers
    if runtime != "copaw":
        base_envs["HOME"] = f"/root/hiclaw-fs/agents/{args.name}"

    # Build K8s env list
    env_list = [
        client.V1EnvVar(name=k, value=str(v))
        for k, v in base_envs.items()
        if v is not None and v != ""
    ]

    rrsa_extra_env, rrsa_vol, rrsa_vm = _worker_manual_rrsa_volume()
    if rrsa_extra_env:
        log("Worker Pod: manual RRSA (projected OIDC token + ALIBABA_CLOUD_* env)")
        env_list.extend(rrsa_extra_env)

    # Define resource limits
    resources = client.V1ResourceRequirements(
        limits={"cpu": cpu_limit, "memory": memory_limit},
        requests={"cpu": "100m", "memory": "256Mi"},
    )

    # Working directory based on runtime
    if runtime == "copaw":
        working_dir = "/root/.copaw-worker"
    else:
        working_dir = f"/root/hiclaw-fs/agents/{args.name}"

    # Create Pod spec
    container_kwargs = dict(
        name="worker",
        image=image,
        env=env_list,
        resources=resources,
        working_dir=working_dir,
        image_pull_policy="IfNotPresent",
    )
    if rrsa_vm:
        container_kwargs["volume_mounts"] = [rrsa_vm]

    container = client.V1Container(**container_kwargs)

    worker_sa = (os.environ.get("HICLAW_K8S_WORKER_SERVICE_ACCOUNT") or "").strip()

    pod_kwargs = dict(
        containers=[container],
        restart_policy="Always",
        # Cloud workers: projected SA token (manual RRSA) or webhook-injected OIDC for OSS STS.
        automount_service_account_token=bool(cloud_worker),
        tolerations=_get_manager_tolerations(api, namespace),
        image_pull_secrets=_get_manager_image_pull_secrets(api, namespace),
    )
    if worker_sa:
        pod_kwargs["service_account_name"] = worker_sa
    if rrsa_vol:
        pod_kwargs["volumes"] = [rrsa_vol]

    pod_spec = client.V1PodSpec(**pod_kwargs)

    pod_metadata = client.V1ObjectMeta(
        name=pod_name,
        namespace=namespace,
        labels={
            "app": "hiclaw-worker",
            "hiclaw.io/worker": args.name,
            "hiclaw.io/runtime": runtime,
        },
        annotations={
            "hiclaw.io/created-by": "manager",
        },
    )

    pod = client.V1Pod(
        api_version="v1",
        kind="Pod",
        metadata=pod_metadata,
        spec=pod_spec,
    )

    try:
        api.create_namespaced_pod(namespace=namespace, body=pod)
        log(f"Pod created: {pod_name}")
        print(json.dumps({
            "pod_name": pod_name,
            "namespace": namespace,
            "status": "created"
        }))
    except Exception as e:
        log(f"Failed to create pod: {e}")
        print(json.dumps({"error": str(e)}))
        sys.exit(1)


def k8s_delete(args):
    """Delete a K8s Pod for a Worker."""
    api = _get_k8s_client()
    namespace = _get_namespace()
    pod_name = _pod_name(args.name)

    pod, _ = _find_worker_pod(api, args.name)
    if not pod:
        print(json.dumps({"pod_name": pod_name, "status": "not_found"}))
        return

    try:
        api.delete_namespaced_pod(name=pod_name, namespace=namespace)
        log(f"Pod deleted: {pod_name}")
        print(json.dumps({"pod_name": pod_name, "status": "deleted"}))
    except Exception as e:
        log(f"Failed to delete pod: {e}")
        print(json.dumps({"error": str(e)}))
        sys.exit(1)


def k8s_stop(args):
    """Stop a Worker by deleting its Pod (K8s has no pause, so we delete)."""
    # In K8s, there's no concept of "stopping" a Pod like Docker.
    # We delete the Pod; it can be recreated with k8s-start.
    k8s_delete(args)


def k8s_start(args):
    """Start a Worker by recreating its Pod if not exists."""
    api = _get_k8s_client()
    pod, pod_name = _find_worker_pod(api, args.name)

    if pod:
        phase = pod.status.phase if pod.status else "Unknown"
        if phase.lower() == "running":
            log(f"Pod already running: {pod_name}")
            print(json.dumps({"pod_name": pod_name, "status": "running"}))
            return
        else:
            log(f"Pod exists but not running (phase: {phase}), cannot auto-recreate")
            print(json.dumps({
                "pod_name": pod_name,
                "status": phase.lower() if phase else "unknown",
                "message": "Pod exists but not running. Delete and recreate to restart."
            }))
            return

    # Pod doesn't exist - need to recreate with stored config
    # This requires the caller to provide envs again via k8s-create
    log(f"Pod not found: {pod_name}. Use k8s-create to recreate.")
    print(json.dumps({
        "pod_name": pod_name,
        "status": "not_found",
        "message": "Use k8s-create to recreate the worker pod"
    }))


def k8s_status(args):
    """Check K8s Pod status for a Worker."""
    api = _get_k8s_client()
    pod, pod_name = _find_worker_pod(api, args.name)

    if not pod:
        print(json.dumps({"pod_name": pod_name, "status": "not_found"}))
        return

    phase = pod.status.phase if pod.status else "Unknown"

    # Normalize K8s phase to simpler values matching Docker/SAE
    status_map = {
        "Running": "running",
        "Pending": "starting",
        "Succeeded": "exited",
        "Failed": "exited",
        "Unknown": "unknown",
    }
    normalized = status_map.get(phase, phase.lower() if phase else "unknown")

    # Check container status for more detail
    container_status = None
    if pod.status and pod.status.container_statuses:
        cs = pod.status.container_statuses[0]
        if cs.state:
            if cs.state.running:
                container_status = "running"
            elif cs.state.waiting:
                container_status = f"waiting:{cs.state.waiting.reason or 'unknown'}"
            elif cs.state.terminated:
                container_status = f"terminated:{cs.state.terminated.reason or 'unknown'}"

    print(json.dumps({
        "pod_name": pod_name,
        "status": normalized,
        "k8s_phase": phase,
        "container_status": container_status,
    }))


def k8s_list(args):
    """List all hiclaw-worker Pods."""
    api = _get_k8s_client()
    namespace = _get_namespace()

    pods = api.list_namespaced_pod(
        namespace=namespace,
        label_selector="app=hiclaw-worker"
    )

    workers = []
    for pod in pods.items:
        name = pod.metadata.labels.get("hiclaw.io/worker", "")
        if name:
            phase = pod.status.phase if pod.status else "Unknown"
            workers.append({
                "name": name,
                "pod_name": pod.metadata.name,
                "phase": phase.lower() if phase else "unknown",
                "runtime": pod.metadata.labels.get("hiclaw.io/runtime", "openclaw"),
            })

    print(json.dumps({"workers": workers}))


def k8s_wait_ready(args):
    """Wait for Worker Pod to become ready."""
    from kubernetes.stream import stream

    api = _get_k8s_client()
    namespace = _get_namespace()
    pod_name = _pod_name(args.name)
    timeout = int(args.timeout) if args.timeout else 120
    runtime = getattr(args, "runtime", "openclaw") or "openclaw"

    elapsed = 0
    interval = 5

    log(f"Waiting for pod {pod_name} to be ready (timeout: {timeout}s, runtime: {runtime})...")

    while elapsed < timeout:
        pod, _ = _find_worker_pod(api, args.name)

        if not pod:
            log(f"Pod {pod_name} not found yet...")
            time.sleep(interval)
            elapsed += interval
            continue

        phase = pod.status.phase if pod.status else "Unknown"
        if phase != "Running":
            log(f"Pod {pod_name} not running yet (phase: {phase})...")
            time.sleep(interval)
            elapsed += interval
            continue

        # Pod is running, check if worker agent is ready
        try:
            if runtime == "copaw":
                # CoPaw: check if config.json exists (bridge completed)
                config_path = f"/root/.copaw-worker/{args.name}/.copaw/config.json"
                exec_cmd = ["cat", config_path]
            else:
                # OpenClaw: check gateway health
                exec_cmd = ["openclaw", "gateway", "health", "--json"]

            resp = stream(
                api.connect_get_namespaced_pod_exec,
                pod_name,
                namespace,
                command=exec_cmd,
                container="worker",
                stderr=True,
                stdin=False,
                stdout=True,
                tty=False,
            )

            if runtime == "copaw":
                # Check if response contains channels (CoPaw config)
                if '"channels"' in resp:
                    log(f"CoPaw Worker {args.name} is ready!")
                    print(json.dumps({"pod_name": pod_name, "status": "ready"}))
                    return
            else:
                # Check if response contains "ok" (OpenClaw health)
                if '"ok"' in resp:
                    log(f"Worker {args.name} is ready!")
                    print(json.dumps({"pod_name": pod_name, "status": "ready"}))
                    return

        except Exception as e:
            log(f"Health check failed: {e}")

        time.sleep(interval)
        elapsed += interval
        log(f"Waiting for {args.name}... ({elapsed}s/{timeout}s)")

    log(f"Worker {args.name} did not become ready within {timeout}s")
    print(json.dumps({"pod_name": pod_name, "status": "timeout"}))
    sys.exit(1)


def k8s_exec(args):
    """Execute a command inside a Worker Pod."""
    from kubernetes.stream import stream

    api = _get_k8s_client()
    namespace = _get_namespace()
    pod_name = _pod_name(args.name)

    pod, _ = _find_worker_pod(api, args.name)
    if not pod:
        print(json.dumps({"error": f"Pod not found: {pod_name}"}))
        sys.exit(1)

    try:
        resp = stream(
            api.connect_get_namespaced_pod_exec,
            pod_name,
            namespace,
            command=args.cmd,
            container="worker",
            stderr=True,
            stdin=False,
            stdout=True,
            tty=False,
        )
        print(resp)
    except Exception as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(1)


# ---------------------------------------------------------------------------
# CLI entry point
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="HiClaw K8s Worker API")
    sub = parser.add_subparsers(dest="command")

    # K8s commands
    p = sub.add_parser("k8s-create")
    p.add_argument("--name", required=True)
    p.add_argument("--image")
    p.add_argument("--envs", default="{}")
    p.add_argument("--runtime", default="openclaw", choices=["openclaw", "copaw"])

    p = sub.add_parser("k8s-delete")
    p.add_argument("--name", required=True)

    p = sub.add_parser("k8s-stop")
    p.add_argument("--name", required=True)

    p = sub.add_parser("k8s-start")
    p.add_argument("--name", required=True)

    p = sub.add_parser("k8s-status")
    p.add_argument("--name", required=True)

    sub.add_parser("k8s-list")

    p = sub.add_parser("k8s-wait-ready")
    p.add_argument("--name", required=True)
    p.add_argument("--timeout", default="120")
    p.add_argument("--runtime", default="openclaw", choices=["openclaw", "copaw"])

    p = sub.add_parser("k8s-exec")
    p.add_argument("--name", required=True)
    p.add_argument("cmd", nargs=argparse.REMAINDER, metavar="-- CMD")

    args = parser.parse_args()

    # Handle the '--' separator for exec command
    if args.command == "k8s-exec" and args.cmd:
        if args.cmd[0] == "--":
            args.cmd = args.cmd[1:]

    commands = {
        "k8s-create": k8s_create,
        "k8s-delete": k8s_delete,
        "k8s-stop": k8s_stop,
        "k8s-start": k8s_start,
        "k8s-status": k8s_status,
        "k8s-list": k8s_list,
        "k8s-wait-ready": k8s_wait_ready,
        "k8s-exec": k8s_exec,
    }

    if args.command not in commands:
        parser.print_help()
        sys.exit(1)

    try:
        commands[args.command](args)
    except Exception as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(1)


if __name__ == "__main__":
    main()
