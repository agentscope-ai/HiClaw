#!/usr/bin/env python3
"""
HiClaw Cloud - SAE Application Deployment

Deploy Tuwunel (Matrix Server) and Element Web to SAE.
"""

import time
import json
from typing import Optional, List, Dict, Any
from alibabacloud_sae20190506 import models as sae_models

from .config import config
from .clients import get_sae_client


def create_namespace(
    namespace_id: str = None,
    namespace_name: str = None
) -> str:
    """
    Create a SAE namespace.
    
    Returns: Namespace ID
    """
    sae = get_sae_client()
    
    namespace_id = namespace_id or config.sae_namespace_id
    namespace_name = namespace_name or config.sae_namespace_name
    
    # Check if namespace exists
    namespaces = list_namespaces()
    for ns in namespaces:
        if ns["id"] == namespace_id or ns["name"] == namespace_name:
            print(f"⏭️  Namespace already exists: {ns['id']}")
            return ns["id"]
    
    try:
        req = sae_models.CreateNamespaceRequest(
            namespace_id=namespace_id,
            namespace_name=namespace_name,
            namespace_description=f"HiClaw Cloud namespace"
        )
        
        resp = sae.create_namespace(req)
        
        print(f"✅ Namespace created: {namespace_id}")
        return namespace_id
    except Exception as e:
        if "InstanceExist" in str(e):
            print(f"⏭️  Namespace already exists: {namespace_id}")
            return namespace_id
        raise


def list_namespaces() -> List[Dict[str, Any]]:
    """List all SAE namespaces"""
    sae = get_sae_client()
    
    req = sae_models.DescribeNamespaceListRequest()
    resp = sae.describe_namespace_list(req)
    
    namespaces = []
    if resp.body.data:
        for ns in resp.body.data:
            namespaces.append({
                "id": ns.namespace_id,
                "name": ns.namespace_name
            })
    
    return namespaces


def create_application(
    app_name: str,
    image_url: str,
    port: int,
    cpu: int = 500,
    memory: int = 1024,
    replicas: int = 1,
    envs: Dict[str, str] = None,
    namespace_id: str = None,
    vpc_id: str = None,
    vswitch_id: str = None,
    security_group_id: str = None,
    command: str = None,
    command_args: str = None,
    oidc_role_name: str = None
) -> str:
    """
    Create a SAE application.
    
    Args:
        app_name: Application name
        image_url: Container image URL
        port: Application port
        cpu: CPU in millicores
        memory: Memory in MB
        replicas: Number of replicas
        envs: Environment variables dict
        namespace_id: SAE namespace
        vpc_id: VPC ID
        vswitch_id: vSwitch ID
        security_group_id: Security Group ID
        oidc_role_name: RAM Role name for RRSA OIDC authentication
    
    Returns: Application ID
    """
    sae = get_sae_client()
    
    namespace_id = namespace_id or config.sae_namespace_id
    vpc_id = vpc_id or config.vpc_id
    vswitch_id = vswitch_id or config.vswitch_id
    security_group_id = security_group_id or config.security_group_id
    
    # Check if app exists
    apps = list_applications(namespace_id)
    for app in apps:
        if app["name"] == app_name:
            print(f"⏭️  Application already exists: {app['id']} ({app_name})")
            return app["id"]
    
    # Build environment variables JSON
    env_list = []
    if envs:
        for k, v in envs.items():
            env_list.append({"name": k, "value": v})
    
    req = sae_models.CreateApplicationRequest(
        app_name=app_name,
        namespace_id=namespace_id,
        package_type="Image",
        image_url=image_url,
        cpu=cpu,
        memory=memory,
        replicas=replicas,
        vpc_id=vpc_id,
        v_switch_id=vswitch_id,
        security_group_id=security_group_id,
        app_description=f"HiClaw {app_name}",
        custom_image_network_type="internet"  # Use public network to pull images
    )
    
    if command:
        req.command = command
    if command_args:
        req.command_args = command_args
    if oidc_role_name:
        req.oidc_role_name = oidc_role_name
    
    if env_list:
        req.envs = json.dumps(env_list)
    
    resp = sae.create_application(req)
    app_id = resp.body.data.app_id
    
    print(f"✅ Application created: {app_id} ({app_name})")
    return app_id


