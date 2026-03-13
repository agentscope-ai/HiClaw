#!/usr/bin/env python3
"""
HiClaw Cloud - One-Click Deployment Orchestrator

Main entry point for deploying HiClaw to Alibaba Cloud.
"""

import sys
import json
import argparse
from datetime import datetime

from .config import config, print_config
from .clients import verify_credentials
from .security import setup_hiclaw_security_group, delete_security_group, list_security_groups
from .oss import setup_hiclaw_storage, delete_bucket
from .gateway import (
    setup_hiclaw_gateway,
    create_hiclaw_gateway_instance,
    configure_hiclaw_gateway,
    list_gateways,
    delete_gateway,
)
from .sae import setup_hiclaw_sae, deploy_manager, list_namespaces, destroy_hiclaw_sae
from .rrsa import setup_rrsa_role, destroy_rrsa_roles
from .vpc import setup_hiclaw_vpc, destroy_hiclaw_vpc, list_vpcs, list_vswitches


def _reuse_existing_vpc() -> dict:
    """
    Find and reuse existing HiClaw VPC, populating config with its IDs.

    Returns: Dict with vpc info (same shape as setup_hiclaw_vpc return value)
    Raises: RuntimeError if VPC not found or incomplete
    """
    vpcs = list_vpcs(vpc_name=config.vpc_name)
    vpc = None
    for v in vpcs:
        if v["vpc_name"] == config.vpc_name:
            vpc = v
            break
    if not vpc:
        raise RuntimeError(f"VPC '{config.vpc_name}' not found. Remove --reuse-vpc to create a new one.")

    vpc_id = vpc["vpc_id"]
    vswitches = list_vswitches(vpc_id)
    if len(vswitches) < 2:
        raise RuntimeError(f"VPC {vpc_id} has {len(vswitches)} vSwitches, need at least 2.")

    # Sort by name to get consistent ordering (vsw-1, vsw-2)
    vswitches.sort(key=lambda x: x.get("vswitch_name", ""))
    vsw_1 = vswitches[0]
    vsw_2 = vswitches[1]

    nat_ids = vpc.get("nat_gateway_ids", [])

    config.vpc_id = vpc_id
    config.vswitch_id = vsw_1["vswitch_id"]
    config.vswitch_id_alt = vsw_2["vswitch_id"]
    config.zone_id = vsw_1["zone_id"]
    config.nat_gateway_id = nat_ids[0] if nat_ids else None
    config.nlb_zone_mappings = [
        {"zone_id": vsw_1["zone_id"], "vswitch_id": vsw_1["vswitch_id"]},
        {"zone_id": vsw_2["zone_id"], "vswitch_id": vsw_2["vswitch_id"]},
    ]

    print(f"  Reusing VPC: {vpc_id}")
    print(f"  vSwitch 1: {vsw_1['vswitch_id']} ({vsw_1['zone_id']})")
    print(f"  vSwitch 2: {vsw_2['vswitch_id']} ({vsw_2['zone_id']})")
    if nat_ids:
        print(f"  NAT Gateway: {nat_ids[0]}")

    return {
        "vpc_id": vpc_id,
        "vswitch_id": vsw_1["vswitch_id"],
        "vswitch_id_alt": vsw_2["vswitch_id"],
        "zone_1": vsw_1["zone_id"],
        "zone_2": vsw_2["zone_id"],
        "nat_gateway_id": nat_ids[0] if nat_ids else None,
    }


