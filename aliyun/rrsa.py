#!/usr/bin/env python3
"""
HiClaw Cloud - RRSA OIDC Setup

Create RAM Roles and custom policies for Manager and Worker SAE applications
to access cloud resources without static AK/SK.

Two roles with separate permissions (least-privilege):
  - hiclaw-manager-role: OSS + SAE (worker management) + APIG (consumer management)
  - hiclaw-worker-role:  OSS only (read/write agent workspace and shared data)
"""

import json
from typing import Dict, Any

from .config import config
from .clients import get_sts_client


def get_ram_client():
    """Get RAM client (lazy import to avoid circular deps)"""
    from alibabacloud_ram20150501 import client as ram_client
    from alibabacloud_tea_openapi import models as open_api_models
    api_config = open_api_models.Config(
        access_key_id=config.access_key_id,
        access_key_secret=config.access_key_secret,
        region_id=config.region,
        endpoint="ram.aliyuncs.com"
    )
    return ram_client.Client(api_config)


def build_trust_policy() -> str:
    """
    Build the trust policy for RAM Roles.

    The OIDC Provider ARN must be set in config after SAE first enables RRSA.
    Shared by both Manager and Worker roles (same OIDC provider).
    """
    if not config.rrsa_oidc_provider_arn:
        raise ValueError(
            "rrsa_oidc_provider_arn is not configured. "
            "Please enable RRSA OIDC on the SAE namespace first, "
            "then set the OIDC Provider ARN in cloud/config.py."
        )

    ns_suffix = config.sae_namespace_id.replace(":", "-")

    policy = {
        "Version": "1",
        "Statement": [
            {
                "Effect": "Allow",
                "Principal": {
                    "Federated": [config.rrsa_oidc_provider_arn]
                },
                "Action": "sts:AssumeRole"
            }
        ]
    }
    return json.dumps(policy)


def build_manager_permission_policy() -> str:
    """
    Build the custom permission policy for HiClaw Manager.

    Grants precise access to:
    - OSS: read/write on the hiclaw bucket
    - SAE: manage worker applications (create, delete, stop, start, query)
    - APIG: manage consumers
    """
    policy = {
        "Version": "1",
        "Statement": [
            {
                "Effect": "Allow",
                "Action": [
                    "oss:GetObject",
                    "oss:PutObject",
                    "oss:DeleteObject",
                    "oss:ListObjects",
                    "oss:ListObjectsV2",
                    "oss:GetBucketInfo",
                    "oss:HeadObject"
                ],
                "Resource": [
                    f"acs:oss:*:*:{config.oss_bucket_name}",
                    f"acs:oss:*:*:{config.oss_bucket_name}/*"
                ]
            },
            {
                "Effect": "Allow",
                "Action": [
                    "sae:CreateApplication",
                    "sae:DeleteApplication",
                    "sae:StopApplication",
                    "sae:StartApplication",
                    "sae:DescribeApplicationStatus",
                    "sae:DescribeApplicationConfig",
                    "sae:ListApplications",
                    "sae:DeployApplication"
                ],
                "Resource": "*"
            },
            {
                "Effect": "Allow",
                "Action": [
                    "apig:CreateConsumer",
                    "apig:GetConsumer",
                    "apig:ListConsumers",
                    "apig:UpdateConsumer",
                    "apig:CreateConsumerAuthorizationRule",
                    "apig:CreateConsumerAuthorizationRules"
                ],
                "Resource": "*"
            }
        ]
    }
    return json.dumps(policy)


def build_worker_permission_policy() -> str:
    """
    Build the custom permission policy for HiClaw Worker.

    Workers only need OSS access to sync their workspace and shared data.
    No management-plane permissions (no SAE, no APIG).
    """
    policy = {
        "Version": "1",
        "Statement": [
            {
                "Effect": "Allow",
                "Action": [
                    "oss:GetObject",
                    "oss:PutObject",
                    "oss:DeleteObject",
                    "oss:ListObjects",
                    "oss:ListObjectsV2",
                    "oss:GetBucketInfo",
                    "oss:HeadObject"
                ],
                "Resource": [
                    f"acs:oss:*:*:{config.oss_bucket_name}",
                    f"acs:oss:*:*:{config.oss_bucket_name}/*"
                ]
            }
        ]
    }
    return json.dumps(policy)


# --- Manager Role ---
MANAGER_ROLE_NAME = "hiclaw-manager-role"
MANAGER_POLICY_NAME = "HiClawManagerPolicy"

# --- Worker Role ---
WORKER_ROLE_NAME = "hiclaw-worker-role"
WORKER_POLICY_NAME = "HiClawWorkerPolicy"