def list_applications(namespace_id: str = None) -> List[Dict[str, Any]]:
    """List applications in a namespace"""
    sae = get_sae_client()
    namespace_id = namespace_id or config.sae_namespace_id
    
    req = sae_models.ListApplicationsRequest(
        namespace_id=namespace_id
    )
    
    try:
        resp = sae.list_applications(req)
        
        apps = []
        if resp.body.data and resp.body.data.applications:
            for app in resp.body.data.applications:
                apps.append({
                    "id": app.app_id,
                    "name": app.app_name,
                    "running_instances": app.running_instances
                })
        
        return apps
    except Exception as e:
        if "NotFound" in str(e):
            return []
        raise


def deploy_application(
    app_id: str,
    image_url: str = None,
    command: str = None,
    command_args: str = None,
    envs: Dict[str, str] = None
) -> str:
    """
    Deploy/update an application.
    
    Args:
        app_id: Application ID
        image_url: New image URL (optional)
        command: Startup command override (optional)
        command_args: Startup command args as JSON array string (optional)
        envs: Environment variables dict (optional)
    
    Returns: Change order ID
    """
    sae = get_sae_client()
    
    req = sae_models.DeployApplicationRequest(
        app_id=app_id
    )
    
    if image_url:
        req.image_url = image_url
    if command:
        req.command = command
    if command_args:
        req.command_args = command_args
    if envs:
        env_list = [{"name": k, "value": v} for k, v in envs.items()]
        req.envs = json.dumps(env_list)
    
    resp = sae.deploy_application(req)
    order_id = resp.body.data.change_order_id
    
    print(f"✅ Deployment started: {order_id}")
    return order_id


def start_application(app_id: str) -> str:
    """Start an application"""
    sae = get_sae_client()
    
    req = sae_models.StartApplicationRequest(app_id=app_id)
    resp = sae.start_application(req)
    
    print(f"✅ Application start triggered: {app_id}")
    return resp.body.data.change_order_id


def stop_application(app_id: str) -> str:
    """Stop an application"""
    sae = get_sae_client()
    
    req = sae_models.StopApplicationRequest(app_id=app_id)
    resp = sae.stop_application(req)
    
    print(f"✅ Application stop triggered: {app_id}")
    return resp.body.data.change_order_id


def delete_application(app_id: str) -> bool:
    """Delete an application"""
    sae = get_sae_client()
    
    req = sae_models.DeleteApplicationRequest(app_id=app_id)
    sae.delete_application(req)
    
    print(f"✅ Application deleted: {app_id}")
    return True


def get_application_info(app_id: str) -> Dict[str, Any]:
    """Get application details"""
    sae = get_sae_client()
    
    req = sae_models.DescribeApplicationConfigRequest(app_id=app_id)
    resp = sae.describe_application_config(req)
    
    data = resp.body.data
    return {
        "id": data.app_id,
        "name": data.app_name,
        "namespace_id": data.namespace_id,
        "image_url": data.image_url,
        "cpu": data.cpu,
        "memory": data.memory,
        "replicas": data.replicas,
        "envs": json.loads(data.envs) if data.envs else []
    }


def bind_slb(app_id: str, port: int, target_port: int = None) -> str:
    """
    Bind an SLB (Load Balancer) to expose the application.
    
    Args:
        app_id: Application ID
        port: External port
        target_port: Internal container port (default: same as port)
    
    Returns: SLB address
    """
    sae = get_sae_client()
    target_port = target_port or port
    
    slb_config = json.dumps([{
        "port": port,
        "targetPort": target_port,
        "protocol": "TCP"
    }])
    
    req = sae_models.BindSlbRequest(
        app_id=app_id,
        internet_slb_id="",  # Auto-create
        internet=slb_config
    )
    
    resp = sae.bind_slb(req)
    
    print(f"✅ SLB bound to application: {app_id}")
    return resp.body.data.internet_address if hasattr(resp.body.data, 'internet_address') else ""


