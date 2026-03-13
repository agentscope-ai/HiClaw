#!/usr/bin/env python3
"""
HiClaw Cloud - AI Gateway Setup

Create and configure AI Gateway (LLM type APIs) for HiClaw.
Supports: hiclaw-model-api (OpenAI/v1), tuwunel-element-api (CustomHttp),
          AI consumer with auth binding, gateway logging.
"""

import time
from typing import Optional, List, Dict, Any
from alibabacloud_apig20240327 import models as apig_models

from .config import config
from .clients import get_apig_client


# ---------------------------------------------------------------------------
# Gateway CRUD
# ---------------------------------------------------------------------------

def list_gateways() -> List[Dict[str, Any]]:
    """List all gateways"""
    apig = get_apig_client()
    req = apig_models.ListGatewaysRequest(page_number=1, page_size=100)
    resp = apig.list_gateways(req)
    gateways = []
    if resp.body.data and resp.body.data.items:
        for gw in resp.body.data.items:
            gateways.append({
                "id": gw.gateway_id,
                "name": gw.name,
                "status": gw.status,
                "type": gw.gateway_type,
            })
    return gateways


def create_gateway(
    name: str = None,
    vpc_id: str = None,
    vswitch_id: str = None,
    enable_log: bool = True,
) -> str:
    """Create an AI Gateway with optional SLS logging enabled. Returns gateway ID."""
    apig = get_apig_client()
    name = name or config.gateway_name
    vpc_id = vpc_id or config.vpc_id
    vswitch_id = vswitch_id or config.vswitch_id

    # Check existing
    for gw in list_gateways():
        if gw["name"] == name:
            print(f"[skip] Gateway already exists: {gw['id']} ({name})")
            config.gateway_id = gw["id"]
            return gw["id"]

    print(f"Creating AI Gateway: {name} ...")
    log_config = None
    if enable_log:
        log_config = apig_models.CreateGatewayRequestLogConfig(
            sls=apig_models.CreateGatewayRequestLogConfigSls(enable=True)
        )

    req = apig_models.CreateGatewayRequest(
        name=name,
        gateway_type="AI",
        spec=config.gateway_spec,
        gateway_edition=config.gateway_edition,
        charge_type="POSTPAY",
        vpc_id=vpc_id,
        zone_config=apig_models.CreateGatewayRequestZoneConfig(
            select_option="Auto",
            v_switch_id=vswitch_id,
        ),
        network_access_config=apig_models.CreateGatewayRequestNetworkAccessConfig(
            type="Internet",
        ),
        log_config=log_config,
    )
    resp = apig.create_gateway(req)
    gateway_id = resp.body.data.gateway_id
    config.gateway_id = gateway_id
    print(f"[ok] Gateway created: {gateway_id}")
    print("Waiting for gateway to be ready (3-5 min) ...")
    _wait_gateway_ready(gateway_id)
    return gateway_id


def _wait_gateway_ready(gateway_id: str, timeout: int = 600):
    apig = get_apig_client()
    start = time.time()
    time.sleep(5)
    while time.time() - start < timeout:
        try:
            resp = apig.get_gateway(gateway_id=gateway_id)
            status = resp.body.data.status
            if status == "Running":
                print("[ok] Gateway is running")
                return
            if status in ("Failed", "Deleted"):
                raise RuntimeError(f"Gateway failed: {status}")
            print(f"  status={status} ({int(time.time()-start)}s)")
        except Exception as e:
            if "NotFound" not in str(e):
                raise
            print(f"  waiting ... ({int(time.time()-start)}s)")
        time.sleep(15)
    raise TimeoutError(f"Gateway not ready within {timeout}s")


def get_gateway_info(gateway_id: str = None) -> Dict[str, Any]:
    """Get gateway details including endpoints and default environment."""
    apig = get_apig_client()
    gateway_id = gateway_id or config.gateway_id
    resp = apig.get_gateway(gateway_id=gateway_id)
    data = resp.body.data

    internet_endpoint = None
    for lb in data.load_balancers or []:
        if lb.address_type == "Internet" and lb.address:
            internet_endpoint = lb.address
            break

    env_id = None
    for env in data.environments or []:
        env_id = env.environment_id
        break

    # The public endpoint is the Internet NLB DNS address
    env_endpoint = internet_endpoint

    return {
        "id": data.gateway_id,
        "name": data.name,
        "status": data.status,
        "internet_endpoint": internet_endpoint,
        "default_env_id": env_id,
        "env_endpoint": env_endpoint,
    }


