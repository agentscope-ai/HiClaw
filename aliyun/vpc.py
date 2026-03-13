#!/usr/bin/env python3
"""
HiClaw Cloud - VPC Network Infrastructure

Create and manage VPC, vSwitches, EIP, NAT Gateway, and SNAT rules.
All functions are idempotent (check-before-create).
"""

import time
from typing import Optional, List, Dict, Any
from alibabacloud_vpc20160428 import models as vpc_models

from .config import config
from .clients import get_vpc_client


# ---------------------------------------------------------------------------
# VPC
# ---------------------------------------------------------------------------

def list_vpcs(vpc_name: str = None) -> List[Dict[str, Any]]:
    """List VPCs, optionally filtered by name."""
    client = get_vpc_client()
    req = vpc_models.DescribeVpcsRequest(
        region_id=config.region,
        page_size=50,
    )
    if vpc_name:
        req.vpc_name = vpc_name
    resp = client.describe_vpcs(req)
    result = []
    for v in resp.body.vpcs.vpc:
        result.append({
            "vpc_id": v.vpc_id,
            "vpc_name": v.vpc_name,
            "cidr_block": v.cidr_block,
            "status": v.status,
            "nat_gateway_ids": v.nat_gateway_ids.nat_gateway_ids if v.nat_gateway_ids else [],
            "vswitch_ids": v.v_switch_ids.v_switch_id if v.v_switch_ids else [],
        })
    return result


def create_vpc(name: str = None, cidr: str = None) -> str:
    """Create a VPC. Idempotent: skips if a VPC with the same name exists. Returns vpc_id."""
    client = get_vpc_client()
    name = name or config.vpc_name
    cidr = cidr or config.vpc_cidr

    existing = list_vpcs(vpc_name=name)
    for v in existing:
        if v["vpc_name"] == name:
            print(f"⏭️  VPC already exists: {v['vpc_id']} ({name})")
            return v["vpc_id"]

    req = vpc_models.CreateVpcRequest(
        region_id=config.region,
        vpc_name=name,
        cidr_block=cidr,
        description="HiClaw Cloud VPC",
    )
    resp = client.create_vpc(req)
    vpc_id = resp.body.vpc_id
    print(f"✅ VPC created: {vpc_id} ({name})")
    return vpc_id


def wait_vpc_available(vpc_id: str, timeout: int = 120) -> None:
    """Wait until VPC status is Available."""
    client = get_vpc_client()
    print(f"  Waiting for VPC {vpc_id} to be available ...")
    start = time.time()
    time.sleep(2)
    while time.time() - start < timeout:
        req = vpc_models.DescribeVpcsRequest(region_id=config.region, vpc_id=vpc_id)
        resp = client.describe_vpcs(req)
        for v in resp.body.vpcs.vpc:
            if v.status == "Available":
                print(f"  ✅ VPC available: {vpc_id}")
                return
        elapsed = int(time.time() - start)
        print(f"  VPC not ready yet ({elapsed}s)")
        time.sleep(5)
    raise TimeoutError(f"VPC {vpc_id} not available within {timeout}s")


# ---------------------------------------------------------------------------
# vSwitch
# ---------------------------------------------------------------------------

def list_vswitches(vpc_id: str = None) -> List[Dict[str, Any]]:
    """List vSwitches in a VPC."""
    client = get_vpc_client()
    vpc_id = vpc_id or config.vpc_id
    req = vpc_models.DescribeVSwitchesRequest(
        region_id=config.region,
        vpc_id=vpc_id,
        page_size=50,
    )
    resp = client.describe_vswitches(req)
    result = []
    for vs in resp.body.v_switches.v_switch:
        result.append({
            "vswitch_id": vs.v_switch_id,
            "vswitch_name": vs.v_switch_name,
            "zone_id": vs.zone_id,
            "cidr_block": vs.cidr_block,
            "status": vs.status,
            "available_ip": vs.available_ip_address_count,
        })
    return result


