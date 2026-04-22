"""
Bridge: translate a Controller-side config (today: openclaw.json) into CoPaw's
on-disk runtime files.

The bridge runs in two distinct phases on every invocation:

1. **create** — when a target file is missing, copy it from an in-tree template
   (``copaw_worker/templates/*.json``) verbatim. Templates carry *all* defaults
   (security off, channels.console enabled, identity fields, etc.), so the
   policy engine no longer needs to encode any "safe default" logic.

2. **restart (overlay)** — after the file exists, apply ``_CONTROLLER_FIELDS``
   in order: only the handful of keys that Controller genuinely owns get
   refreshed from openclaw.json. Everything else — user edits, CoPaw's own
   migration writes — is left alone.

This split replaces the old "big policy table per file" model, where every
leaf default was a ``local-wins`` lambda scattered across ~60 entries. Defaults
now live in human-readable JSON, and the code only needs to describe the
controller → CoPaw mapping.

Three merge policies remain for fields Controller touches on every restart:

* ``remote-wins`` — Controller value overwrites existing leaf.
* ``union`` — remote list ∪ local list (dedup, local order first).
* ``deep-merge`` — recursive dict merge with local-wins-at-leaves. Controller
  may ADD keys but cannot overwrite an existing leaf. Used for
  ``channels.matrix.groups``.

``env`` is NOT bridged: Controller does not carry env through openclaw.json,
and the bridge never touches ``agent.json.env``. Users/agents edit that field
themselves via the CoPaw UI or by hand.

Files touched on each invocation (assuming ``agent="default"``):

  - ``<working_dir>/config.json`` — global CoPaw config (create-only).
    Contains security defaults. Once created, bridge never modifies it.
  - ``<working_dir>/workspaces/default/agent.json`` — per-agent config.
    Created from ``agent.{profile}.json`` template, then overlaid with
    controller fields on every restart.
  - ``<working_dir>/providers.json`` — LLM creds (always remote-wins,
    controller is sole source of truth for provider endpoints/keys).
  - ``<working_dir>.secret/providers.json`` — the path copaw actually reads.
"""
from __future__ import annotations

import json
import logging
import os
import shutil
from importlib import resources
from pathlib import Path
from typing import Any, Callable

logger = logging.getLogger(__name__)


# Sentinel returned by derivers to mean "skip this policy this run" (the
# corresponding key is left as-is in agent.json).
_MISSING: Any = object()


# ---------------------------------------------------------------------------
# Environment / path helpers
# ---------------------------------------------------------------------------

def _port_remap(url: str, is_container: bool) -> str:
    """Remap container-internal :8080 to host-exposed gateway port when needed."""
    if not is_container and url and ":8080" in url:
        gateway_port = os.environ.get("HICLAW_PORT_GATEWAY", "18080")
        return url.replace(":8080", f":{gateway_port}")
    return url


def _is_in_container() -> bool:
    return Path("/.dockerenv").exists() or Path("/run/.containerenv").exists()


def _secret_dir(working_dir: Path) -> Path:
    """Return the secret dir path that copaw uses alongside working_dir."""
    return Path(str(working_dir) + ".secret")


def _patch_copaw_paths(working_dir: Path) -> None:
    """Patch copaw's module-level path constants to point at working_dir.

    copaw.constant captures WORKING_DIR / SECRET_DIR at import time from
    env vars, so setting COPAW_WORKING_DIR after import has no effect.
    We must update the live module objects directly.
    """
    secret_dir = _secret_dir(working_dir)
    secret_dir.mkdir(parents=True, exist_ok=True)

    try:
        import copaw.constant as _const
        _const.WORKING_DIR = working_dir
        _const.SECRET_DIR = secret_dir
        _const.ACTIVE_SKILLS_DIR = working_dir / "active_skills"
        _const.CUSTOMIZED_SKILLS_DIR = working_dir / "customized_skills"
        _const.MEMORY_DIR = working_dir / "memory"
        _const.CUSTOM_CHANNELS_DIR = working_dir / "custom_channels"
        _const.MODELS_DIR = working_dir / "models"
    except ImportError:
        pass

    try:
        import copaw.providers.store as _store
        _store._PROVIDERS_JSON = secret_dir / "providers.json"
        _store._LEGACY_PROVIDERS_JSON_CANDIDATES = (
            Path(__file__).resolve().parent / "providers.json",
            working_dir / "providers.json",
        )
    except ImportError:
        pass

    try:
        import copaw.envs.store as _envs
        _envs._BOOTSTRAP_WORKING_DIR = working_dir
        _envs._BOOTSTRAP_SECRET_DIR = secret_dir
        _envs._ENVS_JSON = secret_dir / "envs.json"
        _envs._LEGACY_ENVS_JSON_CANDIDATES = (working_dir / "envs.json",)
    except (ImportError, AttributeError):
        pass