def delete_gateway(gateway_id: str = None) -> bool:
    """Delete a gateway"""
    apig = get_apig_client()
    gateway_id = gateway_id or config.gateway_id
    apig.delete_gateway(gateway_id)
    print(f"[ok] Gateway deleted: {gateway_id}")
    return True

def ensure_wildcard_domain() -> str:
    """Ensure the AI wildcard domain '*' exists. Returns domain_id."""
    apig = get_apig_client()

    # Try to find existing
    req = apig_models.ListDomainsRequest(gateway_type="AI", page_number=1, page_size=100)
    resp = apig.list_domains(req)
    if resp.body.data and resp.body.data.items:
        for d in resp.body.data.items:
            if d.name == "*":
                print(f"[skip] Wildcard domain already exists: {d.domain_id}")
                return d.domain_id

    # Create it
    try:
        req = apig_models.CreateDomainRequest(
            name="*",
            protocol="HTTP",
            gateway_type="AI",
        )
        resp = apig.create_domain(req)
        domain_id = resp.body.data.domain_id
        print(f"[ok] Wildcard domain created: {domain_id}")
        return domain_id
    except Exception as e:
        if "Conflict" in str(e) or "Existed" in str(e):
            # Race condition — re-list
            req = apig_models.ListDomainsRequest(gateway_type="AI", page_number=1, page_size=100)
            resp = apig.list_domains(req)
            if resp.body.data and resp.body.data.items:
                for d in resp.body.data.items:
                    if d.name == "*":
                        return d.domain_id
        raise



# ---------------------------------------------------------------------------
# Service CRUD
# ---------------------------------------------------------------------------

def create_ai_service(
    name: str,
    api_key: str,
    gateway_id: str = None,
    provider: str = "qwen",
    model_name: str = "qwen-plus",
) -> str:
    """Create an AI LLM service. Returns service ID."""
    apig = get_apig_client()
    gateway_id = gateway_id or config.gateway_id

    # Check existing
    existing = _find_service(name, gateway_id)
    if existing:
        print(f"[skip] AI Service already exists: {existing} ({name})")
        return existing

    req = apig_models.CreateServiceRequest(
        gateway_id=gateway_id,
        source_type="AI",
        service_configs=[
            apig_models.CreateServiceRequestServiceConfigs(
                name=name,
                ai_service_config=apig_models.AiServiceConfig(
                    provider=provider,
                    address="https://dashscope.aliyuncs.com/compatible-mode/v1",
                    api_keys=[api_key],
                    default_model_name=model_name,
                    protocols=["OpenAI/v1"],
                    compatible_protocols=["OpenAI/v1", "DashScope", "Anthropic"],
                    enable_health_check=True,
                ),
            )
        ],
    )
    resp = apig.create_service(req)
    svc_id = resp.body.data.service_ids[0] if resp.body.data.service_ids else None
    print(f"[ok] AI Service created: {svc_id} ({name})")
    return svc_id


def create_dns_service(
    name: str,
    address: str,
    port: int,
    gateway_id: str = None,
) -> str:
    """Create a DNS-based backend service. Returns service ID."""
    apig = get_apig_client()
    gateway_id = gateway_id or config.gateway_id

    existing = _find_service(name, gateway_id)
    if existing:
        print(f"[skip] DNS Service already exists: {existing} ({name})")
        return existing

    req = apig_models.CreateServiceRequest(
        gateway_id=gateway_id,
        source_type="DNS",
        service_configs=[
            apig_models.CreateServiceRequestServiceConfigs(
                name=name,
                addresses=[f"{address}:{port}"],
            )
        ],
    )
    resp = apig.create_service(req)
    svc_id = resp.body.data.service_ids[0] if resp.body.data.service_ids else None
    print(f"[ok] DNS Service created: {svc_id} ({name})")
    return svc_id