def create_vswitch(
    vpc_id: str,
    zone_id: str,
    cidr: str,
    name: str,
) -> str:
    """Create a vSwitch. Idempotent: skips if same name exists in VPC. Returns vswitch_id."""
    client = get_vpc_client()

    existing = list_vswitches(vpc_id)
    for vs in existing:
        if vs["vswitch_name"] == name:
            print(f"⏭️  vSwitch already exists: {vs['vswitch_id']} ({name}, {vs['zone_id']})")
            return vs["vswitch_id"]

    req = vpc_models.CreateVSwitchRequest(
        region_id=config.region,
        vpc_id=vpc_id,
        zone_id=zone_id,
        cidr_block=cidr,
        v_switch_name=name,
        description=f"HiClaw Cloud vSwitch ({zone_id})",
    )
    resp = client.create_vswitch(req)
    vswitch_id = resp.body.v_switch_id
    print(f"✅ vSwitch created: {vswitch_id} ({name}, {zone_id}, {cidr})")
    return vswitch_id


# ---------------------------------------------------------------------------
# NAT Gateway available zones
# ---------------------------------------------------------------------------

def list_nat_gateway_available_zones() -> List[str]:
    """List zone IDs where enhanced NAT gateways can be created."""
    client = get_vpc_client()
    req = vpc_models.ListEnhanhcedNatGatewayAvailableZonesRequest(region_id=config.region)
    resp = client.list_enhanhced_nat_gateway_available_zones(req)
    return [z.zone_id for z in resp.body.zones]


def list_nlb_available_zones() -> List[str]:
    """List zone IDs where NLB instances can be created."""
    from alibabacloud_nlb20220430 import client as nlb_client, models as nlb_models
    from .clients import create_api_config
    client = nlb_client.Client(create_api_config(f"nlb.{config.region}.aliyuncs.com"))
    req = nlb_models.DescribeZonesRequest(region_id=config.region)
    resp = client.describe_zones(req)
    return [z.zone_id for z in resp.body.zones] if resp.body.zones else []


# ---------------------------------------------------------------------------
# EIP
# ---------------------------------------------------------------------------

def create_eip(name: str = None, bandwidth: int = None) -> Dict[str, str]:
    """
    Allocate an EIP. Idempotent: skips if an EIP with the same name exists.

    Returns: {"allocation_id": "...", "ip_address": "..."}
    """
    client = get_vpc_client()
    name = name or f"{config.project_name}-eip"
    bandwidth = bandwidth or config.eip_bandwidth

    # Check existing EIPs by name
    req = vpc_models.DescribeEipAddressesRequest(
        region_id=config.region,
        page_size=50,
    )
    resp = client.describe_eip_addresses(req)
    for eip in resp.body.eip_addresses.eip_address:
        if eip.name == name:
            print(f"⏭️  EIP already exists: {eip.allocation_id} ({eip.ip_address}, {name})")
            return {"allocation_id": eip.allocation_id, "ip_address": eip.ip_address}

    req = vpc_models.AllocateEipAddressRequest(
        region_id=config.region,
        name=name,
        bandwidth=str(bandwidth),
        internet_charge_type="PayByTraffic",
        instance_charge_type="PostPaid",
        description="HiClaw Cloud NAT EIP",
    )
    resp = client.allocate_eip_address(req)
    alloc_id = resp.body.allocation_id
    ip_addr = resp.body.eip_address
    print(f"✅ EIP allocated: {alloc_id} ({ip_addr}, {bandwidth}Mbps)")
    return {"allocation_id": alloc_id, "ip_address": ip_addr}


# ---------------------------------------------------------------------------
# NAT Gateway
# ---------------------------------------------------------------------------

def create_nat_gateway(
    vpc_id: str,
    vswitch_id: str,
    name: str = None,
) -> str:
    """
    Create an enhanced internet NAT gateway. Idempotent. Returns nat_gateway_id.
    """
    client = get_vpc_client()
    name = name or config.nat_gateway_name

    # Check existing
    req = vpc_models.DescribeNatGatewaysRequest(
        region_id=config.region,
        vpc_id=vpc_id,
        name=name,
    )
    resp = client.describe_nat_gateways(req)
    for ng in resp.body.nat_gateways.nat_gateway:
        if ng.name == name:
            print(f"⏭️  NAT Gateway already exists: {ng.nat_gateway_id} ({name})")
            return ng.nat_gateway_id

    req = vpc_models.CreateNatGatewayRequest(
        region_id=config.region,
        vpc_id=vpc_id,
        v_switch_id=vswitch_id,
        name=name,
        nat_type="Enhanced",
        network_type="internet",
        internet_charge_type="PayByLcu",
        description="HiClaw Cloud NAT Gateway",
    )
    resp = client.create_nat_gateway(req)
    nat_id = resp.body.nat_gateway_id
    print(f"✅ NAT Gateway created: {nat_id} ({name})")
    return nat_id


