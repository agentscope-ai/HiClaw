#!/usr/bin/env python3
"""
HiClaw Cloud - Security Group Management

Create and configure security groups for HiClaw cloud deployment.
"""

from typing import Optional, List
from alibabacloud_ecs20140526 import models as ecs_models

from .config import config
from .clients import get_ecs_client


def create_security_group(
    name: str = None,
    description: str = None,
    vpc_id: str = None
) -> str:
    """
    Create a security group in the specified VPC.
    
    Returns: Security Group ID
    """
    ecs = get_ecs_client()
    
    name = name or config.security_group_name
    vpc_id = vpc_id or config.vpc_id
    description = description or f"Security group for {config.project_name}"
    
    req = ecs_models.CreateSecurityGroupRequest(
        region_id=config.region,
        vpc_id=vpc_id,
        security_group_name=name,
        description=description,
        security_group_type="normal"
    )
    
    resp = ecs.create_security_group(req)
    sg_id = resp.body.security_group_id
    
    print(f"✅ Created Security Group: {sg_id} ({name})")
    return sg_id


def authorize_ingress_rule(
    security_group_id: str,
    port_range: str,
    source_cidr: str = "0.0.0.0/0",
    ip_protocol: str = "tcp",
    description: str = ""
) -> bool:
    """
    Add an ingress rule to a security group.
    
    Args:
        security_group_id: Target security group
        port_range: Port range (e.g., "80/80", "443/443", "-1/-1" for all)
        source_cidr: Source CIDR block
        ip_protocol: Protocol (tcp, udp, icmp, all)
        description: Rule description
    
    Returns: True if successful
    """
    ecs = get_ecs_client()
    
    # When ip_protocol is "all", port_range must be "-1/-1"
    if ip_protocol.lower() == "all":
        port_range = "-1/-1"
    
    req = ecs_models.AuthorizeSecurityGroupRequest(
        region_id=config.region,
        security_group_id=security_group_id,
        ip_protocol=ip_protocol,
        port_range=port_range,
        source_cidr_ip=source_cidr,
        description=description
    )
    
    try:
        ecs.authorize_security_group(req)
        print(f"  ✅ Ingress rule added: {ip_protocol} {port_range} from {source_cidr}")
        return True
    except Exception as e:
        if "DuplicateEntry" in str(e) or "already exists" in str(e).lower():
            print(f"  ⏭️  Ingress rule already exists: {ip_protocol} {port_range}")
            return True
        raise


def authorize_egress_rule(
    security_group_id: str,
    port_range: str,
    dest_cidr: str = "0.0.0.0/0",
    ip_protocol: str = "tcp",
    description: str = ""
) -> bool:
    """
    Add an egress rule to a security group.
    """
    ecs = get_ecs_client()
    
    # When ip_protocol is "all", port_range must be "-1/-1"
    if ip_protocol.lower() == "all":
        port_range = "-1/-1"
    
    req = ecs_models.AuthorizeSecurityGroupEgressRequest(
        region_id=config.region,
        security_group_id=security_group_id,
        ip_protocol=ip_protocol,
        port_range=port_range,
        dest_cidr_ip=dest_cidr,
        description=description
    )
    
    try:
        ecs.authorize_security_group_egress(req)
        print(f"  ✅ Egress rule added: {ip_protocol} {port_range} to {dest_cidr}")
        return True
    except Exception as e:
        if "DuplicateEntry" in str(e) or "already exists" in str(e).lower():
            print(f"  ⏭️  Egress rule already exists: {ip_protocol} {port_range}")
            return True
        raise


def setup_hiclaw_security_group() -> str:
    """
    Create and configure a security group for HiClaw with all necessary rules.
    
    Returns: Security Group ID
    """
    print(f"\n{'='*60}")
    print("Creating HiClaw Security Group")
    print(f"{'='*60}")
    
    # Check if security group already exists
    ecs = get_ecs_client()
    req = ecs_models.DescribeSecurityGroupsRequest(
        region_id=config.region,
        vpc_id=config.vpc_id,
        security_group_name=config.security_group_name
    )
    resp = ecs.describe_security_groups(req)
    
    sg_id = None
    if resp.body.security_groups and resp.body.security_groups.security_group:
        for sg in resp.body.security_groups.security_group:
            if sg.security_group_name == config.security_group_name:
                sg_id = sg.security_group_id
                print(f"⏭️  Security Group already exists: {sg_id}")
                config.security_group_id = sg_id
                break
    
    # Create new security group if not exists
    if not sg_id:
        sg_id = create_security_group()
        config.security_group_id = sg_id
    
    print(f"\nConfiguring ingress rules...")
    
    # Allow all internal VPC traffic (VPC CIDR is 10.0.0.0/16)
    authorize_ingress_rule(
        sg_id, "1/65535", "10.0.0.0/16",
        "all", "Allow all VPC internal traffic"
    )
    
    # HTTP/HTTPS (for AI Gateway, Element Web)
    authorize_ingress_rule(sg_id, "80/80", "0.0.0.0/0", "tcp", "HTTP")
    authorize_ingress_rule(sg_id, "443/443", "0.0.0.0/0", "tcp", "HTTPS")
    
    # Matrix server port
    authorize_ingress_rule(sg_id, "6167/6167", "0.0.0.0/0", "tcp", "Tuwunel Matrix")
    
    # AI Gateway (8080 internal)
    authorize_ingress_rule(sg_id, "8080/8080", "0.0.0.0/0", "tcp", "AI Gateway")
    
    print(f"\nConfiguring egress rules...")
    
    # Allow all outbound traffic
    authorize_egress_rule(sg_id, "1/65535", "0.0.0.0/0", "all", "Allow all outbound")
    
    print(f"\n✅ Security Group setup complete: {sg_id}")
    return sg_id


def delete_security_group(security_group_id: str) -> bool:
    """Delete a security group"""
    ecs = get_ecs_client()
    
    req = ecs_models.DeleteSecurityGroupRequest(
        region_id=config.region,
        security_group_id=security_group_id
    )
    
    try:
        ecs.delete_security_group(req)
        print(f"✅ Deleted Security Group: {security_group_id}")
        return True
    except Exception as e:
        print(f"❌ Failed to delete Security Group: {e}")
        return False


def list_security_groups(vpc_id: str = None) -> List[dict]:
    """List security groups in the VPC"""
    ecs = get_ecs_client()
    vpc_id = vpc_id or config.vpc_id
    
    req = ecs_models.DescribeSecurityGroupsRequest(
        region_id=config.region,
        vpc_id=vpc_id
    )
    
    resp = ecs.describe_security_groups(req)
    
    groups = []
    if resp.body.security_groups and resp.body.security_groups.security_group:
        for sg in resp.body.security_groups.security_group:
            groups.append({
                "id": sg.security_group_id,
                "name": sg.security_group_name,
                "description": sg.description
            })
    
    return groups


if __name__ == "__main__":
    # Test: Create security group
    sg_id = setup_hiclaw_security_group()
    print(f"\nSecurity Group ID: {sg_id}")