def describe_application_nlbs(app_id: str) -> Dict[str, Any]:
    """
    Query NLB instances bound to a SAE application.

    Returns: Dict keyed by NLB ID, e.g.
        {"nlb-xxx": {"dns_name": "nlb-xxx.cn-hangzhou.nlb.aliyuncsslb.com",
                      "listeners": {key: {port, target_port, protocol, status}},
                      "created_by_sae": True}}
    """
    sae = get_sae_client()

    req = sae_models.DescribeApplicationNlbsRequest(app_id=app_id)
    resp = sae.describe_application_nlbs(req)

    result = {}
    data = resp.body.data
    if data and data.instances:
        for nlb_id, inst in data.instances.items():
            listeners = {}
            if inst.listeners:
                for key, l in inst.listeners.items():
                    listeners[key] = {
                        "port": l.port,
                        "target_port": l.target_port,
                        "protocol": l.protocol,
                        "status": l.status,
                    }
            result[nlb_id] = {
                "dns_name": inst.dns_name,
                "listeners": listeners,
                "created_by_sae": inst.created_by_sae,
            }
    return result


def wait_change_order_finished(app_id: str, timeout: int = 300) -> bool:
    """
    Wait until all running change orders for an app finish (status >= 2).

    SAE only allows one change order at a time per application. This must be
    called before bind_nlb / deploy_application if a prior change (e.g.
    create_application) may still be executing.

    Status codes: 0=preparing, 1=executing, 2=succeeded, 3=failed,
                  6=terminated, 10=system error.

    Returns: True if all orders finished successfully (status == 2).
    Raises: RuntimeError if a change order failed.
    """
    sae = get_sae_client()
    print(f"  Waiting for running change orders to finish (app={app_id}) ...")
    start = time.time()
    time.sleep(3)
    while time.time() - start < timeout:
        req = sae_models.ListChangeOrdersRequest(
            app_id=app_id,
            current_page=1,
            page_size=1,
        )
        resp = sae.list_change_orders(req)
        orders = resp.body.data.change_order_list if resp.body.data else []
        if not orders:
            print(f"  No change orders found, proceeding")
            return True

        latest = orders[0]
        status = latest.status
        co_type = latest.co_type or latest.co_type_code or "unknown"
        if status == 2:
            print(f"  ✅ Latest change order finished: {latest.change_order_id} ({co_type})")
            return True
        if status in (3, 6, 10):
            raise RuntimeError(
                f"Change order failed: {latest.change_order_id} "
                f"(type={co_type}, status={status})"
            )
        elapsed = int(time.time() - start)
        print(f"  Change order still running: {co_type} status={status} ({elapsed}s)")
        time.sleep(10)

    raise TimeoutError(f"Change order not finished within {timeout}s for app {app_id}")