def _find_service(name: str, gateway_id: str) -> Optional[str]:
    apig = get_apig_client()
    req = apig_models.ListServicesRequest(gateway_id=gateway_id, page_number=1, page_size=100)
    resp = apig.list_services(req)
    if resp.body.data and resp.body.data.items:
        for svc in resp.body.data.items:
            if svc.name == name:
                return svc.service_id
    return None


# ---------------------------------------------------------------------------
# LLM-type HTTP API CRUD
# ---------------------------------------------------------------------------


def create_model_api(
    name: str,
    ai_service_id: str,
    gateway_id: str = None,
    environment_id: str = None,
    domain_id: str = None,
    enable_statistics: bool = True,
) -> str:
    """
    Create an LLM API with OpenAI/v1 protocol (hiclaw-model-api style).
    The API auto-generates builtin routes when auto_deploy=True.
    Returns HTTP API ID.
    """
    apig = get_apig_client()
    gateway_id = gateway_id or config.gateway_id

    existing = _find_llm_api(name, gateway_id)
    if existing:
        print(f"[skip] Model API already exists: {existing} ({name})")
        return existing

    # Build deploy config with AiStatistics enabled
    policy_configs = []
    if enable_statistics:
        policy_configs.append(
            apig_models.HttpApiDeployConfigPolicyConfigs(
                type="AiStatistics",
                enable=True,
                ai_statistics_config=apig_models.HttpApiDeployConfigPolicyConfigsAiStatisticsConfig(
                    log_request_content=False,
                    log_response_content=False,
                ),
            )
        )

    deploy_configs = None
    if environment_id:
        deploy_configs = [
            apig_models.HttpApiDeployConfig(
                environment_id=environment_id,
                gateway_id=gateway_id,
                gateway_type="AI",
                backend_scene="SingleService",
                auto_deploy=True,
                custom_domain_ids=[domain_id] if domain_id else None,
                service_configs=[
                    apig_models.HttpApiDeployConfigServiceConfigs(
                        service_id=ai_service_id,
                        weight=100,
                    )
                ],
                policy_configs=policy_configs if policy_configs else None,
            )
        ]

    req = apig_models.CreateHttpApiRequest(
        belong_gateway_id=gateway_id,
        name=name,
        type="LLM",
        ai_protocols=["OpenAI/v1"],
        model_category="Text",
        protocols=["HTTP", "HTTPS"],
        base_path="/",
        deploy_configs=deploy_configs,
        enable_auth=True,
        auth_config=apig_models.AuthConfig(
            auth_type="Apikey",
            auth_mode="Custom",
        ),
    )
    resp = apig.create_http_api(req)
    api_id = resp.body.data.http_api_id
    print(f"[ok] Model API created: {api_id} ({name})")
    return api_id




def create_custom_http_api(
    name: str,
    gateway_id: str = None,
    environment_id: str = None,
    domain_id: str = None,
) -> str:
    """
    Create an LLM API with CustomHttp protocol (tuwunel-element-api style).
    Routes are added separately.
    Returns HTTP API ID.
    """
    apig = get_apig_client()
    gateway_id = gateway_id or config.gateway_id

    existing = _find_llm_api(name, gateway_id)
    if existing:
        print(f"[skip] Custom HTTP API already exists: {existing} ({name})")
        return existing

    deploy_configs = None
    if environment_id:
        deploy_configs = [
            apig_models.HttpApiDeployConfig(
                environment_id=environment_id,
                gateway_id=gateway_id,
                gateway_type="AI",
                auto_deploy=True,
                custom_domain_ids=[domain_id] if domain_id else None,
            )
        ]

    req = apig_models.CreateHttpApiRequest(
        belong_gateway_id=gateway_id,
        name=name,
        type="LLM",
        ai_protocols=["CustomHttp"],
        model_category="Others",
        protocols=["HTTP", "HTTPS"],
        base_path="/",
        deploy_configs=deploy_configs,
    )
    resp = apig.create_http_api(req)
    api_id = resp.body.data.http_api_id
    print(f"[ok] Custom HTTP API created: {api_id} ({name})")
    return api_id



