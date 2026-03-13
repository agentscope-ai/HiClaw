#!/usr/bin/env python3
"""
HiClaw Cloud - OSS Management

Create and configure OSS bucket for HiClaw file storage.
"""

import json
from typing import Optional, List
import alibabacloud_oss_v2 as oss

from .config import config
from .clients import get_oss_client


def create_bucket(bucket_name: str = None) -> bool:
    """
    Create an OSS bucket.
    
    Returns: True if created or already exists
    """
    client = get_oss_client()
    bucket_name = bucket_name or config.oss_bucket_name
    
    # Check if bucket exists
    try:
        client.get_bucket_info(oss.GetBucketInfoRequest(bucket=bucket_name))
        print(f"⏭️  OSS Bucket already exists: {bucket_name}")
        return True
    except Exception as e:
        if "NoSuchBucket" not in str(e):
            raise
    
    # Create bucket
    req = oss.PutBucketRequest(
        bucket=bucket_name,
        acl="private",
        create_bucket_configuration=oss.CreateBucketConfiguration(
            storage_class="Standard"
        )
    )
    
    client.put_bucket(req)
    print(f"✅ Created OSS Bucket: {bucket_name}")
    return True


def setup_bucket_cors(bucket_name: str = None) -> bool:
    """Configure CORS for web access"""
    client = get_oss_client()
    bucket_name = bucket_name or config.oss_bucket_name
    
    cors_config = oss.PutBucketCorsRequest(
        bucket=bucket_name,
        cors_configuration=oss.CORSConfiguration(
            cors_rules=[
                oss.CORSRule(
                    allowed_origins=["*"],
                    allowed_methods=["GET", "PUT", "POST", "DELETE", "HEAD"],
                    allowed_headers=["*"],
                    expose_headers=["ETag", "x-oss-request-id"],
                    max_age_seconds=3600
                )
            ]
        )
    )
    
    client.put_bucket_cors(cors_config)
    print(f"✅ CORS configured for bucket: {bucket_name}")
    return True


def put_object(key: str, content: str, bucket_name: str = None) -> bool:
    """
    Upload content to OSS.
    
    Args:
        key: Object key (path)
        content: String content
        bucket_name: Optional bucket name
    
    Returns: True if successful
    """
    client = get_oss_client()
    bucket_name = bucket_name or config.oss_bucket_name
    
    req = oss.PutObjectRequest(
        bucket=bucket_name,
        key=key,
        body=content.encode('utf-8')
    )
    
    client.put_object(req)
    print(f"✅ Uploaded: {key}")
    return True


def get_object(key: str, bucket_name: str = None) -> Optional[str]:
    """
    Download content from OSS.
    
    Returns: Content as string, or None if not found
    """
    client = get_oss_client()
    bucket_name = bucket_name or config.oss_bucket_name
    
    try:
        req = oss.GetObjectRequest(bucket=bucket_name, key=key)
        result = client.get_object(req)
        return result.body.content.decode('utf-8')
    except Exception as e:
        if "NoSuchKey" in str(e):
            return None
        raise


def delete_object(key: str, bucket_name: str = None) -> bool:
    """Delete an object from OSS"""
    client = get_oss_client()
    bucket_name = bucket_name or config.oss_bucket_name
    
    req = oss.DeleteObjectRequest(bucket=bucket_name, key=key)
    client.delete_object(req)
    return True


def list_objects(prefix: str = "", bucket_name: str = None) -> List[str]:
    """
    List objects in a bucket with optional prefix.
    
    Returns: List of object keys
    """
    client = get_oss_client()
    bucket_name = bucket_name or config.oss_bucket_name
    
    req = oss.ListObjectsV2Request(
        bucket=bucket_name,
        prefix=prefix,
        max_keys=1000
    )
    
    result = client.list_objects_v2(req)
    
    keys = []
    if result.contents:
        for obj in result.contents:
            keys.append(obj.key)
    
    return keys