def bind_nlb(
    app_id: str,
    port: int,
    target_port: int,
    protocol: str = "TCP",
    address_type: str = "Intranet",
    zone_mappings: List[Dict[str, str]] = None,
) -> str:
    """
    Bind an NLB to a SAE application. SAE auto-creates the NLB instance.

    Idempotent: skips if an NLB with a matching listener already exists.
    Waits for any running change order to finish before binding.

    Args:
        app_id: SAE application ID
        port: NLB listener port
        target_port: Container port
        protocol: TCP / UDP / TCPSSL
        address_type: Intranet (VPC) or Internet
        zone_mappings: List of {"zone_id": "...", "vswitch_id": "..."}
                       Defaults to config.nlb_zone_mappings

    Returns: change_order_id (or empty string if skipped)
    """
    sae = get_sae_client()
    zone_mappings = zone_mappings or config.nlb_zone_mappings

    # Idempotent check
    existing = describe_application_nlbs(app_id)
    for nlb_id, info in existing.items():
        for _, l in info["listeners"].items():
            if l["port"] == port and l["target_port"] == target_port:
                print(f"⏭️  NLB already bound: {nlb_id} (port {port} → {target_port})")
                return ""

    # Wait for any running change order (e.g. create_application) to finish
    wait_change_order_finished(app_id)

    listeners_json = json.dumps([{
        "port": port,
        "TargetPort": target_port,
        "Protocol": protocol,
    }])

    zm_json = json.dumps([{
        "VSwitchId": zm["vswitch_id"],
        "ZoneId": zm["zone_id"],
    } for zm in zone_mappings])

    req = sae_models.BindNlbRequest(
        app_id=app_id,
        address_type=address_type,
        listeners=listeners_json,
        zone_mappings=zm_json,
    )

    resp = sae.bind_nlb(req)
    order_id = resp.body.data.change_order_id if resp.body.data else ""
    print(f"✅ NLB bind started: {app_id} (port {port} → {target_port}), order={order_id}")
    return order_id


def wait_nlb_ready(
    app_id: str,
    expected_port: int,
    timeout: int = 300,
) -> str:
    """
    Wait until the NLB listener for expected_port reaches 'Bounded' status.

    Returns: NLB DNS name
    """
    print(f"  Waiting for NLB (port {expected_port}) to be ready ...")
    start = time.time()
    time.sleep(5)
    while time.time() - start < timeout:
        nlbs = describe_application_nlbs(app_id)
        for nlb_id, info in nlbs.items():
            for _, l in info["listeners"].items():
                if l["port"] == expected_port and l["status"] == "Bounded":
                    dns = info["dns_name"]
                    print(f"  ✅ NLB ready: {dns} (port {expected_port})")
                    return dns
        elapsed = int(time.time() - start)
        print(f"  NLB not ready yet ({elapsed}s)")
        time.sleep(10)
    raise TimeoutError(f"NLB not ready within {timeout}s for port {expected_port}")


def deploy_tuwunel(
    security_group_id: str = None,
    registration_token: str = None,
    server_name: str = None
) -> str:
    """
    Deploy Tuwunel (Matrix Server) to SAE.
    
    Returns: Application ID
    """
    print(f"\n{'='*60}")
    print("Deploying Tuwunel (Matrix Server)")
    print(f"{'='*60}")
    
    security_group_id = security_group_id or config.security_group_id
    registration_token = registration_token or config.matrix_registration_token
    server_name = server_name or config.matrix_server_name
    
    # Environment variables for Tuwunel
    envs = {
        "CONDUWUIT_SERVER_NAME": server_name,
        "CONDUWUIT_ALLOW_REGISTRATION": "true",
        "CONDUWUIT_REGISTRATION_TOKEN": registration_token,
        "CONDUWUIT_ALLOW_GUEST_ACCESS": "false",
        "CONDUWUIT_DATABASE_BACKEND": "rocksdb",
        "CONDUWUIT_DATABASE_PATH": "/data/conduwuit",
        "CONDUWUIT_PORT": str(config.tuwunel_port),
        "CONDUWUIT_ADDRESS": "0.0.0.0",
        "CONDUWUIT_LOG": "info",
        "CONDUWUIT_ALLOW_CHECK_FOR_UPDATES": "false"
    }
    
    # Create namespace first
    create_namespace()
    
    # Create application
    app_id = create_application(
        app_name=config.tuwunel_app_name,
        image_url=config.tuwunel_image,
        port=config.tuwunel_port,
        cpu=config.tuwunel_cpu,
        memory=config.tuwunel_memory,
        replicas=config.tuwunel_replicas,
        envs=envs,
        security_group_id=security_group_id
    )
    
    # Bind NLB for VPC-internal access (used by AI Gateway and Manager)
    bind_nlb(
        app_id=app_id,
        port=config.tuwunel_nlb_port,
        target_port=config.tuwunel_port,
    )
    nlb_dns = wait_nlb_ready(app_id, expected_port=config.tuwunel_nlb_port)
    config.tuwunel_nlb_address = nlb_dns

    print(f"✅ Tuwunel deployed: {app_id} (NLB: {nlb_dns})")
    return app_id