# ---------------------------------------------------------------------------
# Template installation
# ---------------------------------------------------------------------------

def _template_text(name: str) -> str:
    """Read a template by basename from the in-tree templates/ directory."""
    return (resources.files("copaw_worker") / "templates" / name).read_text(
        encoding="utf-8"
    )


def _install_from_template(dst: Path, template_name: str) -> bool:
    """Copy template → dst only if dst is missing. Returns True when installed.

    Parent directories are created as needed. Existing files are left alone
    unconditionally — this is the "create phase" contract.
    """
    if dst.exists():
        return False
    dst.parent.mkdir(parents=True, exist_ok=True)
    dst.write_text(_template_text(template_name), encoding="utf-8")
    logger.info("bridge: installed %s from template %s", dst, template_name)
    return True


# ---------------------------------------------------------------------------
# Bridge entry point
# ---------------------------------------------------------------------------

def bridge_controller_to_copaw(
    controller_config: dict[str, Any],
    working_dir: Path,
    *,
    profile: str = "worker",
    agent: str = "default",
) -> None:
    """Bridge a Controller-side config into CoPaw's runtime files.

    Args:
        controller_config: parsed openclaw.json dict (future: may be a
            different shape from a different controller format).
        working_dir: CoPaw working dir (e.g. ``~/.copaw``).
        profile: ``"worker"`` or ``"manager"``. Selects which
            ``agent.<profile>.json`` template is used on first boot.
        agent: CoPaw workspace key; determines
            ``workspaces/<agent>/agent.json``. Defaults to ``"default"``.
            Exposed for future multi-agent-per-container scenarios.
    """
    if profile not in ("worker", "manager"):
        raise ValueError(
            f"unknown bridge profile: {profile!r} (use 'worker' or 'manager')"
        )

    working_dir.mkdir(parents=True, exist_ok=True)
    in_container = _is_in_container()

    _write_config_json(working_dir)
    _write_providers_json(controller_config, working_dir, in_container)
    _write_agent_json(
        controller_config,
        working_dir,
        in_container,
        profile=profile,
        agent=agent,
    )

    os.environ["COPAW_WORKING_DIR"] = str(working_dir)
    _patch_copaw_paths(working_dir)

    secret_dir = _secret_dir(working_dir)
    providers_src = working_dir / "providers.json"
    if providers_src.exists():
        shutil.copy2(providers_src, secret_dir / "providers.json")


# Deprecated alias — callers should migrate to ``bridge_controller_to_copaw``.
# Kept as a thin wrapper so existing imports (start-copaw-manager.sh, tests)
# do not break mid-refactor. Remove once all call sites are updated.
def bridge_openclaw_to_copaw(
    openclaw_cfg: dict[str, Any],
    working_dir: Path,
    *,
    profile: str = "worker",
) -> None:
    bridge_controller_to_copaw(openclaw_cfg, working_dir, profile=profile)


# ---------------------------------------------------------------------------
# Resolvers — read a single derived value out of the controller config
# ---------------------------------------------------------------------------

def _matrix_raw(cfg: dict[str, Any]) -> dict[str, Any]:
    return cfg.get("channels", {}).get("matrix", {})


