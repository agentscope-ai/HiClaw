#!/usr/bin/env python3
"""
HiClaw Cloud - SDK Client Factory

Creates and caches Alibaba Cloud SDK clients for all services.
"""

from typing import Optional
from functools import lru_cache
from alibabacloud_tea_openapi import models as open_api_models

from .config import config


def create_api_config(endpoint: str) -> open_api_models.Config:
    """Create a standard API config with credentials"""
    return open_api_models.Config(
        access_key_id=config.access_key_id,
        access_key_secret=config.access_key_secret,
        region_id=config.region,
        endpoint=endpoint
    )


@lru_cache(maxsize=1)
def get_vpc_client():
    """Get VPC client"""
    from alibabacloud_vpc20160428 import client as vpc_client
    return vpc_client.Client(create_api_config(f"vpc.{config.region}.aliyuncs.com"))


@lru_cache(maxsize=1)
def get_ecs_client():
    """Get ECS client (for security groups)"""
    from alibabacloud_ecs20140526 import client as ecs_client
    return ecs_client.Client(create_api_config(f"ecs.{config.region}.aliyuncs.com"))


@lru_cache(maxsize=1)
def get_apig_client():
    """Get AI Gateway (APIG) client"""
    from alibabacloud_apig20240327 import client as apig_client
    return apig_client.Client(create_api_config(f"apig.{config.region}.aliyuncs.com"))


@lru_cache(maxsize=1)
def get_sae_client():
    """Get SAE client"""
    from alibabacloud_sae20190506 import client as sae_client
    return sae_client.Client(create_api_config(f"sae.{config.region}.aliyuncs.com"))


@lru_cache(maxsize=1)
def get_oss_client():
    """Get OSS client"""
    import alibabacloud_oss_v2 as oss
    
    cred_provider = oss.credentials.StaticCredentialsProvider(
        config.access_key_id, 
        config.access_key_secret
    )
    oss_config = oss.Config(
        credentials_provider=cred_provider,
        region=config.region
    )
    return oss.Client(oss_config)


@lru_cache(maxsize=1)
def get_sls_client():
    """Get SLS (Log Service) client"""
    from alibabacloud_sls20201230 import client as sls_client
    return sls_client.Client(create_api_config(f"{config.region}.log.aliyuncs.com"))


@lru_cache(maxsize=1)
def get_sts_client():
    """Get STS client (for identity verification)"""
    from alibabacloud_sts20150401 import client as sts_client
    return sts_client.Client(create_api_config(f"sts.{config.region}.aliyuncs.com"))


def verify_credentials() -> dict:
    """Verify credentials and return account info"""
    sts = get_sts_client()
    resp = sts.get_caller_identity()
    return {
        "account_id": resp.body.account_id,
        "user_id": resp.body.user_id,
        "arn": resp.body.arn
    }


def clear_client_cache():
    """Clear all cached clients"""
    get_vpc_client.cache_clear()
    get_ecs_client.cache_clear()
    get_apig_client.cache_clear()
    get_sae_client.cache_clear()
    get_oss_client.cache_clear()
    get_sls_client.cache_clear()
    get_sts_client.cache_clear()


if __name__ == "__main__":
    print("Verifying credentials...")
    info = verify_credentials()
    print(f"Account ID: {info['account_id']}")
    print(f"User ID: {info['user_id']}")
    print(f"ARN: {info['arn']}")