def deploy_element_web(
    security_group_id: str = None,
    matrix_server_url: str = None
) -> str:
    """
    Deploy Element Web (cloud-element image) to SAE.
    
    Uses custom cloud-element image that reads MATRIX_SERVER_URL env var
    to generate config.json at container startup.
    
    Returns: Application ID
    """
    print(f"\n{'='*60}")
    print("Deploying Element Web")
    print(f"{'='*60}")
    
    security_group_id = security_group_id or config.security_group_id
    # Matrix server URL: AI Gateway public address (browser-side, HTTP)
    matrix_server_url = matrix_server_url or f"http://{config.gateway_public_address}"
    
    # Environment variables for cloud-element entrypoint
    envs = {
        "MATRIX_SERVER_URL": matrix_server_url,
        "ELEMENT_BRAND": "HiClaw",
        "ELEMENT_WEB_PORT": str(config.element_port),
    }
    
    # Create namespace (may already exist)
    create_namespace()
    
    # Create application
    app_id = create_application(
        app_name=config.element_app_name,
        image_url=config.element_image,
        port=config.element_port,
        cpu=config.element_cpu,
        memory=config.element_memory,
        replicas=config.element_replicas,
        envs=envs,
        security_group_id=security_group_id
    )
    
    # Bind NLB for VPC-internal access (used by AI Gateway)
    bind_nlb(
        app_id=app_id,
        port=config.element_nlb_port,
        target_port=config.element_port,
    )
    nlb_dns = wait_nlb_ready(app_id, expected_port=config.element_nlb_port)
    config.element_nlb_address = nlb_dns

    print(f"✅ Element Web deployed: {app_id} (NLB: {nlb_dns})")
    return app_id


