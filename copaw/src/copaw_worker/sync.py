"""MinIO file sync for copaw-worker.

All MinIO operations use the `mc` CLI (MinIO Client).

Ownership split (aligned with OpenClaw worker-entrypoint.sh):

  Manager-managed (Worker read-only, pull only, never overwrite):
    openclaw.json, mcporter-servers.json, skills/, shared/

  Worker-managed (Worker read-write, push to MinIO, never pull-overwrite):
    AGENTS.md, SOUL.md, .copaw/sessions/, memory/, etc.

  Local -> Remote (push_loop): change-triggered push of Worker-managed content.
  Remote -> Local (sync_loop pull_all): allowlist only, Manager-managed paths.
  Prevents .copaw/sessions conflict: sessions backed up but never pulled back.
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


class McError(RuntimeError):
    """Error raised when mc command fails."""

    def __init__(self, message: str, command: str, stdout: str, stderr: str) -> None:
        super().__init__(message)
        self.command = command
        self.stdout = stdout
        self.stderr = stderr


def _mc(*args: str, check: bool = True) -> subprocess.CompletedProcess:
    """Run an mc command and return the result."""
    mc_bin = shutil.which("mc")
    if not mc_bin:
        raise RuntimeError(
            "mc binary not found on PATH. Please install mc first:\n"
            "  • Linux/macOS: curl https://dl.min.io/client/mc/release/linux-amd64/mc -o /usr/local/bin/mc && chmod +x /usr/local/bin/mc\n"
            "  • Or with package manager: apt install mc (Debian/Ubuntu), brew install minio/stable/mc (macOS)"
        )
    cmd = [mc_bin, *args]
    cmd_str = " ".join(cmd)
    logger.debug("mc cmd: %s", cmd_str)
    result = subprocess.run(cmd, capture_output=True, text=True, check=check)

    if result.stderr and "mc <" in result.stderr.lower():
        # mc is showing usage/help - likely invalid arguments
        logger.error("mc command failed (invalid arguments): %s\nstderr: %s", cmd_str, result.stderr)
    elif result.returncode != 0:
        logger.debug("mc cmd (exit %d): %s", result.returncode, cmd_str)

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

        # Validate that the endpoint looks like a MinIO/S3 server
        # Common mistake: using Higress Console port (18080) instead of MinIO port (9000)
        if ":18080" in url or ":18001" in url:
            logger.warning(
                "WARNING: The MinIO endpoint appears to be using a Higress Console port (%s).\n"
                "MinIO typically runs on port 9000 (or 9001 for HTTPS).\n"
                "If you're trying to connect to HiClaw's MinIO, check your --fs parameter.",
                url
            )

        try:
            _mc("alias", "set", _MC_ALIAS, url, self.access_key, self.secret_key)
        except subprocess.CalledProcessError as exc:
            # Provide helpful error message for common issues
            error_msg = (
                f"Failed to configure MinIO connection.\n"
                f"  Endpoint: {url}\n"
                f"  Access Key: {self.access_key[:10]}{'*' if len(self.access_key) > 10 else ''}\n"
                f"  Command: mc alias set {_MC_ALIAS} <url> <access-key> <secret-key>\n"
            )

            if "connection" in (exc.stderr or "").lower() or "refused" in (exc.stderr or "").lower():
                error_msg += (
                    f"\n\nConnection refused. Possible causes:\n"
                    f"  1. MinIO server is not running\n"
                    f"  2. Wrong host/port - MinIO default is port 9000, not {url.split(':')[2] if ':' in url else '<unknown>'}\n"
                    f"  3. Firewall blocking the connection\n"
                    f"  4. Using wrong endpoint URL (e.g., Higress Console instead of MinIO)"
                )
            elif "credentials" in (exc.stderr or "").lower() or "access" in (exc.stderr or "").lower() or "401" in (exc.stderr or "") or "403" in (exc.stderr or ""):
                error_msg += (
                    f"\n\nAuthentication failed. Check:\n"
                    f"  1. Access key (--fs-key) is correct\n"
                    f"  2. Secret key (--fs-secret) is correct\n"
                    f"  3. MinIO user has necessary permissions"
                )

            error_msg += f"\n\nmc stderr:\n{exc.stderr or '(empty)'}"
            raise RuntimeError(error_msg) from exc

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
        self._ensure_alias()
        text = self._cat(f"{self._prefix}/openclaw.json")
        if not text:
            raise RuntimeError(
                f"openclaw.json not found in MinIO for worker '{self.worker_name}'.\n"
                f"  Expected path: {_MC_ALIAS}/{self.bucket}/{self._prefix}/openclaw.json\n"
                f"  Please ensure the Manager has created this Worker's configuration first.\n"
                f"  You can check if the file exists using: mc ls {_MC_ALIAS}/{self.bucket}/agents/"
            )
        logger.info("openclaw.json raw content (%d chars): %r", len(text), text[:500])
        try:
            return json.loads(text)
        except json.JSONDecodeError as exc:
            raise RuntimeError(
                f"Failed to parse openclaw.json for worker '{self.worker_name}'.\n"
                f"  JSON decode error: {exc}\n"
                f"  Content preview: {text[:200]}..."
            ) from exc

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
        """Pull Manager-managed files only (allowlist). Returns list of filenames that changed.

        Does NOT pull AGENTS.md, SOUL.md (Worker-managed, sync up but never overwrite).
        """
        changed: list[str] = []
        # Manager-managed files (allowlist)
        files = {
            "openclaw.json": f"{self._prefix}/openclaw.json",
            "mcporter-servers.json": f"{self._prefix}/mcporter-servers.json",
        }
        for name, key in files.items():
            content = self._cat(key)
            if content is None:
                continue
            local = self.local_dir / name
            existing = local.read_text() if local.exists() else None
            if content != existing:
                local.parent.mkdir(parents=True, exist_ok=True)
                local.write_text(content)
                changed.append(name)

        # Manager-managed: skills/
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

    Excludes Manager-managed files only. AGENTS.md, SOUL.md, .copaw/sessions/
    are Worker-managed and are pushed (including session backup).
    """
    # Manager-managed files that should never be pushed back (workspace root)
    _EXCLUDE_FILES = {
        "openclaw.json",
        "mcporter-servers.json",
    }
    # Directory name components to skip anywhere in the tree
    _EXCLUDE_DIRS = {
        ".agents",
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