def deploy_all(skip_gateway: bool = False, reuse_vpc: bool = False):
    """
    Deploy all HiClaw components to Alibaba Cloud.

    Deployment order (respects dependency chain):
    1. Verify credentials
    2. Setup VPC network (VPC + 2 vSwitches + EIP + NAT Gateway + SNAT)
    3. Create Security Group (in the VPC)
    4. Create OSS bucket
    5. Setup RRSA OIDC Roles (Manager + Worker)
    6. Create AI Gateway instance (takes 3-5 min, no route config yet)
    7. Deploy SAE apps (Tuwunel + Element Web) + BindNlb + wait NLB ready
       → produces tuwunel_nlb_address, element_nlb_address
    8. Configure AI Gateway (DNS services, APIs, routes, consumer)
       → uses NLB addresses from step 7
    9. Deploy Manager SAE app
       → uses Gateway address + Tuwunel NLB address from steps 6-7
    """
    print("=" * 60)
    print("HiClaw Cloud Deployment")
    print("=" * 60)
    print(f"Started at: {datetime.now().isoformat()}")
    print()

    print_config()

    # Step 1: Verify credentials
    print("\n" + "=" * 60)
    print("Step 1: Verifying Credentials")
    print("=" * 60)

    try:
        info = verify_credentials()
        config.account_id = info['account_id']
        print(f"✅ Credentials verified")
        print(f"   Account ID: {info['account_id']}")
        print(f"   User: {info['arn']}")
    except Exception as e:
        print(f"❌ Credential verification failed: {e}")
        return False

    # Ensure OSS bucket name is globally unique by appending account_id suffix
    if not config.oss_bucket_name.endswith(config.account_id):
        config.oss_bucket_name = f"{config.oss_bucket_name}-{config.account_id}"
        print(f"   OSS Bucket: {config.oss_bucket_name}")

    # Step 2: Setup VPC Network Infrastructure
    print("\n" + "=" * 60)
    print("Step 2: Setting up VPC Network Infrastructure")
    print("=" * 60)

    vpc_info = None
    try:
        if reuse_vpc:
            vpc_info = _reuse_existing_vpc()
        else:
            vpc_info = setup_hiclaw_vpc()
    except Exception as e:
        print(f"❌ VPC setup failed: {e}")
        return False

    # Step 3: Create Security Group
    print("\n" + "=" * 60)
    print("Step 3: Creating Security Group")
    print("=" * 60)

    try:
        sg_id = setup_hiclaw_security_group()
        config.security_group_id = sg_id
    except Exception as e:
        print(f"❌ Security Group creation failed: {e}")
        return False

    # Step 4: Create OSS bucket
    print("\n" + "=" * 60)
    print("Step 4: Creating OSS Storage")
    print("=" * 60)

    try:
        setup_hiclaw_storage()
    except Exception as e:
        print(f"❌ OSS setup failed: {e}")
        return False

    # Step 5: Setup RRSA OIDC for Manager and Worker
    print("\n" + "=" * 60)
    print("Step 5: Setting up RRSA OIDC Roles (Manager + Worker)")
    print("=" * 60)

    try:
        rrsa_info = setup_rrsa_role()
    except Exception as e:
        print(f"❌ RRSA setup failed: {e}")
        return False

    # Step 6: Create AI Gateway instance (no route config yet)
    gateway_info = None
    if not skip_gateway:
        print("\n" + "=" * 60)
        print("Step 6: Creating AI Gateway instance (this may take 3-5 minutes)")
        print("=" * 60)

        try:
            gateway_info = create_hiclaw_gateway_instance()
            config.gateway_id = gateway_info["gateway_id"]
            # Set public address early so Step 7 (Element Web) can use it
            if gateway_info.get("internet_endpoint"):
                config.gateway_public_address = gateway_info["internet_endpoint"]
                config.gateway_env_address = gateway_info["internet_endpoint"]
                print(f"   Gateway endpoint: {config.gateway_public_address}")
        except Exception as e:
            print(f"❌ AI Gateway creation failed: {e}")
            print("   You can skip gateway with --skip-gateway and create it manually")
            return False
    else:
        print("\n" + "=" * 60)
        print("Step 6: Skipping AI Gateway (--skip-gateway)")
        print("=" * 60)

    # Step 7: Deploy SAE apps (Tuwunel + Element Web) with NLB
    print("\n" + "=" * 60)
    print("Step 7: Deploying SAE Applications (Tuwunel + Element Web + NLB)")
    print("=" * 60)

    try:
        sae_info = setup_hiclaw_sae(config.security_group_id)
    except Exception as e:
        print(f"❌ SAE deployment failed: {e}")
        return False

    # Step 8: Configure AI Gateway with NLB addresses
    gw_config_info = None
    if not skip_gateway and gateway_info:
        print("\n" + "=" * 60)
        print("Step 8: Configuring AI Gateway (services, APIs, routes, consumer)")
        print("=" * 60)

        try:
            gw_config_info = configure_hiclaw_gateway(
                gateway_id=gateway_info["gateway_id"],
                env_id=gateway_info["environment_id"],
                domain_id=gateway_info["domain_id"],
                tuwunel_address=config.tuwunel_nlb_address,
                tuwunel_port=config.tuwunel_nlb_port,
                element_address=config.element_nlb_address,
                element_port=config.element_nlb_port,
            )
            # Explicit config write-back (configure_hiclaw_gateway also does this internally)
            config.gw_model_api_id = gw_config_info.get("model_api_id") or ""
            config.gw_env_id = gw_config_info.get("environment_id", "")
            config.gateway_public_address = gw_config_info.get("endpoint", "")
            config.gateway_env_address = gw_config_info.get("endpoint", "")
            config.manager_consumer_key = gw_config_info.get("consumer_key") or ""
        except Exception as e:
            print(f"❌ AI Gateway configuration failed: {e}")
            return False
    else:
        print("\n" + "=" * 60)
        print("Step 8: Skipping AI Gateway configuration (--skip-gateway)")
        print("=" * 60)

    # Step 9: Deploy Manager SAE app
    print("\n" + "=" * 60)
    print("Step 9: Deploying Manager Agent (SAE)")
    print("=" * 60)

    try:
        manager_app_id = deploy_manager(config.security_group_id)
    except Exception as e:
        print(f"❌ Manager deployment failed: {e}")
        return False

    # Save configuration
    config_path = config.save()

    # Print summary
    print("\n" + "=" * 60)
    print("Deployment Complete!")
    print("=" * 60)
    print(f"Finished at: {datetime.now().isoformat()}")
    print()
    print("Resources created:")
    print(f"  VPC:               {config.vpc_id}")
    print(f"  vSwitch 1:         {config.vswitch_id} ({config.zone_id})")
    print(f"  vSwitch 2:         {config.vswitch_id_alt}")
    print(f"  NAT Gateway:       {config.nat_gateway_id}")
    if vpc_info:
        print(f"  EIP:               {vpc_info.get('eip_ip', 'N/A')}")
    print(f"  Security Group:    {config.security_group_id}")
    print(f"  OSS Bucket:        {config.oss_bucket_name}")
    if not skip_gateway:
        print(f"  AI Gateway:        {config.gateway_id}")
    print(f"  RRSA Manager Role: {rrsa_info.get('role_name', 'N/A')}")
    print(f"  RRSA Worker Role:  {rrsa_info.get('worker_role_name', 'N/A')}")
    print(f"  SAE Tuwunel:       {sae_info.get('tuwunel_app_id', 'N/A')}")
    print(f"  Tuwunel NLB:       {config.tuwunel_nlb_address}:{config.tuwunel_nlb_port}")
    print(f"  SAE Element Web:   {sae_info.get('element_app_id', 'N/A')}")
    print(f"  Element NLB:       {config.element_nlb_address}:{config.element_nlb_port}")
    print(f"  SAE Manager:       {manager_app_id}")
    print()
    print(f"Credentials saved to: {config_path}")
    print()
    print("Important credentials (save these!):")
    print(f"  Admin User:                {config.admin_user}")
    print(f"  Admin Password:            {config.admin_password}")
    print(f"  Manager Gateway Key:       {config.manager_gateway_key[:16]}...")
    print(f"  Matrix Registration Token: {config.matrix_registration_token}")

    return True


