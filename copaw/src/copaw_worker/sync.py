"""MinIO file sync for copaw-worker.

Pulls openclaw.json, SOUL.md, AGENTS.md, and skills/ from MinIO using the
`mc` CLI (MinIO Client), which is pre-installed in the hiclaw environment.
Runs a background loop that re-pulls on interval and calls on_pull callback.
"""
from __future__ import annotations

import asyncio
import json
import logging
import shutil
import subprocess
from pathlib import Path
from typing import Any, Callable, Coroutine, Optional

logger = logging.getLogger(__name__)

_MC_ALIAS = "hiclaw-worker"


class FileSync:
    """MinIO file sync using the mc CLI."""

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
        self.local_dir = local_dir or Path.home() / ".copaw-worker" / worker_name
        self.local_dir.mkdir(parents=True, exist_ok=True)
        self._prefix = f"agents/{worker_name}"
        self._mc = shutil.which("mc") or "mc"
        self._setup_alias()

    def _setup_alias(self) -> None:
        try:
            subprocess.run(
                [self._mc, "alias", "set", _MC_ALIAS,
                 self.endpoint, self.access_key, self.secret_key,
                 "--api", "s3v4", "--path", "on"],
                capture_output=True, check=True, timeout=10,
            )
        except Exception as exc:
            logger.warning("FileSync: mc alias set failed: %s", exc)

    def _mc_cat(self, key: str) -> Optional[str]:
        path = f"{_MC_ALIAS}/{self.bucket}/{key}"
        try:
            result = subprocess.run([self._mc, "cat", path], capture_output=True, timeout=15)
            if result.returncode == 0:
                return result.stdout.decode("utf-8")
            logger.debug("FileSync: mc cat %s failed: %s", path, result.stderr.decode())
            return None
        except Exception as exc:
            logger.debug("FileSync: mc cat %s exception: %s", path, exc)
            return None

    def _mc_ls_dirs(self, prefix: str) -> list[str]:
        path = f"{_MC_ALIAS}/{self.bucket}/{prefix}"
        try:
            result = subprocess.run([self._mc, "ls", path], capture_output=True, timeout=10)
            if result.returncode != 0:
                return []
            dirs = []
            for line in result.stdout.decode().splitlines():
                parts = line.strip().split()
                if parts and parts[-1].endswith("/"):
                    dirs.append(parts[-1].rstrip("/"))
            return dirs
        except Exception as exc:
            logger.debug("FileSync: mc ls %s exception: %s", path, exc)
            return []

    def get_config(self) -> dict[str, Any]:
        text = self._mc_cat(f"{self._prefix}/openclaw.json")
        if not text:
            raise RuntimeError(f"openclaw.json not found in MinIO for worker {self.worker_name}")
        return json.loads(text)

    def get_soul(self) -> Optional[str]:
        return self._mc_cat(f"{self._prefix}/SOUL.md")

    def get_agents_md(self) -> Optional[str]:
        return self._mc_cat(f"{self._prefix}/AGENTS.md")

    def list_skills(self) -> list[str]:
        return self._mc_ls_dirs(f"{self._prefix}/skills/")

    def get_skill_md(self, skill_name: str) -> Optional[str]:
        return self._mc_cat(f"{self._prefix}/skills/{skill_name}/SKILL.md")

    def pull_all(self) -> list[str]:
        changed: list[str] = []
        for name, key in {
            "openclaw.json": f"{self._prefix}/openclaw.json",
            "SOUL.md": f"{self._prefix}/SOUL.md",
            "AGENTS.md": f"{self._prefix}/AGENTS.md",
        }.items():
            content = self._mc_cat(key)
            if content is None:
                continue
            local = self.local_dir / name
            if content != (local.read_text() if local.exists() else None):
                local.write_text(content)
                changed.append(name)

        for skill_name in self.list_skills():
            skill_md = self.get_skill_md(skill_name)
            if skill_md is None:
                continue
            skill_dir = self.local_dir / "skills" / skill_name
            skill_dir.mkdir(parents=True, exist_ok=True)
            local = skill_dir / "SKILL.md"
            if skill_md != (local.read_text() if local.exists() else None):
                local.write_text(skill_md)
                changed.append(f"skills/{skill_name}/SKILL.md")

        return changed


async def sync_loop(
    sync: FileSync,
    interval: int,
    on_pull: Callable[[list[str]], Coroutine],
) -> None:
    while True:
        await asyncio.sleep(interval)
        try:
            changed = await asyncio.get_event_loop().run_in_executor(None, sync.pull_all)
            if changed:
                logger.info("FileSync: files changed: %s", changed)
                await on_pull(changed)
        except asyncio.CancelledError:
            break
        except Exception as exc:
            logger.warning("FileSync: sync error: %s", exc)
