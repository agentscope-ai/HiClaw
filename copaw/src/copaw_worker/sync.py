"""MinIO file sync for copaw-worker.

All MinIO operations use the `mc` CLI (MinIO Client).
Pulls openclaw.json, SOUL.md, AGENTS.md, and skills from MinIO bucket.
Runs a background loop that re-pulls on interval and calls on_pull callback.
"""
from __future__ import annotations

import asyncio
import json
import logging
import shutil
import subprocess
import time
from pathlib import Path
from typing import Any, Callable, Coroutine, Optional

logger = logging.getLogger(__name__)

# mc alias name used for this worker session
_MC_ALIAS = "hiclaw"


def _mc(*args: str, check: bool = True) -> subprocess.CompletedProcess:
    """Run an mc command and return the result."""
    mc_bin = shutil.which("mc")
    if not mc_bin:
        raise RuntimeError("mc binary not found on PATH. Please install mc first.")
    cmd = [mc_bin, *args]
    logger.info("mc cmd: %s", " ".join(cmd))
    result = subprocess.run(cmd, capture_output=True, text=True, check=check)
    logger.info("mc stdout (%d chars): %r", len(result.stdout), result.stdout[:200])
    if result.stderr:
        logger.info("mc stderr: %r", result.stderr[:200])
    return result


class FileSync:
    """MinIO file sync using mc CLI."""

    def __init__(
        self,
        endpoint: str,
        access_key: str,
        secret_key: str,
        bucket: str,
        worker_name: str,
        secure: bool = False,
        local_dir: Optional[Path] = None,
    ) -> None:
        self.endpoint = endpoint.rstrip("/")
        self.access_key = access_key
        self.secret_key = secret_key
        self.bucket = bucket
        self.worker_name = worker_name
        self._secure = secure
        self.local_dir = local_dir or Path.home() / ".copaw-worker" / worker_name
        self.local_dir.mkdir(parents=True, exist_ok=True)
        self._prefix = f"agents/{worker_name}"
        self._alias_set = False

    # ------------------------------------------------------------------
    # mc alias management
    # ------------------------------------------------------------------

    def _ensure_alias(self) -> None:
        """Set up mc alias (idempotent)."""
        if self._alias_set:
            return
        # endpoint may already include scheme
        if self.endpoint.startswith("http"):
            url = self.endpoint
        else:
            scheme = "https" if self._secure else "http"
            url = f"{scheme}://{self.endpoint}"
        _mc("alias", "set", _MC_ALIAS, url, self.access_key, self.secret_key)
        self._alias_set = True

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _object_path(self, key: str) -> str:
        """Return full mc path: alias/bucket/key"""
        return f"{_MC_ALIAS}/{self.bucket}/{key}"

    def _cat(self, key: str) -> Optional[str]:
        """Download object content as text using mc cat."""
        self._ensure_alias()
        try:
            result = _mc("cat", self._object_path(key), check=True)
            return result.stdout
        except subprocess.CalledProcessError as exc:
            logger.debug("mc cat failed for %s: %s", key, exc.stderr)
            return None
        except Exception as exc:
            logger.debug("mc cat error for %s: %s", key, exc)
            return None

    def _ls(self, prefix: str) -> list[str]:
        """List objects under prefix, return list of relative names."""
        self._ensure_alias()
        try:
            result = _mc("ls", "--recursive", self._object_path(prefix), check=True)
            names = []
            for line in result.stdout.splitlines():
                # mc ls output: "2024-01-01 00:00:00   1234 filename"
                parts = line.strip().split()
                if parts:
                    names.append(parts[-1])
            return names
        except subprocess.CalledProcessError as exc:
            logger.debug("mc ls failed for %s: %s", prefix, exc.stderr)
            return []
        except Exception as exc:
            logger.debug("mc ls error for %s: %s", prefix, exc)
            return []

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def get_config(self) -> dict[str, Any]:
        """Pull openclaw.json and return parsed dict."""
        text = self._cat(f"{self._prefix}/openclaw.json")
        if not text:
            raise RuntimeError(f"openclaw.json not found in MinIO for worker {self.worker_name}")
        logger.info("openclaw.json raw content (%d chars): %r", len(text), text[:500])
        return json.loads(text)

    def get_soul(self) -> Optional[str]:
        return self._cat(f"{self._prefix}/SOUL.md")

    def get_agents_md(self) -> Optional[str]:
        return self._cat(f"{self._prefix}/AGENTS.md")

    def list_skills(self) -> list[str]:
        """Return list of skill names available in MinIO for this worker."""
        prefix = f"{self._prefix}/skills/"
        entries = self._ls(prefix)
        # entries look like "skill-name/SKILL.md"
        skill_names: list[str] = []
        seen: set[str] = set()
        for entry in entries:
            parts = entry.rstrip("/").split("/")
            if parts:
                name = parts[0]
                if name and name not in seen:
                    seen.add(name)
                    skill_names.append(name)
        return skill_names

    def get_skill_md(self, skill_name: str) -> Optional[str]:
        """Pull SKILL.md for a given skill name."""
        return self._cat(f"{self._prefix}/skills/{skill_name}/SKILL.md")

    def pull_all(self) -> list[str]:
        """Pull all known files; return list of filenames that changed."""
        changed: list[str] = []
        files = {
            "openclaw.json": f"{self._prefix}/openclaw.json",
            "SOUL.md": f"{self._prefix}/SOUL.md",
            "AGENTS.md": f"{self._prefix}/AGENTS.md",
        }
        for name, key in files.items():
            content = self._cat(key)
            if content is None:
                continue
            local = self.local_dir / name
            existing = local.read_text() if local.exists() else None
            if content != existing:
                local.write_text(content)
                changed.append(name)

        # Also check for skill changes
        for skill_name in self.list_skills():
            skill_md = self.get_skill_md(skill_name)
            if skill_md is None:
                continue
            local = self.local_dir / "skills" / skill_name / "SKILL.md"
            existing = local.read_text() if local.exists() else None
            if skill_md != existing:
                local.parent.mkdir(parents=True, exist_ok=True)
                local.write_text(skill_md)
                changed.append(f"skills/{skill_name}/SKILL.md")

        return changed