def status():
    """Show status of deployed resources"""
    print("=" * 60)
    print("HiClaw Cloud Status")
    print("=" * 60)
    
    # Verify credentials
    try:
        info = verify_credentials()
        print(f"✅ Account: {info['account_id']}")
    except Exception as e:
        print(f"❌ Credential error: {e}")
        return
    
    # Check AI Gateway
    print(f"\n### AI Gateways ###")
    gateways = list_gateways()
    hiclaw_gw = None
    for gw in gateways:
        marker = "★" if gw["name"] == config.gateway_name else " "
        print(f"  {marker} {gw['id']}: {gw['name']} ({gw['status']})")
        if gw["name"] == config.gateway_name:
            hiclaw_gw = gw
    
    if not hiclaw_gw:
        print(f"  ⚠️  HiClaw gateway '{config.gateway_name}' not found")
    
    # Check SAE
    print(f"\n### SAE Namespaces ###")
    namespaces = list_namespaces()
    for ns in namespaces:
        marker = "★" if config.sae_namespace_name in ns["id"] else " "
        print(f"  {marker} {ns['id']}: {ns['name']}")


def destroy(confirm: bool = False, include_iam: bool = False, keep_vpc: bool = False):
    """
    Destroy all HiClaw cloud resources (reverse order of creation).

    RRSA Roles/Policies are NOT deleted by default (idempotent, safe to keep).
    Use --include-iam to also delete IAM resources.
    Use --keep-vpc to preserve VPC network and Security Group.

    Destruction order:
    1. SAE applications (Manager, Workers, Element, Tuwunel) + Namespace
    2. AI Gateway
    3. OSS bucket (force-delete all objects)
    4. Security Group (skipped with --keep-vpc)
    5. [optional] RRSA Roles and Policies (--include-iam)
    6. VPC network (skipped with --keep-vpc)
    """
    if not confirm:
        print("⚠️  This will delete ALL HiClaw cloud resources!")
        if include_iam:
            print("   Including IAM resources (RRSA Roles and Policies)")
        if keep_vpc:
            print("   Keeping VPC network and Security Group")
        print("   Use --confirm to proceed")
        return False

    print("=" * 60)
    print("Destroying HiClaw Cloud Resources")
    print("=" * 60)
    print(f"Started at: {datetime.now().isoformat()}")
    if keep_vpc:
        print("Mode: Keep VPC network and Security Group")
    print()

    # Step 1: Verify credentials
    try:
        info = verify_credentials()
        config.account_id = info['account_id']
        print(f"✅ Credentials verified: {info['account_id']}")
    except Exception as e:
        print(f"❌ Credential verification failed: {e}")
        return False

    # Ensure OSS bucket name matches deploy-time convention
    if not config.oss_bucket_name.endswith(config.account_id):
        config.oss_bucket_name = f"{config.oss_bucket_name}-{config.account_id}"

    # Step 2: Delete SAE applications
    print("\n" + "=" * 60)
    print("Step 1: Destroying SAE Applications")
    print("=" * 60)
    try:
        destroy_hiclaw_sae()
    except Exception as e:
        print(f"⚠️  SAE destruction error (continuing): {e}")

    # Step 3: Delete AI Gateway
    print("\n" + "=" * 60)
    print("Step 2: Destroying AI Gateway")
    print("=" * 60)
    try:
        gateways = list_gateways()
        for gw in gateways:
            if gw["name"] == config.gateway_name:
                print(f"  Deleting gateway: {gw['id']} ({gw['name']})")
                delete_gateway(gw["id"])
                break
        else:
            print(f"  ⏭️  Gateway not found: {config.gateway_name}")
    except Exception as e:
        print(f"⚠️  Gateway destruction error (continuing): {e}")

    # Step 4: Delete OSS bucket
    print("\n" + "=" * 60)
    print("Step 3: Destroying OSS Storage")
    print("=" * 60)
    try:
        delete_bucket(force=True)
    except Exception as e:
        if "NoSuchBucket" in str(e):
            print(f"  ⏭️  Bucket not found: {config.oss_bucket_name}")
        else:
            print(f"⚠️  OSS destruction error (continuing): {e}")

    # Step 5: Delete Security Group
    if keep_vpc:
        print("\n" + "=" * 60)
        print("Step 4: Skipping Security Group (--keep-vpc)")
        print("=" * 60)
    else:
        print("\n" + "=" * 60)
        print("Step 4: Destroying Security Group")
        print("=" * 60)
        try:
            # Need VPC ID to find security group — look up VPC first
            from .vpc import list_vpcs
            vpcs = list_vpcs(vpc_name=config.vpc_name)
            vpc_id = None
            for v in vpcs:
                if v["vpc_name"] == config.vpc_name:
                    vpc_id = v["vpc_id"]
                    break

            if vpc_id:
                sgs = list_security_groups(vpc_id)
                for sg in sgs:
                    if sg["name"] == config.security_group_name:
                        print(f"  Deleting security group: {sg['id']} ({sg['name']})")
                        delete_security_group(sg["id"])
                        break
                else:
                    print(f"  ⏭️  Security group not found: {config.security_group_name}")
            else:
                print(f"  ⏭️  VPC not found, skipping security group lookup")
        except Exception as e:
            print(f"⚠️  Security group destruction error (continuing): {e}")

    # Step 6: [optional] Delete RRSA Roles and Policies
    if include_iam:
        print("\n" + "=" * 60)
        print("Step 5: Destroying RRSA Roles and Policies (--include-iam)")
        print("=" * 60)
        try:
            destroy_rrsa_roles()
        except Exception as e:
            print(f"⚠️  RRSA destruction error (continuing): {e}")
    else:
        print("\n" + "=" * 60)
        print("Step 5: Skipping RRSA Roles (use --include-iam to delete)")
        print("=" * 60)

    # Step 7: Delete VPC network
    if keep_vpc:
        print("\n" + "=" * 60)
        print("Step 6: Skipping VPC Network (--keep-vpc)")
        print("=" * 60)
    else:
        print("\n" + "=" * 60)
        print("Step 6: Destroying VPC Network")
        print("=" * 60)
        try:
            destroy_hiclaw_vpc()
        except Exception as e:
            print(f"⚠️  VPC destruction error (continuing): {e}")

    print("\n" + "=" * 60)
    print("Destruction Complete")
    print("=" * 60)
    print(f"Finished at: {datetime.now().isoformat()}")

    return True


