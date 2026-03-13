#!/usr/bin/env python3
"""
HiClaw Cloud - Centralized Configuration

All configuration for cloud deployment. Can be overridden by environment variables.

Required environment variables:
  ALIBABA_CLOUD_ACCESS_KEY_ID      - Alibaba Cloud Access Key ID
  ALIBABA_CLOUD_ACCESS_KEY_SECRET  - Alibaba Cloud Access Key Secret
  HICLAW_LLM_API_KEY               - LLM provider API key (e.g. Qwen)
  HICLAW_RRSA_OIDC_PROVIDER_ARN   - OIDC Provider ARN (from SAE RRSA)

Optional environment variables:
  HICLAW_REGION                    - Region (default: cn-hangzhou)
  HICLAW_REGISTRATION_TOKEN        - Matrix registration token (auto-generated if not set)
  HICLAW_ADMIN_PASSWORD            - Admin password (auto-generated if not set)
  HICLAW_MANAGER_PASSWORD          - Manager Matrix password (auto-generated if not set)
"""

import os
import secrets
import string
from dataclasses import dataclass, field
from typing import Optional


def _generate_password(length: int = 16) -> str:
    """Generate a secure random password with mixed characters."""
    alphabet = string.ascii_letters + string.digits
    return ''.join(secrets.choice(alphabet) for _ in range(length))