def _find_llm_api(name: str, gateway_id: str) -> Optional[str]:
    """Find an LLM/MCP type API by name under a gateway."""
    apig = get_apig_client()
    req = apig_models.ListHttpApisRequest(gateway_type="AI", page_number=1, page_size=100)
    resp = apig.list_http_apis(req)
    if resp.body.data and resp.body.data.items:
        for api in resp.body.data.items:
            api_map = api.to_map()
            for v in api_map.get("versionedHttpApis", []):
                if v.get("name") == name and v.get("gatewayId") == gateway_id:
                    return v.get("httpApiId")
    return None


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------


def create_route(
    http_api_id: str,
    name: str,
    path: str,
    service_id: str,
    environment_id: str,
    domain_ids: List[str] = None,
    path_type: str = "Prefix",
    methods: List[str] = None,
) -> str:
    """Create a route on an HTTP API. Returns route ID."""
    apig = get_apig_client()

    # Check existing
    existing = _find_route(http_api_id, name)
    if existing:
        print(f"[skip] Route already exists: {existing} ({name})")
        return existing

    match_config = apig_models.HttpRouteMatch(
        path=apig_models.HttpRouteMatchPath(type=path_type, value=path),
    )
    if methods:
        match_config.methods = methods

    req = apig_models.CreateHttpApiRouteRequest(
        name=name,
        environment_id=environment_id,
        domain_ids=domain_ids,
        match=match_config,
        backend_config=apig_models.CreateHttpApiRouteRequestBackendConfig(
            scene="SingleService",
            services=[
                apig_models.CreateHttpApiRouteRequestBackendConfigServices(
                    service_id=service_id,
                    weight=100,
                )
            ],
        ),
    )
    resp = apig.create_http_api_route(http_api_id, req)
    route_id = resp.body.data.route_id
    print(f"[ok] Route created: {route_id} ({name} -> {path})")
    return route_id



def _find_route(http_api_id: str, name: str) -> Optional[str]:
    apig = get_apig_client()
    req = apig_models.ListHttpApiRoutesRequest(page_number=1, page_size=100)
    resp = apig.list_http_api_routes(http_api_id, req)
    if resp.body.data and resp.body.data.items:
        for r in resp.body.data.items:
            if r.name == name:
                return r.route_id
    return None


# ---------------------------------------------------------------------------
# Deploy
# ---------------------------------------------------------------------------


def deploy_api(http_api_id: str, gateway_id: str = None, route_ids: List[str] = None):
    """Deploy an HTTP API by deploying each route individually.
    If route_ids is None, auto-discovers all routes on the API.
    """
    apig = get_apig_client()
    gateway_id = gateway_id or config.gateway_id

    # Auto-discover routes if not provided
    if route_ids is None:
        req = apig_models.ListHttpApiRoutesRequest(page_number=1, page_size=100)
        resp = apig.list_http_api_routes(http_api_id, req)
        route_ids = []
        if resp.body.data and resp.body.data.items:
            for r in resp.body.data.items:
                route_ids.append(r.route_id)
        if not route_ids:
            print(f"[warn] No routes found for API {http_api_id}, skipping deploy")
            return

    # Deploy each route individually (API only supports single route_id per call)
    for rid in route_ids:
        req = apig_models.DeployHttpApiRequest(route_id=rid)
        apig.deploy_http_api(http_api_id, req)
        print(f"[ok] Route deployed: {rid}")

    print(f"[ok] API deployed: {http_api_id} ({len(route_ids)} routes)")