def wait_nat_gateway_available(nat_gateway_id: str, timeout: int = 120) -> None:
    """Wait until NAT gateway status is Available."""
    client = get_vpc_client()
    print(f"  Waiting for NAT Gateway {nat_gateway_id} to be available ...")
    start = time.time()
    time.sleep(3)
    while time.time() - start < timeout:
        req = vpc_models.DescribeNatGatewaysRequest(
            region_id=config.region,
            nat_gateway_id=nat_gateway_id,
        )
        resp = client.describe_nat_gateways(req)
        for ng in resp.body.nat_gateways.nat_gateway:
            if ng.status == "Available":
                print(f"  ✅ NAT Gateway available: {nat_gateway_id}")
                return
        elapsed = int(time.time() - start)
        print(f"  NAT Gateway not ready yet ({elapsed}s)")
        time.sleep(5)
    raise TimeoutError(f"NAT Gateway {nat_gateway_id} not available within {timeout}s")


# ---------------------------------------------------------------------------
# EIP → NAT association
# ---------------------------------------------------------------------------

def associate_eip_to_nat(allocation_id: str, nat_gateway_id: str) -> None:
    """Associate an EIP with a NAT gateway. Idempotent: skips if already bound."""
    client = get_vpc_client()

    # Check if already associated
    req = vpc_models.DescribeNatGatewaysRequest(
        region_id=config.region,
        nat_gateway_id=nat_gateway_id,
    )
    resp = client.describe_nat_gateways(req)
    for ng in resp.body.nat_gateways.nat_gateway:
        if ng.ip_lists and ng.ip_lists.ip_list:
            for ip_item in ng.ip_lists.ip_list:
                if ip_item.allocation_id == allocation_id:
                    print(f"⏭️  EIP already associated: {allocation_id} → {nat_gateway_id}")
                    return

    req = vpc_models.AssociateEipAddressRequest(
        region_id=config.region,
        allocation_id=allocation_id,
        instance_id=nat_gateway_id,
        instance_type="Nat",
    )
    client.associate_eip_address(req)
    print(f"✅ EIP associated: {allocation_id} → {nat_gateway_id}")

def wait_eip_bindable(allocation_id: str, timeout: int = 120) -> None:
    """Wait until EIP status is InUse (bound to NAT), so SNAT can reference it."""
    client = get_vpc_client()
    print(f"  Waiting for EIP {allocation_id} to be InUse ...")
    start = time.time()
    time.sleep(3)
    while time.time() - start < timeout:
        req = vpc_models.DescribeEipAddressesRequest(
            region_id=config.region,
            allocation_id=allocation_id,
        )
        resp = client.describe_eip_addresses(req)
        for eip in resp.body.eip_addresses.eip_address:
            if eip.status == "InUse":
                print(f"  ✅ EIP InUse: {allocation_id}")
                return
            print(f"  EIP status: {eip.status} ({int(time.time() - start)}s)")
        time.sleep(5)
    raise TimeoutError(f"EIP {allocation_id} not InUse within {timeout}s")



# ---------------------------------------------------------------------------
# SNAT
# ---------------------------------------------------------------------------

def _get_snat_table_id(nat_gateway_id: str) -> str:
    """Get the SNAT table ID from a NAT gateway."""
    client = get_vpc_client()
    req = vpc_models.DescribeNatGatewaysRequest(
        region_id=config.region,
        nat_gateway_id=nat_gateway_id,
    )
    resp = client.describe_nat_gateways(req)
    for ng in resp.body.nat_gateways.nat_gateway:
        if ng.snat_table_ids and ng.snat_table_ids.snat_table_id:
            return ng.snat_table_ids.snat_table_id[0]
    raise RuntimeError(f"No SNAT table found for NAT gateway {nat_gateway_id}")


