#!/usr/bin/env python3
"""
Bridge Manager openclaw.json to CoPaw config.

This script is a Manager-specific wrapper around copaw_worker.bridge.
It calls the base bridge (shared with Workers) then post-processes
config.json to add Manager-only fields:

  - user_id derivation from env + openclaw.json
  - require_approval: False
  - heartbeat config bridging
  - system_prompt_files (includes TOOLS.md)
  - require_mention: True for group rooms

Usage:
  bridge-manager-config.py --openclaw-json <path> --working-dir <path>
"""
from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any

# Ensure copaw_worker.bridge is importable
sys.path.insert(0, "/opt/hiclaw/copaw/src")

from copaw_worker.bridge import bridge_openclaw_to_copaw  # noqa: E402


def _derive_user_id(cfg: dict[str, Any]) -> str:
    """Derive Matrix user_id from config or environment variables."""
    matrix_raw = cfg.get("channels", {}).get("matrix", {})
    user_id = matrix_raw.get("userId") or matrix_raw.get("user_id")

    if user_id:
        return user_id

    matrix_domain = os.environ.get(
        "HICLAW_MATRIX_DOMAIN", os.environ.get("MATRIX_DOMAIN", "")
    )
    if matrix_domain:
        local_name = os.environ.get(
            "HICLAW_WORKER_NAME", os.environ.get("WORKER_NAME", "manager")
        )
        return f"@{local_name}:{matrix_domain}"

    return ""


def post_process_config(
    openclaw_cfg: dict[str, Any],
    working_dir: Path,
) -> None:
    """Patch config.json with Manager-only fields after base bridge runs."""
    config_path = working_dir / "config.json"
    if not config_path.exists():
        return

    with open(config_path) as f:
        config = json.load(f)

    # --- user_id ---
    user_id = _derive_user_id(openclaw_cfg)
    matrix_cfg = config.setdefault("channels", {}).setdefault("matrix", {})
    if user_id:
        matrix_cfg["user_id"] = user_id
    else:
        print("WARNING: Could not derive Matrix user_id, channel config may be incomplete", flush=True)
    matrix_cfg["require_mention"] = True

    # --- require_approval: False ---
    config.setdefault("agents", {}).setdefault("running", {})[
        "require_approval"
    ] = False

    # --- heartbeat config ---
    heartbeat_raw = (
        openclaw_cfg.get("agents", {})
        .get("defaults", {})
        .get("heartbeat", {})
    )
    if heartbeat_raw:
        heartbeat_cfg: dict[str, Any] = {"enabled": True}
        if "every" in heartbeat_raw:
            heartbeat_cfg["every"] = heartbeat_raw["every"]
        if "target" in heartbeat_raw:
            heartbeat_cfg["target"] = heartbeat_raw["target"]
        if "activeHours" in heartbeat_raw:
            heartbeat_cfg["active_hours"] = heartbeat_raw["activeHours"]
        config["heartbeat"] = heartbeat_cfg

    # --- system_prompt_files (includes TOOLS.md for Manager) ---
    config.setdefault("agents", {})["system_prompt_files"] = [
        "AGENTS.md", "SOUL.md", "PROFILE.md", "TOOLS.md"
    ]

    # --- console channel: enable for Manager (base bridge disables it for Workers) ---
    config.setdefault("channels", {}).setdefault("console", {})["enabled"] = True

    with open(config_path, "w") as f:
        json.dump(config, f, indent=2, ensure_ascii=False)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Bridge Manager openclaw.json to CoPaw config (Manager-specific)"
    )
    parser.add_argument(
        "--openclaw-json",
        required=True,
        help="Path to openclaw.json",
    )
    parser.add_argument(
        "--working-dir",
        required=True,
        help="CoPaw working directory (e.g. /root/manager-workspace/.copaw)",
    )
    args = parser.parse_args()

    openclaw_path = Path(args.openclaw_json)
    if not openclaw_path.exists():
        print(f"ERROR: {openclaw_path} not found", flush=True)
        raise SystemExit(1)

    working_dir = Path(args.working_dir)
    working_dir.mkdir(parents=True, exist_ok=True)

    with open(openclaw_path) as f:
        openclaw_cfg = json.load(f)

    # Step 1: Run base bridge (shared with Workers, includes embedding support)
    bridge_openclaw_to_copaw(openclaw_cfg, working_dir)

    # Step 2: Add Manager-only fields
    post_process_config(openclaw_cfg, working_dir)

    print(f"Bridged {openclaw_path} -> {working_dir} (manager)", flush=True)


if __name__ == "__main__":
    main()