def _wait_api_stable(http_api_id: str, gateway_id: str, timeout: int = 120):
    """Wait until an API is no longer in a changing/deploying state."""
    apig = get_apig_client()
    start = time.time()
    time.sleep(5)
    while time.time() - start < timeout:
        try:
            resp = apig.get_http_api(http_api_id)
            d = resp.body.data.to_map()
            envs = d.get("environments", [])
            # Check if any env is still deploying
            still_changing = False
            for env in envs:
                status = env.get("deployStatus", "")
                if status in ("Deploying", "Changing"):
                    still_changing = True
                    break
            if not still_changing:
                print(f"[ok] API {http_api_id} is stable")
                return
            print(f"  API still deploying ({int(time.time()-start)}s)")
        except Exception as e:
            if "Conflict" in str(e):
                print(f"  API still changing ({int(time.time()-start)}s)")
            else:
                raise
        time.sleep(10)
    # Timeout is not fatal — proceed anyway
    print(f"[warn] API stability wait timed out after {timeout}s, proceeding")



# ---------------------------------------------------------------------------
# Consumer
# ---------------------------------------------------------------------------

def create_ai_consumer(
    name: str,
    description: str = None,
    gateway_id: str = None,
) -> Dict[str, str]:
    """Create an AI-type consumer with ApiKey auth. Returns {consumer_id, api_key}.

    Consumer name is prefixed with a short gateway ID (first 8 chars) to avoid
    account-level name collisions across gateways.
    """
    apig = get_apig_client()
    gateway_id = gateway_id or config.gateway_id

    # Prefix consumer name with gateway ID to avoid cross-gateway collisions
    if gateway_id:
        consumer_name = f"{gateway_id}-{name}"
    else:
        print(f"[warn] gateway_id not set, using raw consumer name: {name}")
        consumer_name = name

    # Check existing (use name_like to narrow search)
    req = apig_models.ListConsumersRequest(gateway_type="AI", name_like=consumer_name, page_number=1, page_size=100)
    resp = apig.list_consumers(req)
    if resp.body.data and resp.body.data.items:
        for c in resp.body.data.items:
            if c.name == consumer_name:
                detail = apig.get_consumer(c.consumer_id)
                d = detail.body.data
                key = None
                if d.api_key_identity_config and d.api_key_identity_config.credentials:
                    key = d.api_key_identity_config.credentials[0].apikey
                print(f"[skip] AI Consumer already exists: {c.consumer_id} ({consumer_name})")
                return {"consumer_id": c.consumer_id, "api_key": key}

    req = apig_models.CreateConsumerRequest(
        name=consumer_name,
        gateway_type="AI",
        enable=True,
        description=description or f"HiClaw {name} consumer",
        apikey_identity_config=apig_models.ApiKeyIdentityConfig(
            type="Apikey",
            apikey_source=apig_models.ApiKeyIdentityConfigApikeySource(
                source="Default",
                value="Authorization",
            ),
            credentials=[
                apig_models.ApiKeyIdentityConfigCredentials(generate_mode="System")
            ],
        ),
    )
    resp = apig.create_consumer(req)
    consumer_id = resp.body.data.consumer_id

    # Retrieve generated key
    detail = apig.get_consumer(consumer_id)
    key = None
    if detail.body.data.api_key_identity_config and detail.body.data.api_key_identity_config.credentials:
        key = detail.body.data.api_key_identity_config.credentials[0].apikey

    print(f"[ok] AI Consumer created: {consumer_id} ({consumer_name}), key={key}")
    return {"consumer_id": consumer_id, "api_key": key}


# ---------------------------------------------------------------------------
# Auth — enable consumer auth on an API
# ---------------------------------------------------------------------------



def enable_api_auth(http_api_id: str, retries: int = 3):
    """Enable consumer authentication on an HTTP API (with retry for transient errors)."""
    apig = get_apig_client()
    req = apig_models.UpdateHttpApiRequest(
        enable_auth=True,
        auth_config=apig_models.AuthConfig(
            auth_type="Apikey",
            auth_mode="Custom",
        ),
    )
    for attempt in range(1, retries + 1):
        try:
            apig.update_http_api(http_api_id, req)
            print(f"[ok] Auth enabled on API: {http_api_id}")
            return
        except Exception as e:
            if attempt < retries and ("500" in str(e) or "InternalError" in str(e) or "Conflict" in str(e)):
                print(f"[retry] enable_api_auth attempt {attempt} failed: {e}")
                time.sleep(10)
            else:
                raise



