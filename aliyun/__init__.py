"""
HiClaw Cloud - Alibaba Cloud Deployment SDK

This package provides Python SDK automation for deploying HiClaw to Alibaba Cloud.
"""

from .config import config, CloudConfig, print_config
from .clients import (
    get_vpc_client,
    get_ecs_client,
    get_apig_client,
    get_sae_client,
    get_oss_client,
    get_sls_client,
    verify_credentials,
)
from .rrsa import setup_rrsa_role

__all__ = [
    'config',
    'CloudConfig',
    'print_config',
    'get_vpc_client',
    'get_ecs_client',
    'get_apig_client',
    'get_sae_client',
    'get_oss_client',
    'get_sls_client',
    'verify_credentials',
    'setup_rrsa_role',
]

__version__ = '0.1.0'
