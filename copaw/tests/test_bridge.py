"""Tests for bridge.py embedding config bridging."""

import json
import logging
import tempfile
from pathlib import Path

from copaw_worker.bridge import bridge_openclaw_to_copaw


def _make_openclaw_cfg(**memory_search_overrides):
    """Helper to build openclaw config with optional memorySearch overrides."""
    base = {
        "channels": {"matrix": {"enabled": True, "homeserver": "http://localhost:6167", "accessToken": "tok"}},
        "models": {
            "providers": {
                "gw": {"baseUrl": "http://aigw:8080/v1", "apiKey": "key123", "models": [{"id": "qwen3.5-plus", "name": "qwen3.5-plus"}]}
            }
        },
        "agents": {"defaults": {"model": {"primary": "gw/qwen3.5-plus"}}},
    }
    if memory_search_overrides is not None:
        base["agents"]["defaults"]["memorySearch"] = {
            "provider": "openai",
            "model": "text-embedding-v4",
            "remote": {
                "baseUrl": "http://aigw:8080/v1",
                "apiKey": "key123",
            },
            **memory_search_overrides,
        }
    return base


def _bridge_and_read(openclaw_cfg):
    """Bridge config and return parsed config.json."""
    with tempfile.TemporaryDirectory() as tmpdir:
        working_dir = Path(tmpdir) / "agent"
        bridge_openclaw_to_copaw(openclaw_cfg, working_dir)
        with open(working_dir / "config.json") as f:
            return json.load(f)


def test_bridge_embedding_config():
    """memorySearch in openclaw.json should produce embedding_config in config.json."""
    config = _bridge_and_read(_make_openclaw_cfg())

    emb = config["agents"]["running"]["embedding_config"]
    assert emb["backend"] == "openai"
    assert emb["model_name"] == "text-embedding-v4"
    # _port_remap converts :8080 → :18080 when not in container
    assert emb["base_url"] == "http://aigw:18080/v1"
    assert emb["api_key"] == "key123"
    assert emb["dimensions"] == 1024
    assert emb["enable_cache"] is True


def test_bridge_no_embedding_config():
    """Without memorySearch, embedding_config should not be written."""
    openclaw_cfg = {
        "channels": {"matrix": {"enabled": True, "homeserver": "http://localhost:6167", "accessToken": "tok"}},
        "models": {
            "providers": {
                "gw": {"baseUrl": "http://aigw:8080/v1", "apiKey": "key123", "models": [{"id": "qwen3.5-plus", "name": "qwen3.5-plus"}]}
            }
        },
        "agents": {"defaults": {"model": {"primary": "gw/qwen3.5-plus"}}},
    }
    config = _bridge_and_read(openclaw_cfg)

    running = config.get("agents", {}).get("running", {})
    assert "embedding_config" not in running


def test_bridge_embedding_config_empty_api_key(caplog):
    """Empty api_key should still produce config (for no-auth envs) but log a warning."""
    cfg = _make_openclaw_cfg(remote={"baseUrl": "http://aigw:8080/v1", "apiKey": ""})

    with caplog.at_level(logging.WARNING, logger="copaw_worker.bridge"):
        config = _bridge_and_read(cfg)

    emb = config["agents"]["running"]["embedding_config"]
    assert emb["api_key"] == ""
    assert emb["model_name"] == "text-embedding-v4"
    assert "apiKey is empty" in caplog.text


def test_bridge_embedding_config_custom_dimensions():
    """outputDimensionality from openclaw should override default 1024."""
    cfg = _make_openclaw_cfg(outputDimensionality=768)
    config = _bridge_and_read(cfg)

    emb = config["agents"]["running"]["embedding_config"]
    assert emb["dimensions"] == 768