def bind_consumer_to_api(
    consumer_id: str,
    http_api_id: str,
    environment_id: str,
) -> List[str]:
    """
    Bind a consumer to an HTTP API via authorization rule (API-level, resourceType=LLM).
    Returns list of authorization rule IDs.
    """
    apig = get_apig_client()

    # Check if already bound at API level
    try:
        req = apig_models.QueryConsumerAuthorizationRulesRequest(
            consumer_id=consumer_id,
            resource_id=http_api_id,
            environment_id=environment_id,
            resource_type="LLM",
            page_number=1,
            page_size=100,
        )
        resp = apig.query_consumer_authorization_rules(req)
        if resp.body.data and resp.body.data.items and len(resp.body.data.items) > 0:
            existing_ids = [r.consumer_authorization_rule_id for r in resp.body.data.items]
            print(f"[skip] Consumer already bound to API: {len(existing_ids)} rules")
            return existing_ids
    except Exception:
        pass  # Query may fail if no rules exist, proceed to create

    # Create API-level authorization rule
    req = apig_models.CreateConsumerAuthorizationRulesRequest(
        authorization_rules=[
            apig_models.CreateConsumerAuthorizationRulesRequestAuthorizationRules(
                consumer_id=consumer_id,
                resource_type="LLM",
                expire_mode="LongTerm",
                resource_identifier=apig_models.CreateConsumerAuthorizationRulesRequestAuthorizationRulesResourceIdentifier(
                    resource_id=http_api_id,
                    environment_id=environment_id,
                ),
            )
        ],
    )
    resp = apig.create_consumer_authorization_rules(req)
    rule_ids = resp.body.data.consumer_authorization_rule_ids or []
    print(f"[ok] Consumer bound to API: {len(rule_ids)} authorization rule(s) created")
    return rule_ids





# ---------------------------------------------------------------------------
# Verification
# ---------------------------------------------------------------------------

def verify_gateway_resources(
    gateway_id: str,
    model_api_id: str,
    custom_api_id: str,
    consumer_id: str,
) -> Dict[str, Any]:
    """Verify all gateway resources are created and deployed correctly."""
    apig = get_apig_client()
    results = {"ok": True, "errors": []}

    # 1. Check gateway running
    try:
        resp = apig.get_gateway(gateway_id=gateway_id)
        if resp.body.data.status != "Running":
            results["errors"].append(f"Gateway not running: {resp.body.data.status}")
            results["ok"] = False
        else:
            print("[verify] Gateway: Running")
    except Exception as e:
        results["errors"].append(f"Gateway check failed: {e}")
        results["ok"] = False

    # 2. Check model API and its routes
    try:
        resp = apig.get_http_api(model_api_id)
        d = resp.body.data.to_map()
        envs = d.get("environments", [])
        deployed = any(e.get("deployStatus") == "Deployed" for e in envs)
        if deployed:
            print(f"[verify] Model API ({model_api_id}): Deployed")
        else:
            results["errors"].append(f"Model API not deployed")
            results["ok"] = False

        # Check routes
        req = apig_models.ListHttpApiRoutesRequest(page_number=1, page_size=100)
        route_resp = apig.list_http_api_routes(model_api_id, req)
        route_count = len(route_resp.body.data.items) if route_resp.body.data and route_resp.body.data.items else 0
        if route_count >= 3:
            print(f"[verify] Model API routes: {route_count} (expected >=3)")
        else:
            results["errors"].append(f"Model API routes: {route_count} (expected >=3)")
            results["ok"] = False
    except Exception as e:
        results["errors"].append(f"Model API check failed: {e}")
        results["ok"] = False

    # 3. Check custom HTTP API and its routes
    try:
        resp = apig.get_http_api(custom_api_id)
        d = resp.body.data.to_map()
        envs = d.get("environments", [])
        deployed = any(e.get("deployStatus") == "Deployed" for e in envs)
        if deployed:
            print(f"[verify] Custom API ({custom_api_id}): Deployed")
        else:
            results["errors"].append(f"Custom API not deployed")
            results["ok"] = False

        req = apig_models.ListHttpApiRoutesRequest(page_number=1, page_size=100)
        route_resp = apig.list_http_api_routes(custom_api_id, req)
        route_count = len(route_resp.body.data.items) if route_resp.body.data and route_resp.body.data.items else 0
        if route_count >= 2:
            print(f"[verify] Custom API routes: {route_count} (expected >=2)")
        else:
            results["errors"].append(f"Custom API routes: {route_count} (expected >=2)")
            results["ok"] = False
    except Exception as e:
        results["errors"].append(f"Custom API check failed: {e}")
        results["ok"] = False

    # 4. Check consumer
    try:
        resp = apig.get_consumer(consumer_id)
        if resp.body.data.enable:
            print(f"[verify] Consumer ({consumer_id}): Enabled")
        else:
            results["errors"].append("Consumer not enabled")
            results["ok"] = False
    except Exception as e:
        results["errors"].append(f"Consumer check failed: {e}")
        results["ok"] = False

    if results["ok"]:
        print("[verify] All checks passed")
    else:
        print(f"[verify] FAILED: {results['errors']}")

    return results