async def sync_loop(
    sync: FileSync,
    interval: int,
    on_pull: Callable[[list[str]], Coroutine],
) -> None:
    """Background task: pull files every `interval` seconds."""
    while True:
        await asyncio.sleep(interval)
        try:
            changed = await asyncio.get_event_loop().run_in_executor(
                None, sync.pull_all
            )
            if changed:
                logger.info("FileSync: files changed: %s", changed)
                await on_pull(changed)
        except asyncio.CancelledError:
            break
        except Exception as exc:
            logger.warning("FileSync: sync error: %s", exc)


def push_local(sync: FileSync, since: float = 0) -> list[str]:
    """Push locally-changed files back to MinIO. Returns list of pushed keys.

    Mirrors the openclaw worker entrypoint behavior: only scans files whose
    mtime > `since` (epoch seconds), then content-compares before uploading.
    When since=0 (first run), scans all eligible files.

    Excludes Manager-owned config files and internal dirs, matching the
    exclude list in worker-entrypoint.sh's Local->Remote sync.
    """
    # Manager-owned files that should never be pushed back (workspace root)
    _EXCLUDE_FILES = {
        "openclaw.json",
        "AGENTS.md",
        "SOUL.md",
        "mcporter-servers.json",
    }
    # Directory name components to skip anywhere in the tree
    _EXCLUDE_DIRS = {
        ".agents",
        ".openclaw",
        ".cache",
        ".npm",
        ".local",
        ".mc",
        # .copaw sub-dirs that are derived / installed at startup
        "custom_channels",
        "active_skills",
        "__pycache__",
    }
    # Derived files inside .copaw/ that are generated by bridge.py or
    # pulled from MinIO — must not be pushed back.
    _COPAW_DERIVED_FILES = {
        "config.json",
        "providers.json",
        "SOUL.md",
        "AGENTS.md",
    }

    pushed: list[str] = []
    local_dir = sync.local_dir
    if not local_dir.exists():
        return pushed

    sync._ensure_alias()

    for path in local_dir.rglob("*"):
        if not path.is_file():
            continue
        # Quick mtime check — skip files not modified since last push
        try:
            if path.stat().st_mtime <= since:
                continue
        except OSError:
            continue
        rel = path.relative_to(local_dir)
        # Skip Manager-owned config files at workspace root
        if len(rel.parts) == 1 and rel.name in _EXCLUDE_FILES:
            continue
        # Skip excluded directory trees
        if any(p in _EXCLUDE_DIRS for p in rel.parts):
            continue
        # Skip derived files inside .copaw/
        if rel.parts[0] == ".copaw" and rel.name in _COPAW_DERIVED_FILES:
            continue

        key = f"{sync._prefix}/{rel.as_posix()}"
        try:
            remote = sync._cat(key)
            local_content = path.read_text(errors="replace")
            if remote == local_content:
                continue
            dest = sync._object_path(key)
            _mc("cp", str(path), dest, check=True)
            pushed.append(str(rel))
            logger.debug("Pushed %s -> %s", rel, dest)
        except Exception as exc:
            logger.debug("push_local: failed for %s: %s", rel, exc)

    return pushed


async def push_loop(sync: FileSync, check_interval: int = 5) -> None:
    """Background task: push local changes to MinIO every `check_interval` seconds.

    Tracks last push timestamp and only triggers push_local when files with
    newer mtime are detected, similar to openclaw's find-newermt approach.
    """
    last_push_time: float = time.time()

    while True:
        await asyncio.sleep(check_interval)
        try:
            now = time.time()
            pushed = await asyncio.get_event_loop().run_in_executor(
                None, push_local, sync, last_push_time
            )
            last_push_time = now
            if pushed:
                logger.info("FileSync push: uploaded %s", pushed)
        except asyncio.CancelledError:
            break
        except Exception as exc:
            logger.warning("FileSync push error: %s", exc)