def _resolve_active_model(cfg: dict[str, Any]) -> dict[str, Any] | None:
    """Return the config dict of the active model from openclaw.json, or None.

    Prefers ``agents.defaults.model.primary`` ("provider_id/model_id");
    falls back to the first model of the first provider.
    """
    providers_raw = cfg.get("models", {}).get("providers", {})
    if not providers_raw:
        return None

    primary = (
        cfg.get("agents", {})
        .get("defaults", {})
        .get("model", {})
        .get("primary", "")
    )

    if primary and "/" in primary:
        pid, mid = primary.split("/", 1)
        provider = providers_raw.get(pid, {})
        for m in provider.get("models", []):
            if m.get("id") == mid:
                return m

    for provider_cfg in providers_raw.values():
        models = provider_cfg.get("models", [])
        if models:
            return models[0]

    return None


def _resolve_context_window(cfg: dict[str, Any]) -> int | None:
    m = _resolve_active_model(cfg)
    if m and "contextWindow" in m:
        return int(m["contextWindow"])
    return None


def _resolve_vision_enabled(cfg: dict[str, Any]) -> bool:
    """True if the active model declares image input support."""
    m = _resolve_active_model(cfg)
    if m is None:
        return False
    return "image" in m.get("input", [])


def _resolve_embedding_config(
    cfg: dict[str, Any],
    in_container: bool,
) -> dict[str, Any] | None:
    """Extract embedding config from openclaw's ``agents.defaults.memorySearch``."""
    memory_search = (
        cfg.get("agents", {})
        .get("defaults", {})
        .get("memorySearch", {})
    )
    if not memory_search:
        return None

    remote = memory_search.get("remote", {})
    base_url = _port_remap(remote.get("baseUrl", ""), in_container)
    api_key = remote.get("apiKey", "")
    model = memory_search.get("model", "")

    if not base_url or not model:
        return None

    if not api_key:
        logger.warning(
            "memorySearch.remote.apiKey is empty; embedding requests will likely fail",
        )

    dimensions = (
        memory_search.get("outputDimensionality")
        or int(os.environ.get("HICLAW_EMBEDDING_DIMENSIONS", "0"))
        or 1024
    )

    return {
        "backend": "openai",
        "api_key": api_key,
        "base_url": base_url,
        "model_name": model,
        "dimensions": dimensions,
        "enable_cache": True,
        "use_dimensions": False,
    }


def _resolve_history_limit(cfg: dict[str, Any]) -> int | None:
    matrix_raw = _matrix_raw(cfg)
    hl = matrix_raw.get("historyLimit")
    if hl is None:
        hl = cfg.get("messages", {}).get("groupChat", {}).get("historyLimit")
    return int(hl) if hl is not None else None


def _derive_matrix_user_id(cfg: dict[str, Any], _in_container: bool = False) -> Any:
    """Matrix user_id: prefer openclaw channels.matrix.userId/user_id, else
    synthesise ``@${HICLAW_WORKER_NAME or WORKER_NAME or 'manager'}:${HICLAW_MATRIX_DOMAIN}``.

    Controller's agentconfig generator always writes ``channels.matrix.userId``
    for Workers; the env-fallback is for Manager whose openclaw.json
    historically does not carry a userId.

    Returns ``_MISSING`` when nothing is derivable, so we never clobber an
    existing agent.json leaf with an empty string.
    """
    m = _matrix_raw(cfg)
    uid = m.get("userId") or m.get("user_id")
    if uid:
        return uid
    domain = os.environ.get("HICLAW_MATRIX_DOMAIN") or os.environ.get("MATRIX_DOMAIN", "")
    if not domain:
        return _MISSING
    local = os.environ.get("HICLAW_WORKER_NAME") or os.environ.get("WORKER_NAME", "manager")
    return f"@{local}:{domain}"


def _derive_heartbeat(cfg: dict[str, Any], _in_container: bool = False) -> Any:
    """Map openclaw agents.defaults.heartbeat → copaw heartbeat block.

    Returns ``_MISSING`` when openclaw did not declare heartbeat so we do
    not clobber an existing agent-side setting.
    """
    hb = cfg.get("agents", {}).get("defaults", {}).get("heartbeat")
    if not isinstance(hb, dict) or not hb:
        return _MISSING
    out: dict[str, Any] = {"enabled": True}
    if "every" in hb:
        out["every"] = hb["every"]
    if "target" in hb:
        out["target"] = hb["target"]
    if "activeHours" in hb:
        out["active_hours"] = hb["activeHours"]
    return out