def create_snat_entry(
    nat_gateway_id: str,
    snat_ip: str,
    source_cidr: str = "0.0.0.0/0",
    name: str = None,
) -> str:
    """
    Create a VPC-level SNAT entry. Idempotent. Returns snat_entry_id.
    """
    client = get_vpc_client()
    name = name or f"{config.project_name}-snat"
    snat_table_id = _get_snat_table_id(nat_gateway_id)

    # Check existing
    req = vpc_models.DescribeSnatTableEntriesRequest(
        region_id=config.region,
        snat_table_id=snat_table_id,
        page_size=50,
    )
    resp = client.describe_snat_table_entries(req)
    for entry in resp.body.snat_table_entries.snat_table_entry:
        if entry.source_cidr == source_cidr and entry.snat_ip == snat_ip:
            print(f"⏭️  SNAT entry already exists: {entry.snat_entry_id} ({source_cidr} → {snat_ip})")
            return entry.snat_entry_id

    req = vpc_models.CreateSnatEntryRequest(
        region_id=config.region,
        snat_table_id=snat_table_id,
        snat_ip=snat_ip,
        source_cidr=source_cidr,
        snat_entry_name=name,
    )
    resp = client.create_snat_entry(req)
    snat_id = resp.body.snat_entry_id
    print(f"✅ SNAT entry created: {snat_id} ({source_cidr} → {snat_ip})")
    return snat_id


# ---------------------------------------------------------------------------
# Main orchestrator
# ---------------------------------------------------------------------------

def setup_hiclaw_vpc() -> Dict[str, Any]:
    """
    Setup complete VPC network infrastructure for HiClaw.

    Steps:
    1. Query NAT gateway available zones, pick first 2
    2. Create VPC
    3. Wait VPC available
    4. Create vSwitch 1 (zone 1)
    5. Create vSwitch 2 (zone 2)
    6. Allocate EIP
    7. Create enhanced NAT gateway (in zone 1)
    8. Wait NAT gateway available
    9. Associate EIP to NAT gateway
    10. Create VPC-level SNAT rule (0.0.0.0/0)
    11. Populate config with all IDs

    Returns: Dict with vpc_id, vswitch_id, vswitch_id_alt, nat_gateway_id, eip_ip, zones
    """
    print(f"\n{'='*60}")
    print("Setting up VPC Network Infrastructure")
    print(f"{'='*60}")

    # 1. Pick 2 zones that support both NAT gateway and NLB
    nat_zones = list_nat_gateway_available_zones()
    nlb_zones = list_nlb_available_zones()
    common_zones = [z for z in nat_zones if z in nlb_zones]
    if len(common_zones) < 2:
        raise RuntimeError(
            f"Need at least 2 zones supporting both NAT and NLB, "
            f"found {len(common_zones)}: {common_zones} "
            f"(NAT: {nat_zones}, NLB: {nlb_zones})"
        )
    zone_1, zone_2 = common_zones[0], common_zones[1]
    print(f"  Selected zones (NAT+NLB compatible): {zone_1}, {zone_2}")

    # 2-3. Create VPC and wait
    vpc_id = create_vpc()
    wait_vpc_available(vpc_id)

    # 4-5. Create 2 vSwitches
    vsw_1 = create_vswitch(
        vpc_id=vpc_id,
        zone_id=zone_1,
        cidr=config.vswitch_cidr_1,
        name=f"{config.project_name}-vsw-1",
    )
    vsw_2 = create_vswitch(
        vpc_id=vpc_id,
        zone_id=zone_2,
        cidr=config.vswitch_cidr_2,
        name=f"{config.project_name}-vsw-2",
    )

    # 6. Allocate EIP
    eip_info = create_eip()

    # 7-8. Create NAT gateway and wait
    nat_id = create_nat_gateway(vpc_id=vpc_id, vswitch_id=vsw_1)
    wait_nat_gateway_available(nat_id)

    # 9. Associate EIP to NAT
    associate_eip_to_nat(eip_info["allocation_id"], nat_id)

    # 9.5 Wait for EIP to be InUse before creating SNAT
    wait_eip_bindable(eip_info["allocation_id"])

    # 10. Create SNAT rule
    create_snat_entry(nat_gateway_id=nat_id, snat_ip=eip_info["ip_address"])

    # 11. Populate config
    config.vpc_id = vpc_id
    config.vswitch_id = vsw_1
    config.vswitch_id_alt = vsw_2
    config.zone_id = zone_1
    config.nat_gateway_id = nat_id
    config.nlb_zone_mappings = [
        {"zone_id": zone_1, "vswitch_id": vsw_1},
        {"zone_id": zone_2, "vswitch_id": vsw_2},
    ]

    result = {
        "vpc_id": vpc_id,
        "vswitch_id": vsw_1,
        "vswitch_id_alt": vsw_2,
        "zone_1": zone_1,
        "zone_2": zone_2,
        "nat_gateway_id": nat_id,
        "eip_allocation_id": eip_info["allocation_id"],
        "eip_ip": eip_info["ip_address"],
    }

    print(f"\n{'='*60}")
    print("VPC Network Infrastructure Ready")
    print(f"{'='*60}")
    print(f"  VPC:          {vpc_id}")
    print(f"  vSwitch 1:    {vsw_1} ({zone_1})")
    print(f"  vSwitch 2:    {vsw_2} ({zone_2})")
    print(f"  NAT Gateway:  {nat_id}")
    print(f"  EIP:          {eip_info['ip_address']}")
    print(f"  SNAT:         0.0.0.0/0 → {eip_info['ip_address']}")

    return result