def setup_hiclaw_storage() -> bool:
    """
    Setup OSS bucket and initial directory structure for HiClaw.
    
    Directory structure:
    hiclaw-cloud-storage/
    ├── agents/           # Worker agent configs
    │   └── <worker>/
    │       ├── SOUL.md
    │       ├── AGENTS.md
    │       ├── openclaw.json
    │       └── skills/
    ├── shared/           # Shared data
    │   ├── tasks/        # Task specs and results
    │   └── knowledge/    # Shared reference materials
    └── workers/          # Worker work products
    """
    print(f"\n{'='*60}")
    print("Setting up HiClaw OSS Storage")
    print(f"{'='*60}")
    
    # Create bucket
    create_bucket()
    
    # Setup CORS
    setup_bucket_cors()
    
    # Create directory markers
    directories = [
        "agents/.keep",
        "shared/tasks/.keep",
        "shared/knowledge/.keep",
        "workers/.keep"
    ]
    
    print(f"\nCreating directory structure...")
    for d in directories:
        put_object(d, "")
    
    print(f"\n✅ OSS Storage setup complete: {config.oss_bucket_name}")
    return True


def create_worker_config(
    worker_name: str,
    matrix_token: str,
    gateway_key: str,
    matrix_homeserver: str,
    ai_gateway_url: str
) -> bool:
    """
    Create configuration files for a worker agent in OSS.
    
    Args:
        worker_name: Worker name (e.g., "alice")
        matrix_token: Matrix access token
        gateway_key: AI Gateway key-auth token
        matrix_homeserver: Matrix server URL
        ai_gateway_url: AI Gateway URL
    """
    prefix = f"agents/{worker_name}/"
    
    # SOUL.md
    soul_md = f"""# Worker Agent: {worker_name}

You are a HiClaw Worker Agent deployed on Alibaba Cloud.

## Identity
- Name: {worker_name}
- Role: Task executor
- Manager: @manager

## Behavior
- Execute tasks assigned by Manager via Matrix
- Report progress and results in the Matrix room
- Use file-sync skill to sync files with OSS
- Use mcporter to call MCP tools via AI Gateway

## Communication
- Respond to @mentions from Manager
- Keep human admin informed of progress
- Ask for clarification when needed
"""
    
    # AGENTS.md
    agents_md = f"""# Worker Workspace: {worker_name}

This is the workspace for HiClaw Worker Agent `{worker_name}`.

## Directory Structure
- `~/` - Worker home (synced to OSS)
- `~/skills/` - Worker skills
- `~/memory/` - Worker memory files

## Key Files
- `SOUL.md` - Worker identity and behavior
- `openclaw.json` - OpenClaw configuration
- `mcporter-servers.json` - MCP Server configuration
"""
    
    # openclaw.json
    openclaw_config = {
        "channels": {
            "matrix": {
                "homeserver": matrix_homeserver,
                "accessToken": matrix_token,
                "syncEnabled": True
            }
        },
        "models": {
            "providers": {
                "hiclaw-gateway": {
                    "type": "openai-compat",
                    "apiUrl": f"{ai_gateway_url}/v1",
                    "apiKey": gateway_key,
                    "models": [
                        {
                            "id": config.default_model,
                            "name": config.default_model,
                            "contextWindow": 960000,
                            "maxTokens": 64000
                        }
                    ]
                }
            }
        },
        "agents": {
            "defaults": {
                "model": {
                    "primary": f"hiclaw-gateway/{config.default_model}"
                }
            }
        }
    }
    
    # mcporter-servers.json
    mcporter_config = {
        "servers": {
            "github": {
                "url": f"{ai_gateway_url}/mcp/github",
                "headers": {
                    "Authorization": f"Bearer {gateway_key}"
                }
            }
        }
    }
    
    # Upload files
    put_object(f"{prefix}SOUL.md", soul_md)
    put_object(f"{prefix}AGENTS.md", agents_md)
    put_object(f"{prefix}openclaw.json", json.dumps(openclaw_config, indent=2))
    put_object(f"{prefix}mcporter-servers.json", json.dumps(mcporter_config, indent=2))
    put_object(f"{prefix}skills/.keep", "")
    put_object(f"{prefix}memory/.keep", "")
    
    print(f"✅ Worker config created: {worker_name}")
    return True


def delete_bucket(bucket_name: str = None, force: bool = False) -> bool:
    """
    Delete an OSS bucket.
    
    Args:
        bucket_name: Bucket to delete
        force: If True, delete all objects first
    """
    client = get_oss_client()
    bucket_name = bucket_name or config.oss_bucket_name
    
    if force:
        print(f"Deleting all objects in {bucket_name}...")
        keys = list_objects("", bucket_name)
        for key in keys:
            delete_object(key, bucket_name)
    
    req = oss.DeleteBucketRequest(bucket=bucket_name)
    client.delete_bucket(req)
    print(f"✅ Deleted OSS Bucket: {bucket_name}")
    return True


if __name__ == "__main__":
    # Test: Setup storage
    setup_hiclaw_storage()