@dataclass
class CloudConfig:
    """Alibaba Cloud deployment configuration"""

    # Credentials (from environment, no hardcoded defaults)
    access_key_id: str = field(default_factory=lambda: os.environ.get(
        "ALIBABA_CLOUD_ACCESS_KEY_ID", ""))
    access_key_secret: str = field(default_factory=lambda: os.environ.get(
        "ALIBABA_CLOUD_ACCESS_KEY_SECRET", ""))

    # Account info (auto-populated by deploy_all from STS GetCallerIdentity)
    account_id: str = ""

    # Region and network (populated by setup_hiclaw_vpc or set via env)
    region: str = field(default_factory=lambda: os.environ.get(
        "HICLAW_REGION", "cn-hangzhou"))
    vpc_id: str = ""
    vswitch_id: str = ""      # primary vSwitch (zone 1)
    vswitch_id_alt: str = ""  # secondary vSwitch (zone 2)
    zone_id: str = ""         # primary zone (populated at deploy time)

    # VPC creation parameters
    vpc_name: str = "hiclaw-cloud-vpc"
    vpc_cidr: str = "10.0.0.0/16"
    vswitch_cidr_1: str = "10.0.0.0/24"   # zone 1
    vswitch_cidr_2: str = "10.0.1.0/24"   # zone 2

    # NAT Gateway
    nat_gateway_name: str = "hiclaw-cloud-nat"
    nat_gateway_id: Optional[str] = None
    eip_bandwidth: int = 100  # Mbps

    # Naming convention
    project_name: str = "hiclaw-cloud"

    # Security Group
    security_group_name: str = field(default_factory=lambda: "hiclaw-cloud-sg")
    security_group_id: Optional[str] = None

    # OSS (bucket name includes account_id suffix for global uniqueness; endpoints computed in __post_init__)
    oss_bucket_name: str = "hiclaw-cloud"  # will be updated in deploy_all after account_id is known
    oss_endpoint: str = ""
    oss_internal_endpoint: str = ""

    # AI Gateway
    gateway_name: str = "hiclaw-cloud"
    gateway_id: Optional[str] = None  # populated by create_hiclaw_gateway_instance
    gateway_spec: str = "aigw.small.x1"  # Smallest spec
    gateway_edition: str = "Professional"  # Gateway edition

    # SAE (namespace_id computed in __post_init__ based on region)
    sae_namespace_id: str = ""
    sae_namespace_name: str = "hiclaw-cloud"

    # Tuwunel (Matrix Server) SAE Application
    tuwunel_app_name: str = "hiclaw-tuwunel"
    tuwunel_image: str = "registry.cn-hangzhou.aliyuncs.com/hiclaw-cloud/tuwunel:20260216"
    tuwunel_port: int = 6167
    tuwunel_cpu: int = 500  # millicores
    tuwunel_memory: int = 1024  # MB
    tuwunel_replicas: int = 1

    # Element Web SAE Application
    element_app_name: str = "hiclaw-element"
    element_image: str = "registry.cn-hangzhou.aliyuncs.com/hiclaw-cloud/cloud-element:latest"
    element_port: int = 8080
    element_cpu: int = 500  # SAE minimum is 500m
    element_memory: int = 1024  # SAE minimum is 1024MB
    element_replicas: int = 1

    # Manager Agent SAE Application
    manager_sae_app_name: str = "hiclaw-manager"
    manager_sae_image: str = "registry.cn-hangzhou.aliyuncs.com/hiclaw-cloud/hiclaw-manager:latest"
    manager_sae_cpu: int = 1000  # 1 vCPU
    manager_sae_memory: int = 2048  # 2GB
    manager_sae_replicas: int = 1
    manager_sae_port: int = 9000  # OpenClaw gateway port (internal only, for health check)
    manager_sae_oidc_role_name: str = "hiclaw-manager-role"  # RAM Role for RRSA OIDC

    # RRSA OIDC (must be set via environment variable — created by SAE when RRSA is first enabled)
    rrsa_oidc_provider_arn: str = field(default_factory=lambda: os.environ.get(
        "HICLAW_RRSA_OIDC_PROVIDER_ARN", ""))

    # Worker Agent SAE Application
    worker_image: str = "registry.cn-hangzhou.aliyuncs.com/hiclaw-cloud/hiclaw-worker:latest"
    worker_sae_app_name_prefix: str = "hiclaw-worker-"
    worker_sae_cpu: int = 500  # millicores
    worker_sae_memory: int = 1024  # MB
    worker_sae_replicas: int = 1
    worker_sae_port: int = 9000
    worker_sae_oidc_role_name: str = "hiclaw-worker-role"

    # AI Gateway model API (populated by configure_hiclaw_gateway at deploy time)
    gw_model_api_id: str = ""
    gw_env_id: str = ""

    # Matrix configuration
    matrix_server_name: str = "hiclaw.cloud"
    matrix_registration_token: str = field(default_factory=lambda: os.environ.get(
        "HICLAW_REGISTRATION_TOKEN", secrets.token_hex(16)))

    # NLB zone mappings for SAE BindNlb (populated by setup_hiclaw_vpc, at least 2 zones)
    nlb_zone_mappings: list = field(default_factory=list)

    # Cloud networking - NLB private addresses for SAE apps (populated at deploy time)
    tuwunel_nlb_address: str = ""
    tuwunel_nlb_port: int = 6167
    element_nlb_address: str = ""
    element_nlb_port: int = 80

    # AI Gateway addresses (populated by configure_hiclaw_gateway at deploy time)
    gateway_public_address: str = ""
    gateway_env_address: str = ""
    manager_consumer_key: str = ""

    # Admin credentials (auto-generated, overridable via env)
    admin_user: str = "admin"
    admin_password: str = field(default_factory=lambda: os.environ.get(
        "HICLAW_ADMIN_PASSWORD", _generate_password()))

    # User preferences
    language: str = field(default_factory=lambda: os.environ.get(
        "HICLAW_LANGUAGE", "zh"))

    # Manager credentials (auto-generated, overridable via env)
    manager_gateway_key: str = field(default_factory=lambda: secrets.token_hex(32))
    manager_matrix_password: str = field(default_factory=lambda: os.environ.get(
        "HICLAW_MANAGER_PASSWORD", _generate_password()))

    # LLM Provider
    llm_provider: str = "qwen"
    llm_api_key: str = field(default_factory=lambda: os.environ.get(
        "HICLAW_LLM_API_KEY", ""))
    default_model: str = "qwen3.5-plus"

    def __post_init__(self):
        """Compute region-dependent defaults after init."""
        if not self.oss_endpoint:
            self.oss_endpoint = f"https://oss-{self.region}.aliyuncs.com"
        if not self.oss_internal_endpoint:
            self.oss_internal_endpoint = f"https://oss-{self.region}-internal.aliyuncs.com"
        if not self.sae_namespace_id:
            self.sae_namespace_id = f"{self.region}:hiclawcloud"

    def get_oss_s3_endpoint(self) -> str:
        """Get S3-compatible endpoint for mc client"""
        return f"https://oss-{self.region}.aliyuncs.com"

    def get_internal_matrix_url(self) -> str:
        """Get internal Matrix server URL (via AI Gateway)"""
        return f"http://{self.gateway_name}.{self.region}.apig.aliyuncs.com/_matrix"

    @classmethod
    def load(cls) -> "CloudConfig":
        """Load configuration, optionally from a saved file"""
        return cls()

    def save(self, path: str = None):
        """Save deployment state and credentials to a file"""
        import json
        if path is None:
            path = "/tmp/hiclaw-cloud-config.json"

        data = {
            # Network
            "vpc_id": self.vpc_id,
            "vswitch_id": self.vswitch_id,
            "vswitch_id_alt": self.vswitch_id_alt,
            "nat_gateway_id": self.nat_gateway_id,
            "security_group_id": self.security_group_id,
            # Gateway
            "gateway_id": self.gateway_id,
            "gw_model_api_id": self.gw_model_api_id,
            "gw_env_id": self.gw_env_id,
            "gateway_public_address": self.gateway_public_address,
            "manager_consumer_key": self.manager_consumer_key,
            # Credentials
            "matrix_registration_token": self.matrix_registration_token,
            "admin_password": self.admin_password,
            "manager_gateway_key": self.manager_gateway_key,
            "manager_matrix_password": self.manager_matrix_password,
        }

        with open(path, 'w') as f:
            json.dump(data, f, indent=2)

        print(f"Configuration saved to {path}")
        return path


# Global config instance
config = CloudConfig()


def print_config():
    """Print current configuration (masking secrets)"""
    print("=" * 60)
    print("HiClaw Cloud Configuration")
    print("=" * 60)
    print(f"Region: {config.region}")
    print(f"VPC: {config.vpc_id}")
    print(f"vSwitch: {config.vswitch_id}")
    print(f"Project Name: {config.project_name}")
    print()
    print(f"OSS Bucket: {config.oss_bucket_name}")
    print(f"Gateway: {config.gateway_name}")
    print(f"SAE Namespace: {config.sae_namespace_name}")
    print()
    print(f"Manager SAE App: {config.manager_sae_app_name}")
    print(f"LLM Provider: {config.llm_provider}")
    print(f"Default Model: {config.default_model}")
    print("=" * 60)


if __name__ == "__main__":
    print_config()