# ---------------------------------------------------------------------------
# Destroy helpers
# ---------------------------------------------------------------------------


def delete_snat_entries(nat_gateway_id: str) -> None:
    """Delete all SNAT entries on a NAT gateway."""
    client = get_vpc_client()
    snat_table_id = _get_snat_table_id(nat_gateway_id)

    req = vpc_models.DescribeSnatTableEntriesRequest(
        region_id=config.region,
        snat_table_id=snat_table_id,
        page_size=50,
    )
    resp = client.describe_snat_table_entries(req)
    for entry in resp.body.snat_table_entries.snat_table_entry:
        client.delete_snat_entry(vpc_models.DeleteSnatEntryRequest(
            region_id=config.region,
            snat_table_id=snat_table_id,
            snat_entry_id=entry.snat_entry_id,
        ))
        print(f"  ✅ SNAT entry deleted: {entry.snat_entry_id}")


def disassociate_eip_from_nat(allocation_id: str, nat_gateway_id: str) -> None:
    """Disassociate an EIP from a NAT gateway."""
    client = get_vpc_client()
    try:
        client.unassociate_eip_address(vpc_models.UnassociateEipAddressRequest(
            region_id=config.region,
            allocation_id=allocation_id,
            instance_id=nat_gateway_id,
            instance_type="Nat",
        ))
        print(f"  ✅ EIP disassociated from NAT: {allocation_id}")
    except Exception as e:
        if "IncorrectEipStatus" in str(e) or "InvalidAssociation" in str(e):
            print(f"  ⏭️  EIP already disassociated: {allocation_id}")
        else:
            raise


def delete_nat_gateway(nat_gateway_id: str, force: bool = True) -> None:
    """Delete a NAT gateway. force=True deletes associated SNAT/DNAT entries."""
    client = get_vpc_client()
    client.delete_nat_gateway(vpc_models.DeleteNatGatewayRequest(
        region_id=config.region,
        nat_gateway_id=nat_gateway_id,
        force=force,
    ))
    print(f"  ✅ NAT Gateway deleted: {nat_gateway_id}")


def wait_nat_gateway_deleted(nat_gateway_id: str, timeout: int = 120) -> None:
    """Wait until a NAT gateway is fully deleted."""
    client = get_vpc_client()
    start = time.time()
    while time.time() - start < timeout:
        req = vpc_models.DescribeNatGatewaysRequest(
            region_id=config.region,
            nat_gateway_id=nat_gateway_id,
        )
        resp = client.describe_nat_gateways(req)
        if not resp.body.nat_gateways.nat_gateway:
            print(f"  ✅ NAT Gateway gone: {nat_gateway_id}")
            return
        status = resp.body.nat_gateways.nat_gateway[0].status
        elapsed = int(time.time() - start)
        print(f"  NAT Gateway status: {status} ({elapsed}s)")
        time.sleep(5)
    raise TimeoutError(f"NAT Gateway not deleted within {timeout}s: {nat_gateway_id}")


def release_eip(allocation_id: str) -> None:
    """Release an EIP."""
    client = get_vpc_client()
    try:
        client.release_eip_address(vpc_models.ReleaseEipAddressRequest(
            region_id=config.region,
            allocation_id=allocation_id,
        ))
        print(f"  ✅ EIP released: {allocation_id}")
    except Exception as e:
        if "InvalidAllocationId.NotFound" in str(e):
            print(f"  ⏭️  EIP already released: {allocation_id}")
        else:
            raise