# ---------------------------------------------------------------------------
# Policy engine (restart-phase overlay only)
# ---------------------------------------------------------------------------

def _get_path(container: dict[str, Any], path: tuple[str, ...]) -> Any:
    """Return value at ``path`` inside nested dicts, or ``_MISSING``."""
    node: Any = container
    for key in path:
        if not isinstance(node, dict) or key not in node:
            return _MISSING
        node = node[key]
    return node


def _set_path(container: dict[str, Any], path: tuple[str, ...], value: Any) -> None:
    """Assign ``value`` at ``path``, creating intermediate dicts as needed."""
    node = container
    for key in path[:-1]:
        nxt = node.get(key)
        if not isinstance(nxt, dict):
            nxt = {}
            node[key] = nxt
        node = nxt
    node[path[-1]] = value


def _deep_merge_local_wins(remote: Any, local: Any) -> Any:
    """Deep-merge two JSON trees where local leaves win over remote."""
    if isinstance(remote, dict) and isinstance(local, dict):
        out: dict[str, Any] = {}
        for k in remote.keys() | local.keys():
            if k in remote and k in local:
                out[k] = _deep_merge_local_wins(remote[k], local[k])
            elif k in remote:
                out[k] = remote[k]
            else:
                out[k] = local[k]
        return out
    return local


def _union_list(remote: list[Any] | None, local: list[Any] | None) -> list[Any]:
    """Concat local then remote, dedup preserving order. Local entries win order."""
    seen: set[str] = set()
    out: list[Any] = []
    for item in (local or []) + (remote or []):
        try:
            key = (
                json.dumps(item, sort_keys=True)
                if isinstance(item, (dict, list))
                else repr(item)
            )
        except TypeError:
            key = repr(item)
        if key not in seen:
            seen.add(key)
            out.append(item)
    return out


def _apply_policy(
    existing: dict[str, Any],
    path: tuple[str, ...],
    policy: str,
    remote_value: Any,
) -> None:
    """Apply one merge policy for one path. ``remote_value == _MISSING`` skips."""
    if remote_value is _MISSING:
        return

    if policy == "remote-wins":
        _set_path(existing, path, remote_value)
        return

    if policy == "union":
        local_value = _get_path(existing, path)
        local_list = local_value if isinstance(local_value, list) else []
        remote_list = remote_value if isinstance(remote_value, list) else []
        _set_path(existing, path, _union_list(remote_list, local_list))
        return

    if policy == "deep-merge":
        local_value = _get_path(existing, path)
        if local_value is _MISSING:
            _set_path(existing, path, remote_value)
        else:
            _set_path(existing, path, _deep_merge_local_wins(remote_value, local_value))
        return

    raise ValueError(f"unknown merge policy: {policy}")


# ---------------------------------------------------------------------------
# Controller field table — slim restart-overlay list
# ---------------------------------------------------------------------------

# Each entry: (path, policy, deriver). ``deriver(cfg, in_container)`` returns
# the remote value to apply, or ``_MISSING`` to signal "don't touch this key
# on this run". Defaults for everything NOT in this list live in the
# templates under copaw_worker/templates/.
_PolicyDeriver = Callable[[dict[str, Any], bool], Any]