def deploy_manager(
    security_group_id: str = None,
) -> str:
    """
    Deploy Manager Agent to SAE.

    Manager is a long-running container that:
    - Polls Tuwunel (Matrix) for messages (outbound only)
    - Calls AI Gateway for LLM inference (outbound only)
    - Syncs workspace with OSS via mc (outbound only)
    - No inbound connections needed — no NLB/SLB binding

    Returns: Application ID
    """
    print(f"\n{'='*60}")
    print("Deploying Manager Agent (SAE)")
    print(f"{'='*60}")

    security_group_id = security_group_id or config.security_group_id

    # Matrix URL: Tuwunel NLB private address (VPC internal)
    matrix_url = f"http://{config.tuwunel_nlb_address}:{config.tuwunel_nlb_port}"

    # AI Gateway URL: environment endpoint (accessible from VPC)
    ai_gateway_url = f"http://{config.gateway_env_address}"

    # OSS endpoint: VPC internal for S3-compatible access via mc
    oss_s3_endpoint = f"https://oss-{config.region}-internal.aliyuncs.com"

    # Environment variables for Manager
    envs = {
        # Matrix connection
        "HICLAW_MATRIX_URL": matrix_url,
        "HICLAW_MATRIX_DOMAIN": config.matrix_server_name,
        "HICLAW_REGISTRATION_TOKEN": config.matrix_registration_token,
        "HICLAW_ADMIN_USER": config.admin_user,
        "HICLAW_ADMIN_PASSWORD": config.admin_password,
        "HICLAW_MANAGER_PASSWORD": config.manager_matrix_password,
        # AI Gateway
        "HICLAW_AI_GATEWAY_URL": ai_gateway_url,
        "HICLAW_MANAGER_GATEWAY_KEY": config.manager_consumer_key,
        "HICLAW_DEFAULT_MODEL": config.default_model,
        # OSS (bucket name and endpoint only — credentials via RRSA OIDC)
        "HICLAW_OSS_ENDPOINT": oss_s3_endpoint,
        "HICLAW_OSS_BUCKET": config.oss_bucket_name,
        # Region (needed for STS endpoint in RRSA token refresh)
        "HICLAW_REGION": config.region,
        # General
        "TZ": "Asia/Shanghai",
        "HICLAW_LANGUAGE": config.language,
        "OPENCLAW_MDNS_HOSTNAME": "hiclaw-mgr",  # Override mDNS hostname to avoid 63-byte label limit
        "OPENCLAW_DISABLE_BONJOUR": "1",  # Disable mDNS entirely (not needed in cloud)
    }

    # SAE Worker management — env vars for cloud-worker-api.py
    envs["HICLAW_SAE_WORKER_IMAGE"] = config.worker_image
    envs["HICLAW_SAE_NAMESPACE_ID"] = config.sae_namespace_id
    envs["HICLAW_SAE_VPC_ID"] = config.vpc_id
    envs["HICLAW_SAE_VSWITCH_ID"] = config.vswitch_id
    envs["HICLAW_SAE_SECURITY_GROUP_ID"] = config.security_group_id
    envs["HICLAW_SAE_WORKER_OIDC_ROLE_NAME"] = config.worker_sae_oidc_role_name
    envs["HICLAW_ACCOUNT_ID"] = config.account_id
    # AI Gateway model API binding (for worker consumer authorization)
    envs["HICLAW_GW_MODEL_API_ID"] = config.gw_model_api_id
    envs["HICLAW_GW_ENV_ID"] = config.gw_env_id
    envs["HICLAW_GW_GATEWAY_ID"] = config.gateway_id or ""

    # Create namespace (may already exist)
    create_namespace()

    # Create application with RRSA OIDC
    app_id = create_application(
        app_name=config.manager_sae_app_name,
        image_url=config.manager_sae_image,
        port=config.manager_sae_port,
        cpu=config.manager_sae_cpu,
        memory=config.manager_sae_memory,
        replicas=config.manager_sae_replicas,
        envs=envs,
        security_group_id=security_group_id,
        oidc_role_name=config.manager_sae_oidc_role_name
    )

    print(f"✅ Manager Agent deployed to SAE: {app_id}")
    return app_id

def deploy_worker(
    worker_name: str,
    envs: Dict[str, str] = None,
    security_group_id: str = None,
) -> str:
    """
    Deploy a Worker Agent to SAE.

    Workers are lightweight containers that:
    - Poll Tuwunel (Matrix) for messages (outbound only)
    - Call AI Gateway for LLM inference (outbound only)
    - Sync workspace with OSS via mc using RRSA OIDC (outbound only)
    - No inbound connections needed

    Args:
        worker_name: Worker name (e.g., "alice")
        envs: Environment variables dict for the worker
        security_group_id: Security group ID

    Returns: Application ID
    """
    print(f"\n{'='*60}")
    print(f"Deploying Worker Agent (SAE): {worker_name}")
    print(f"{'='*60}")

    security_group_id = security_group_id or config.security_group_id
    app_name = f"{config.worker_sae_app_name_prefix}{worker_name}"

    create_namespace()

    app_id = create_application(
        app_name=app_name,
        image_url=config.worker_image,
        port=config.worker_sae_port,
        cpu=config.worker_sae_cpu,
        memory=config.worker_sae_memory,
        replicas=config.worker_sae_replicas,
        envs=envs or {},
        security_group_id=security_group_id,
        oidc_role_name=config.worker_sae_oidc_role_name
    )

    print(f"✅ Worker Agent deployed to SAE: {app_id} ({app_name})")
    return app_id