# Backward compat aliases
ROLE_NAME = MANAGER_ROLE_NAME
POLICY_NAME = MANAGER_POLICY_NAME


def _create_role(role_name: str, description: str) -> str:
    """Create a RAM Role for RRSA OIDC. Idempotent. Returns Role ARN."""
    from alibabacloud_ram20150501 import models as ram_models

    ram = get_ram_client()
    trust_policy = build_trust_policy()

    try:
        resp = ram.get_role(ram_models.GetRoleRequest(role_name=role_name))
        arn = resp.body.role.arn
        print(f"⏭️  RAM Role already exists: {role_name} ({arn})")
        return arn
    except Exception as e:
        if "EntityNotExist" not in str(e):
            raise

    resp = ram.create_role(ram_models.CreateRoleRequest(
        role_name=role_name,
        assume_role_policy_document=trust_policy,
        description=description
    ))
    arn = resp.body.role.arn
    print(f"✅ RAM Role created: {role_name} ({arn})")
    return arn


def _create_policy(policy_name: str, policy_document: str, description: str) -> str:
    """Create or update a custom permission policy. Idempotent. Returns policy name."""
    from alibabacloud_ram20150501 import models as ram_models

    ram = get_ram_client()

    try:
        ram.get_policy(ram_models.GetPolicyRequest(
            policy_name=policy_name,
            policy_type="Custom"
        ))
        print(f"⏭️  Policy already exists: {policy_name}")

        try:
            ram.create_policy_version(ram_models.CreatePolicyVersionRequest(
                policy_name=policy_name,
                policy_document=policy_document,
                set_as_default=True
            ))
            print(f"✅ Policy updated: {policy_name}")
        except Exception as ver_err:
            if "LimitExceeded" in str(ver_err):
                # RAM allows max 5 versions; delete non-default ones and retry
                print(f"  Policy version limit reached, cleaning old versions...")
                ver_resp = ram.list_policy_versions(ram_models.ListPolicyVersionsRequest(
                    policy_name=policy_name,
                    policy_type="Custom",
                ))
                if ver_resp.body.policy_versions and ver_resp.body.policy_versions.policy_version:
                    for pv in ver_resp.body.policy_versions.policy_version:
                        if not pv.is_default_version:
                            ram.delete_policy_version(ram_models.DeletePolicyVersionRequest(
                                policy_name=policy_name,
                                version_id=pv.version_id,
                            ))
                ram.create_policy_version(ram_models.CreatePolicyVersionRequest(
                    policy_name=policy_name,
                    policy_document=policy_document,
                    set_as_default=True
                ))
                print(f"✅ Policy updated (after version cleanup): {policy_name}")
            else:
                raise

        return policy_name
    except Exception as e:
        if "EntityNotExist" not in str(e):
            raise

    ram.create_policy(ram_models.CreatePolicyRequest(
        policy_name=policy_name,
        policy_document=policy_document,
        description=description
    ))
    print(f"✅ Policy created: {policy_name}")
    return policy_name


def _attach_policy_to_role(policy_name: str, role_name: str) -> None:
    """Attach a custom policy to a RAM Role. Idempotent."""
    from alibabacloud_ram20150501 import models as ram_models

    ram = get_ram_client()

    try:
        ram.attach_policy_to_role(ram_models.AttachPolicyToRoleRequest(
            policy_name=policy_name,
            policy_type="Custom",
            role_name=role_name
        ))
        print(f"✅ Policy {policy_name} attached to role {role_name}")
    except Exception as e:
        if "EntityAlreadyExists" in str(e):
            print(f"⏭️  Policy already attached to role {role_name}")
        else:
            raise


# --- Public convenience wrappers (backward compat) ---

def create_role() -> str:
    return _create_role(MANAGER_ROLE_NAME,
                        "HiClaw Manager RRSA role for SAE — access OSS, SAE, APIG")

def create_policy() -> str:
    return _create_policy(MANAGER_POLICY_NAME,
                          build_manager_permission_policy(),
                          "HiClaw Manager permissions: OSS read/write, SAE worker management, APIG consumer management")

def attach_policy_to_role() -> None:
    _attach_policy_to_role(MANAGER_POLICY_NAME, MANAGER_ROLE_NAME)