_CONTROLLER_FIELDS: list[tuple[tuple[str, ...], str, _PolicyDeriver]] = [
    # ── channels.matrix: controller-owned scalars ────────────────────────
    (("channels", "matrix", "enabled"),
     "remote-wins", lambda c, _: _matrix_raw(c).get("enabled", True)),
    (("channels", "matrix", "homeserver"),
     "remote-wins", lambda c, ic: _port_remap(_matrix_raw(c).get("homeserver", ""), ic)),
    (("channels", "matrix", "access_token"),
     "remote-wins", lambda c, _: _matrix_raw(c).get("accessToken", "")),
    (("channels", "matrix", "user_id"),
     "remote-wins", _derive_matrix_user_id),
    (("channels", "matrix", "encryption"),
     "remote-wins", lambda c, _: _matrix_raw(c).get("encryption", False)),
    (("channels", "matrix", "dm_policy"),
     "remote-wins", lambda c, _: _matrix_raw(c).get("dm", {}).get("policy", "allowlist")),
    (("channels", "matrix", "group_policy"),
     "remote-wins", lambda c, _: _matrix_raw(c).get("groupPolicy", "allowlist")),
    (("channels", "matrix", "vision_enabled"),
     "remote-wins", lambda c, _: _resolve_vision_enabled(c)),
    (("channels", "matrix", "history_limit"),
     "remote-wins",
     lambda c, _: _resolve_history_limit(c) if _resolve_history_limit(c) is not None else _MISSING),

    # ── channels.matrix: collection fields ───────────────────────────────
    (("channels", "matrix", "allow_from"),
     "union", lambda c, _: _matrix_raw(c).get("dm", {}).get("allowFrom", []) or []),
    (("channels", "matrix", "group_allow_from"),
     "union", lambda c, _: _matrix_raw(c).get("groupAllowFrom", []) or []),
    (("channels", "matrix", "groups"),
     "deep-merge", lambda c, _: _matrix_raw(c).get("groups", {}) or {}),

    # ── running.* (controller-owned) ─────────────────────────────────────
    (("running", "max_input_length"),
     "remote-wins",
     lambda c, _: _resolve_context_window(c) if _resolve_context_window(c) is not None else _MISSING),
    (("running", "embedding_config"),
     "remote-wins",
     lambda c, ic: _resolve_embedding_config(c, ic) if _resolve_embedding_config(c, ic) is not None else _MISSING),

    # ── heartbeat ────────────────────────────────────────────────────────
    # Controller-sourced on Manager (via openclaw.agents.defaults.heartbeat);
    # Workers' openclaw.json never declares heartbeat so deriver returns
    # _MISSING and the key is untouched.
    (("heartbeat",), "remote-wins", _derive_heartbeat),
]


# ---------------------------------------------------------------------------
# config.json — create-only from template
# ---------------------------------------------------------------------------

def _write_config_json(working_dir: Path) -> None:
    """Install config.json from template if missing. Never overwrite.

    ``config.json`` is CoPaw's global singleton — it owns cross-workspace
    defaults like ``security.*.enabled``. The bridge seeds safe defaults on
    first boot (tool_guard / file_guard / skill_scanner all disabled) and
    then stays out of the way: user edits via the CoPaw UI or by hand are
    authoritative from that point on.
    """
    _install_from_template(working_dir / "config.json", "config.json")


# ---------------------------------------------------------------------------
# agent.json — template create + controller-field overlay
# ---------------------------------------------------------------------------

def _write_agent_json(
    controller_config: dict[str, Any],
    working_dir: Path,
    in_container: bool,
    *,
    profile: str = "worker",
    agent: str = "default",
) -> None:
    """Create agent.json from template if absent; then overlay controller fields.

    The template carries all defaults (identity, language, console channel,
    matrix channel skeleton, running skeleton, Manager-only require_mention).
    The ``_CONTROLLER_FIELDS`` overlay only touches Matrix scalars and
    running.* fields that Controller genuinely owns — user edits to anything
    else survive restarts. Security concerns (tool_guard / file_guard /
    skill_scanner) live in the global ``config.json`` singleton, never in
    ``agent.json``.
    """
    agent_path = working_dir / "workspaces" / agent / "agent.json"
    _install_from_template(agent_path, f"agent.{profile}.json")

    try:
        with open(agent_path) as f:
            existing = json.load(f)
        if not isinstance(existing, dict):
            raise ValueError("agent.json root is not a dict")
    except Exception as exc:
        logger.warning(
            "agent.json at %s is unreadable (%s); re-seeding from template",
            agent_path,
            exc,
        )
        agent_path.unlink(missing_ok=True)
        _install_from_template(agent_path, f"agent.{profile}.json")
        with open(agent_path) as f:
            existing = json.load(f)

    for path, policy, deriver in _CONTROLLER_FIELDS:
        remote_value = deriver(controller_config, in_container)
        _apply_policy(existing, path, policy, remote_value)

    # workspace_dir depends on local filesystem layout; seed once, never rewrite.
    existing.setdefault("workspace_dir", str(agent_path.parent))

    with open(agent_path, "w") as f:
        json.dump(existing, f, indent=2, ensure_ascii=False)