def main():
    parser = argparse.ArgumentParser(
        description="HiClaw Cloud Deployment Tool",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python -m cloud.deploy                    # Deploy all resources
  python -m cloud.deploy --skip-gateway     # Deploy without AI Gateway
  python -m cloud.deploy status             # Show resource status
  python -m cloud.deploy destroy --confirm  # Destroy all resources
        """
    )
    
    subparsers = parser.add_subparsers(dest="command", help="Commands")
    
    # Deploy command (default)
    deploy_parser = subparsers.add_parser("deploy", help="Deploy HiClaw")
    deploy_parser.add_argument(
        "--skip-gateway", 
        action="store_true",
        help="Skip AI Gateway creation (can be created later)"
    )
    deploy_parser.add_argument(
        "--reuse-vpc",
        action="store_true",
        help="Reuse existing VPC instead of creating a new one"
    )
    
    # Status command
    subparsers.add_parser("status", help="Show resource status")
    
    # Destroy command
    destroy_parser = subparsers.add_parser("destroy", help="Destroy all resources")
    destroy_parser.add_argument(
        "--confirm",
        action="store_true",
        help="Confirm destruction"
    )
    destroy_parser.add_argument(
        "--include-iam",
        action="store_true",
        help="Also delete RRSA Roles and Policies (default: keep)"
    )
    destroy_parser.add_argument(
        "--keep-vpc",
        action="store_true",
        help="Keep VPC network and Security Group (only destroy SAE, Gateway, OSS, IAM)"
    )
    
    # Config command
    subparsers.add_parser("config", help="Show configuration")
    
    args = parser.parse_args()
    
    # Default to deploy if no command
    if args.command is None or args.command == "deploy":
        skip_gateway = getattr(args, "skip_gateway", False)
        reuse_vpc = getattr(args, "reuse_vpc", False)
        success = deploy_all(skip_gateway=skip_gateway, reuse_vpc=reuse_vpc)
        sys.exit(0 if success else 1)
    elif args.command == "status":
        status()
    elif args.command == "destroy":
        destroy(confirm=args.confirm, include_iam=getattr(args, "include_iam", False), keep_vpc=getattr(args, "keep_vpc", False))
    elif args.command == "config":
        print_config()


if __name__ == "__main__":
    main()