# ---------------------------------------------------------------------------
# Main orchestrator
# ---------------------------------------------------------------------------


def create_hiclaw_gateway_instance(
    gateway_name: str = None,
) -> Dict[str, Any]:
    """
    Phase 1: Create the AI Gateway instance and wait for it to be ready.

    This is separated from route/service configuration because the NLB
    addresses (needed for DNS services) are not available until SAE apps
    are deployed and NLBs are bound.

    Returns: {"gateway_id", "environment_id", "domain_id"}
    """
    gateway_name = gateway_name or config.gateway_name

    print(f"\n{'='*60}")
    print(f"Creating HiClaw AI Gateway instance: {gateway_name}")
    print(f"{'='*60}")

    gateway_id = create_gateway(name=gateway_name, enable_log=True)

    gw_info = get_gateway_info(gateway_id)
    env_id = gw_info["default_env_id"]
    if not env_id:
        raise RuntimeError("No default environment found on gateway")
    print(f"[info] Default environment: {env_id}")

    domain_id = ensure_wildcard_domain()
    print(f"[info] Wildcard domain: {domain_id}")

    return {
        "gateway_id": gateway_id,
        "environment_id": env_id,
        "domain_id": domain_id,
        "internet_endpoint": gw_info.get("internet_endpoint", ""),
    }