def setup_rrsa_role() -> Dict[str, str]:
    """
    Setup complete RRSA: create Manager and Worker Roles, Policies, and bind them.

    Returns: Dict with manager and worker role info
    """
    print(f"\n{'='*60}")
    print("Setting up RRSA OIDC for Manager and Worker")
    print(f"{'='*60}")

    # Manager Role
    print(f"\n--- Manager Role ---")
    manager_role_arn = _create_role(
        MANAGER_ROLE_NAME,
        "HiClaw Manager RRSA role for SAE — access OSS, SAE, APIG"
    )
    _create_policy(
        MANAGER_POLICY_NAME,
        build_manager_permission_policy(),
        "HiClaw Manager permissions: OSS read/write, SAE worker management, APIG consumer management"
    )
    _attach_policy_to_role(MANAGER_POLICY_NAME, MANAGER_ROLE_NAME)

    # Worker Role
    print(f"\n--- Worker Role ---")
    worker_role_arn = _create_role(
        WORKER_ROLE_NAME,
        "HiClaw Worker RRSA role for SAE — access OSS only"
    )
    _create_policy(
        WORKER_POLICY_NAME,
        build_worker_permission_policy(),
        "HiClaw Worker permissions: OSS read/write only"
    )
    _attach_policy_to_role(WORKER_POLICY_NAME, WORKER_ROLE_NAME)

    print(f"\n✅ RRSA setup complete")
    print(f"   Manager Role: {MANAGER_ROLE_NAME} ({manager_role_arn})")
    print(f"   Worker Role:  {WORKER_ROLE_NAME} ({worker_role_arn})")

    return {
        "role_arn": manager_role_arn,
        "role_name": MANAGER_ROLE_NAME,
        "policy_name": MANAGER_POLICY_NAME,
        "worker_role_arn": worker_role_arn,
        "worker_role_name": WORKER_ROLE_NAME,
        "worker_policy_name": WORKER_POLICY_NAME,
    }


def destroy_rrsa_roles() -> bool:
    """
    Destroy RRSA Roles and Policies for both Manager and Worker.

    Order per role: detach policy → delete policy (all versions) → delete role.
    """
    from alibabacloud_ram20150501 import models as ram_models

    print(f"\n{'='*60}")
    print("Destroying RRSA Roles and Policies")
    print(f"{'='*60}")

    ram = get_ram_client()

    for role_name, policy_name in [
        (MANAGER_ROLE_NAME, MANAGER_POLICY_NAME),
        (WORKER_ROLE_NAME, WORKER_POLICY_NAME),
    ]:
        print(f"\n--- {role_name} ---")

        # Detach policy from role
        try:
            ram.detach_policy_from_role(ram_models.DetachPolicyFromRoleRequest(
                policy_name=policy_name,
                policy_type="Custom",
                role_name=role_name,
            ))
            print(f"  ✅ Policy detached: {policy_name} from {role_name}")
        except Exception as e:
            if "EntityNotExist" in str(e):
                print(f"  ⏭️  Policy or role not found, skipping detach")
            else:
                print(f"  ⚠️  Detach error (continuing): {e}")

        # Delete non-default policy versions first (required before deleting policy)
        try:
            resp = ram.list_policy_versions(ram_models.ListPolicyVersionsRequest(
                policy_name=policy_name,
                policy_type="Custom",
            ))
            if resp.body.policy_versions and resp.body.policy_versions.policy_version:
                for pv in resp.body.policy_versions.policy_version:
                    if not pv.is_default_version:
                        ram.delete_policy_version(ram_models.DeletePolicyVersionRequest(
                            policy_name=policy_name,
                            version_id=pv.version_id,
                        ))
                        print(f"  ✅ Policy version deleted: {pv.version_id}")
        except Exception as e:
            if "EntityNotExist" not in str(e):
                print(f"  ⚠️  Policy version cleanup error (continuing): {e}")

        # Delete policy
        try:
            ram.delete_policy(ram_models.DeletePolicyRequest(
                policy_name=policy_name,
            ))
            print(f"  ✅ Policy deleted: {policy_name}")
        except Exception as e:
            if "EntityNotExist" in str(e):
                print(f"  ⏭️  Policy not found: {policy_name}")
            else:
                print(f"  ⚠️  Policy delete error (continuing): {e}")

        # Delete role
        try:
            ram.delete_role(ram_models.DeleteRoleRequest(
                role_name=role_name,
            ))
            print(f"  ✅ Role deleted: {role_name}")
        except Exception as e:
            if "EntityNotExist" in str(e):
                print(f"  ⏭️  Role not found: {role_name}")
            else:
                print(f"  ⚠️  Role delete error (continuing): {e}")

    print(f"\n  ✅ RRSA resources destroyed")
    return True


if __name__ == "__main__":
    setup_rrsa_role()