def delete_vswitch(vswitch_id: str) -> None:
    """Delete a vSwitch."""
    client = get_vpc_client()
    try:
        client.delete_vswitch(vpc_models.DeleteVSwitchRequest(
            region_id=config.region,
            v_switch_id=vswitch_id,
        ))
        print(f"  ✅ vSwitch deleted: {vswitch_id}")
    except Exception as e:
        if "InvalidVSwitchId.NotFound" in str(e):
            print(f"  ⏭️  vSwitch already deleted: {vswitch_id}")
        else:
            raise


def delete_vpc(vpc_id: str) -> None:
    """Delete a VPC."""
    client = get_vpc_client()
    try:
        client.delete_vpc(vpc_models.DeleteVpcRequest(
            region_id=config.region,
            vpc_id=vpc_id,
        ))
        print(f"  ✅ VPC deleted: {vpc_id}")
    except Exception as e:
        if "InvalidVpcId.NotFound" in str(e):
            print(f"  ⏭️  VPC already deleted: {vpc_id}")
        else:
            raise


def destroy_hiclaw_vpc() -> bool:
    """
    Destroy all HiClaw VPC network resources by name lookup.

    Order (reverse of creation):
    1. Delete SNAT entries
    2. Disassociate EIP from NAT
    3. Delete NAT gateway + wait
    4. Release EIP
    5. Delete vSwitches
    6. Delete VPC
    """
    print(f"\n{'='*60}")
    print("Destroying VPC Network Infrastructure")
    print(f"{'='*60}")

    # Find VPC by name
    vpcs = list_vpcs(vpc_name=config.vpc_name)
    target_vpc = None
    for v in vpcs:
        if v["vpc_name"] == config.vpc_name:
            target_vpc = v
            break

    if not target_vpc:
        print(f"⏭️  VPC not found: {config.vpc_name}")
        return True

    vpc_id = target_vpc["vpc_id"]
    print(f"  Found VPC: {vpc_id} ({config.vpc_name})")

    # Find NAT gateway
    req = vpc_models.DescribeNatGatewaysRequest(
        region_id=config.region,
        vpc_id=vpc_id,
        name=config.nat_gateway_name,
    )
    client = get_vpc_client()
    resp = client.describe_nat_gateways(req)
    nat_gw = None
    for ng in resp.body.nat_gateways.nat_gateway:
        if ng.name == config.nat_gateway_name:
            nat_gw = ng
            break

    if nat_gw:
        nat_id = nat_gw.nat_gateway_id
        print(f"  Found NAT Gateway: {nat_id}")

        # 1. Delete SNAT entries
        try:
            delete_snat_entries(nat_id)
        except Exception as e:
            print(f"  ⚠️  SNAT cleanup error (continuing): {e}")

        # 2. Disassociate EIP
        eip_name = f"{config.project_name}-eip"
        eip_req = vpc_models.DescribeEipAddressesRequest(
            region_id=config.region,
            page_size=50,
        )
        eip_resp = client.describe_eip_addresses(eip_req)
        eip_alloc_id = None
        for eip in eip_resp.body.eip_addresses.eip_address:
            if eip.name == eip_name:
                eip_alloc_id = eip.allocation_id
                break

        if eip_alloc_id:
            disassociate_eip_from_nat(eip_alloc_id, nat_id)

        # 3. Delete NAT gateway
        delete_nat_gateway(nat_id)
        wait_nat_gateway_deleted(nat_id)

        # 4. Release EIP
        if eip_alloc_id:
            # Wait a moment for disassociation to settle
            time.sleep(5)
            release_eip(eip_alloc_id)
    else:
        print(f"  ⏭️  NAT Gateway not found: {config.nat_gateway_name}")

    # 5. Delete vSwitches
    vswitches = list_vswitches(vpc_id)
    for vs in vswitches:
        delete_vswitch(vs["vswitch_id"])

    # 6. Delete VPC (may need retry if resources are still releasing)
    time.sleep(5)
    delete_vpc(vpc_id)

    print(f"  ✅ VPC network infrastructure destroyed")
    return True


if __name__ == "__main__":
    # Show current VPC info
    vpcs = list_vpcs()
    print(f"Existing VPCs: {len(vpcs)}")
    for v in vpcs:
        print(f"  - {v['vpc_id']}: {v['vpc_name']} ({v['cidr_block']}, {v['status']})")