def configure_hiclaw_gateway(
    gateway_id: str,
    env_id: str,
    domain_id: str,
    tuwunel_address: str,
    tuwunel_port: int,
    element_address: str,
    element_port: int,
) -> Dict[str, Any]:
    """
    Phase 2: Configure services, APIs, routes, consumer on an existing gateway.

    Called after SAE apps are deployed and NLB addresses are known.

    Returns: Full gateway info dict
    """
    print(f"\n{'='*60}")
    print(f"Configuring HiClaw AI Gateway: {gateway_id}")
    print(f"{'='*60}")

    # Create services
    ai_svc_id = None
    if config.llm_api_key:
        ai_svc_id = create_ai_service(
            name="hiclaw-qwen",
            api_key=config.llm_api_key,
            gateway_id=gateway_id,
            provider="qwen",
            model_name=config.default_model,
        )

    tuwunel_svc_id = create_dns_service(
        name="tuwunel",
        address=tuwunel_address,
        port=tuwunel_port,
        gateway_id=gateway_id,
    )

    element_svc_id = create_dns_service(
        name="element-web",
        address=element_address,
        port=element_port,
        gateway_id=gateway_id,
    )

    # Create hiclaw-model-api
    model_api_id = None
    if ai_svc_id:
        model_api_id = create_model_api(
            name="hiclaw-model-api",
            ai_service_id=ai_svc_id,
            gateway_id=gateway_id,
            environment_id=env_id,
            domain_id=domain_id,
            enable_statistics=True,
        )
        print("[info] Waiting for model API deployment to settle ...")
        _wait_api_stable(model_api_id, gateway_id)

    # Create tuwunel-element-api
    custom_api_id = create_custom_http_api(
        name="tuwunel-element-api",
        gateway_id=gateway_id,
        environment_id=env_id,
        domain_id=domain_id,
    )

    # Create routes
    matrix_route_id = create_route(
        http_api_id=custom_api_id,
        name="matrix",
        path="/_matrix",
        service_id=tuwunel_svc_id,
        environment_id=env_id,
        domain_ids=[domain_id],
    )

    element_route_id = create_route(
        http_api_id=custom_api_id,
        name="element-web",
        path="/",
        service_id=element_svc_id,
        environment_id=env_id,
        domain_ids=[domain_id],
    )

    # Deploy custom API routes
    _wait_api_stable(custom_api_id, gateway_id)
    deploy_api(custom_api_id, gateway_id=gateway_id)

    # Create AI consumer
    consumer = create_ai_consumer(name="hiclaw-manager")

    # Bind consumer to model API
    if model_api_id:
        bind_consumer_to_api(
            consumer_id=consumer["consumer_id"],
            http_api_id=model_api_id,
            environment_id=env_id,
        )

    # Verify
    print(f"\n{'='*60}")
    print("Verifying resources ...")
    print(f"{'='*60}")
    time.sleep(5)
    verification = verify_gateway_resources(
        gateway_id=gateway_id,
        model_api_id=model_api_id,
        custom_api_id=custom_api_id,
        consumer_id=consumer["consumer_id"],
    )

    # Summary
    gw_info = get_gateway_info(gateway_id)
    endpoint = gw_info.get("env_endpoint", "N/A")

    # Write back to config so downstream steps (deploy_manager) see fresh values
    config.gw_model_api_id = model_api_id or ""
    config.gw_env_id = env_id
    config.gateway_env_address = endpoint
    config.gateway_public_address = endpoint
    config.manager_consumer_key = consumer["api_key"] or ""

    print(f"\n{'='*60}")
    print("Gateway Configuration Complete")
    print(f"{'='*60}")
    print(f"Gateway ID:    {gateway_id}")
    print(f"Environment:   {env_id}")
    print(f"Endpoint:      {endpoint}")
    print(f"Model API:     {model_api_id}")
    print(f"Custom API:    {custom_api_id}")
    print(f"Consumer:      {consumer['consumer_id']}")
    print(f"Consumer Key:  {consumer['api_key']}")
    print(f"Verification:  {'PASS' if verification['ok'] else 'FAIL'}")

    return {
        "gateway_id": gateway_id,
        "environment_id": env_id,
        "endpoint": endpoint,
        "domain_id": domain_id,
        "ai_service_id": ai_svc_id,
        "tuwunel_service_id": tuwunel_svc_id,
        "element_service_id": element_svc_id,
        "model_api_id": model_api_id,
        "custom_api_id": custom_api_id,
        "consumer_id": consumer["consumer_id"],
        "consumer_key": consumer["api_key"],
        "verification": verification,
    }


def setup_hiclaw_gateway(
    gateway_name: str = None,
    tuwunel_address: str = None,
    tuwunel_port: int = None,
    element_address: str = None,
    element_port: int = None,
) -> Dict[str, Any]:
    """
    Full gateway setup (backward-compatible entry point).

    Combines create_hiclaw_gateway_instance + configure_hiclaw_gateway.
    Requires NLB addresses to be set in config or passed as arguments.
    """
    tuwunel_address = tuwunel_address or config.tuwunel_nlb_address
    tuwunel_port = tuwunel_port or config.tuwunel_nlb_port
    element_address = element_address or config.element_nlb_address
    element_port = element_port or config.element_nlb_port

    gw = create_hiclaw_gateway_instance(gateway_name)

    return configure_hiclaw_gateway(
        gateway_id=gw["gateway_id"],
        env_id=gw["environment_id"],
        domain_id=gw["domain_id"],
        tuwunel_address=tuwunel_address,
        tuwunel_port=tuwunel_port,
        element_address=element_address,
        element_port=element_port,
    )



if __name__ == "__main__":
    gateways = list_gateways()
    print(f"Existing gateways: {len(gateways)}")
    for gw in gateways:
        print(f"  - {gw['id']}: {gw['name']} ({gw['status']}, type={gw['type']})")