# ---------------------------------------------------------------------------
# providers.json — always remote-wins (controller is sole source of truth)
# ---------------------------------------------------------------------------

def _write_providers_json(
    cfg: dict[str, Any],
    working_dir: Path,
    in_container: bool,
) -> None:
    providers_raw = cfg.get("models", {}).get("providers", {})

    custom_providers: dict[str, Any] = {}
    active_provider_id = ""
    active_model = ""

    for provider_id, provider_cfg in providers_raw.items():
        base_url = _port_remap(provider_cfg.get("baseUrl", ""), in_container)
        api_key = provider_cfg.get("apiKey", "")

        models_raw = provider_cfg.get("models", [])
        models = [
            {"id": m["id"], "name": m.get("name", m["id"])}
            for m in models_raw
            if m.get("id")
        ]

        custom_providers[provider_id] = {
            "id": provider_id,
            "name": provider_id,
            "default_base_url": base_url,
            "api_key_prefix": "",
            "models": models,
            "base_url": base_url,
            "api_key": api_key,
            "chat_model": "OpenAIChatModel",
        }

        if not active_provider_id and models:
            active_provider_id = provider_id
            active_model = models[0]["id"]

    primary = (
        cfg.get("agents", {})
        .get("defaults", {})
        .get("model", {})
        .get("primary", "")
    )
    if primary and "/" in primary:
        pid, mid = primary.split("/", 1)
        if pid in custom_providers:
            active_provider_id = pid
            active_model = mid

    providers_data: dict[str, Any] = {
        "providers": {},
        "custom_providers": custom_providers,
        "active_llm": {
            "provider_id": active_provider_id,
            "model": active_model,
        },
    }

    providers_path = working_dir / "providers.json"
    with open(providers_path, "w") as f:
        json.dump(providers_data, f, indent=2, ensure_ascii=False)


# ---------------------------------------------------------------------------
# CLI entry point — used by manager/scripts/init/start-copaw-manager.sh
# ---------------------------------------------------------------------------

def _main_cli(argv: list[str] | None = None) -> int:
    import argparse

    parser = argparse.ArgumentParser(
        prog="python -m copaw_worker.bridge",
        description=(
            "Bridge Controller config (openclaw.json today) into CoPaw's "
            "config.json / agent.json / providers.json."
        ),
    )
    parser.add_argument("--openclaw-json", required=True,
                        help="Path to the controller config file (openclaw.json)")
    parser.add_argument("--working-dir", required=True,
                        help="CoPaw working dir (e.g. ~/.copaw)")
    parser.add_argument("--profile", default="worker", choices=["worker", "manager"],
                        help="Template profile to use on first boot")
    parser.add_argument("--agent", default="default",
                        help="CoPaw workspace key (maps to workspaces/<agent>/). "
                             "Default: 'default'. Exposed for multi-agent setups.")
    args = parser.parse_args(argv)

    openclaw_path = Path(args.openclaw_json)
    if not openclaw_path.exists():
        print(f"ERROR: {openclaw_path} not found", flush=True)
        return 1

    working_dir = Path(args.working_dir)
    working_dir.mkdir(parents=True, exist_ok=True)

    with open(openclaw_path) as f:
        controller_config = json.load(f)

    bridge_controller_to_copaw(
        controller_config,
        working_dir,
        profile=args.profile,
        agent=args.agent,
    )
    print(
        f"Bridged {openclaw_path} -> {working_dir} "
        f"(profile={args.profile}, agent={args.agent})",
        flush=True,
    )
    return 0


if __name__ == "__main__":
    import sys
    sys.exit(_main_cli())