def setup_hiclaw_sae(security_group_id: str = None) -> Dict[str, str]:
    """
    Setup SAE applications for HiClaw: Tuwunel + Element Web (with NLB).

    Manager is NOT deployed here — it depends on AI Gateway being configured
    first (which in turn depends on the NLB addresses produced here).

    Returns: Dict with app IDs and NLB addresses
    """
    print(f"\n{'='*60}")
    print("Setting up HiClaw SAE Applications (Tuwunel + Element Web)")
    print(f"{'='*60}")
    
    # Deploy Tuwunel (includes NLB bind + wait)
    tuwunel_id = deploy_tuwunel(security_group_id)
    
    # Deploy Element Web (includes NLB bind + wait)
    element_id = deploy_element_web(security_group_id)
    
    print(f"\n{'='*60}")
    print("SAE Setup Complete (Tuwunel + Element Web)")
    print(f"{'='*60}")
    print(f"Tuwunel App ID:      {tuwunel_id}")
    print(f"Tuwunel NLB:         {config.tuwunel_nlb_address}:{config.tuwunel_nlb_port}")
    print(f"Element Web App ID:  {element_id}")
    print(f"Element NLB:         {config.element_nlb_address}:{config.element_nlb_port}")
    
    return {
        "tuwunel_app_id": tuwunel_id,
        "element_app_id": element_id,
        "tuwunel_nlb_address": config.tuwunel_nlb_address,
        "tuwunel_nlb_port": config.tuwunel_nlb_port,
        "element_nlb_address": config.element_nlb_address,
        "element_nlb_port": config.element_nlb_port,
    }


# ---------------------------------------------------------------------------
# Destroy helpers
# ---------------------------------------------------------------------------


def delete_namespace(namespace_id: str = None) -> bool:
    """Delete a SAE namespace."""
    sae = get_sae_client()
    namespace_id = namespace_id or config.sae_namespace_id

    try:
        sae.delete_namespace(sae_models.DeleteNamespaceRequest(
            namespace_id=namespace_id,
        ))
        print(f"  ✅ Namespace deleted: {namespace_id}")
        return True
    except Exception as e:
        if "NotFound" in str(e) or "not found" in str(e).lower():
            print(f"  ⏭️  Namespace not found: {namespace_id}")
            return True
        raise


def destroy_hiclaw_sae() -> bool:
    """
    Destroy all HiClaw SAE applications and namespace.

    Deletion order:
    1. Manager app
    2. All worker apps (hiclaw-worker-*)
    3. Element Web app
    4. Tuwunel app
    5. Namespace
    """
    print(f"\n{'='*60}")
    print("Destroying SAE Applications")
    print(f"{'='*60}")

    apps = list_applications()
    if not apps:
        print(f"  ⏭️  No applications found in namespace {config.sae_namespace_id}")
    else:
        # Sort: manager first, then workers, then element, then tuwunel
        hiclaw_apps = []
        for app in apps:
            name = app["name"]
            if name == config.manager_sae_app_name:
                hiclaw_apps.insert(0, app)  # manager first
            elif name.startswith(config.worker_sae_app_name_prefix):
                hiclaw_apps.append(app)
            elif name == config.element_app_name:
                hiclaw_apps.append(app)
            elif name == config.tuwunel_app_name:
                hiclaw_apps.append(app)

        for app in hiclaw_apps:
            print(f"  Deleting {app['name']} ({app['id']}) ...")
            try:
                # Stop first to speed up deletion
                try:
                    stop_application(app["id"])
                except Exception:
                    pass
                # Wait for stop change order to finish before deleting
                try:
                    wait_change_order_finished(app["id"], timeout=120)
                except Exception:
                    pass
                delete_application(app["id"])
                # Wait for delete change order to finish before next app / namespace
                try:
                    wait_change_order_finished(app["id"], timeout=120)
                except Exception:
                    pass
            except Exception as e:
                print(f"  ⚠️  Failed to delete {app['name']}: {e}")

    # Delete namespace
    print(f"  Deleting namespace ...")
    delete_namespace()

    print(f"  ✅ SAE resources destroyed")
    return True


if __name__ == "__main__":
    # List existing namespaces
    namespaces = list_namespaces()
    print(f"Existing namespaces: {len(namespaces)}")
    for ns in namespaces:
        print(f"  - {ns['id']}: {ns['name']}")
