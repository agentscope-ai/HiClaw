"""
MinIO file synchronization for CoPaw Worker via mc (MinIO Client CLI).

mc is auto-downloaded on first use based on the current OS/arch.
"""

import asyncio
import json
import os
import platform
import stat
import urllib.request
from pathlib import Path
from typing import Optional

from rich.console import Console

console = Console()

# mc download base URL
_MC_BASE_URL = "https://dl.min.io/client/mc/release"

# Map (system, machine) -> mc platform string
_PLATFORM_MAP = {
    ("linux", "x86_64"):  "linux-amd64",
    ("linux", "aarch64"): "linux-arm64",
    ("linux", "ppc64le"): "linux-ppc64le",
    ("linux", "s390x"):   "linux-s390x",
    ("darwin", "x86_64"): "darwin-amd64",
    ("darwin", "arm64"):  "darwin-arm64",
    ("windows", "x86_64"): "windows-amd64",
}


def _mc_platform() -> str:
    system = platform.system().lower()
    machine = platform.machine().lower()
    key = (system, machine)
    plat = _PLATFORM_MAP.get(key)
    if not plat:
        raise RuntimeError(f"Unsupported platform: {system}/{machine}")
    return plat


def _mc_binary_name() -> str:
    return "mc.exe" if platform.system().lower() == "windows" else "mc"


def _default_mc_path() -> Path:
    return Path.home() / ".copaw-worker" / "bin" / _mc_binary_name()


def download_mc(dest: Optional[Path] = None) -> Path:
    """
    Download mc binary for the current platform if not already present.

    Returns:
        Path to the mc binary.
    """
    dest = dest or _default_mc_path()

    if dest.exists():
        return dest

    plat = _mc_platform()
    binary = _mc_binary_name()
    url = f"{_MC_BASE_URL}/{plat}/{binary}"

    dest.parent.mkdir(parents=True, exist_ok=True)
    console.print(f"[yellow]Downloading mc from {url}...[/yellow]")

    # dl.min.io is external so proxies are fine here; use a plain opener
    urllib.request.urlretrieve(url, dest)

    # Make executable on Unix
    if platform.system().lower() != "windows":
        dest.chmod(dest.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)

    console.print(f"[green]mc downloaded to {dest}[/green]")
    return dest


class FileSync:
    """Handles bidirectional file sync between worker and MinIO via mc."""

    def __init__(
        self,
        endpoint: str,
        access_key: str,
        secret_key: str,
        bucket: str,
        worker_name: str,
        secure: bool = False,
        local_dir: Optional[Path] = None,
        mc_path: Optional[Path] = None,
    ):
        """
        Initialize file sync.

        Args:
            endpoint: MinIO endpoint URL (e.g., "http://fs-local.hiclaw.io:18080")
            access_key: MinIO access key
            secret_key: MinIO secret key
            bucket: MinIO bucket name
            worker_name: Worker name (used as prefix in bucket)
            secure: Unused, inferred from endpoint scheme
            local_dir: Local workspace dir (default: ~/.copaw-worker/<worker_name>)
            mc_path: Path to mc binary (auto-downloaded if not provided)
        """
        self.endpoint = endpoint.rstrip("/")
        self.access_key = access_key
        self.secret_key = secret_key
        self.bucket = bucket
        self.worker_name = worker_name

        # Local workspace mirrors the hiclaw-fs layout used by OpenClaw workers
        self.local_dir = local_dir or Path.home() / ".copaw-worker" / worker_name
        self.local_dir.mkdir(parents=True, exist_ok=True)

        # shared/ sits alongside the per-worker directory
        base = local_dir.parent if local_dir else Path.home() / ".copaw-worker"
        self.shared_dir = base / "shared"

        # mc alias name for this connection
        self.alias = "hiclaw"

        # mc binary (resolved lazily on first setup())
        self._mc_path: Optional[Path] = mc_path
        self._ready = False

    # ------------------------------------------------------------------
    # Setup
    # ------------------------------------------------------------------

    def setup(self) -> None:
        """Ensure mc is available and alias is configured."""
        if self._ready:
            return
        if self._mc_path is None:
            self._mc_path = download_mc()
        self._configure_alias()
        self._ready = True

    def _run_mc(self, *args: str, check: bool = True):
        """Run an mc subcommand, return CompletedProcess."""
        import subprocess
        # Bypass any system proxy for local hiclaw.io domains
        existing = os.environ.get("no_proxy", os.environ.get("NO_PROXY", ""))
        hiclaw = "hiclaw.io,localhost,127.0.0.1"
        no_proxy_val = f"{existing},{hiclaw}" if existing else hiclaw
        env = {**os.environ, "MC_NO_COLOR": "1", "no_proxy": no_proxy_val, "NO_PROXY": no_proxy_val}
        result = subprocess.run(
            [str(self._mc_path), *args],
            capture_output=True,
            text=True,
            env=env,
        )
        if check and result.returncode != 0:
            raise RuntimeError(
                f"mc {' '.join(args[:2])} failed (exit {result.returncode}):\n"
                f"{result.stderr.strip()}"
            )
        return result

    def _configure_alias(self) -> None:
        """Register mc alias for this MinIO endpoint."""
        self._run_mc(
            "alias", "set", self.alias,
            self.endpoint,
            self.access_key,
            self.secret_key,
            "--api", "S3v4",
        )

    # ------------------------------------------------------------------
    # Sync operations
    # ------------------------------------------------------------------

    def pull(self) -> list[str]:
        """
        Pull files from MinIO agents/<worker_name>/ to local workspace.

        Returns:
            List of pulled object keys.
        """
        self.setup()
        src = f"{self.alias}/{self.bucket}/agents/{self.worker_name}/"
        dst = str(self.local_dir) + "/"
        result = self._run_mc("mirror", src, dst, "--overwrite", "--json", check=False)
        return _parse_mirror_output(result.stdout)

    def push(self, exclude: Optional[list[str]] = None) -> list[str]:
        """
        Push local workspace files to MinIO agents/<worker_name>/.

        Args:
            exclude: Glob patterns to exclude (defaults to Manager-managed config files)

        Returns:
            List of pushed object keys.
        """
        self.setup()
        default_exclude = ["openclaw.json", "AGENTS.md", "SOUL.md"]
        patterns = exclude if exclude is not None else default_exclude

        args = [
            "mirror",
            str(self.local_dir) + "/",
            f"{self.alias}/{self.bucket}/agents/{self.worker_name}/",
            "--overwrite", "--json",
        ]
        for pat in patterns:
            args += ["--exclude", pat]

        result = self._run_mc(*args, check=False)
        return _parse_mirror_output(result.stdout)

    def pull_shared(self) -> list[str]:
        """Pull shared/ from MinIO to local shared directory."""
        self.setup()
        self.shared_dir.mkdir(parents=True, exist_ok=True)
        src = f"{self.alias}/{self.bucket}/shared/"
        dst = str(self.shared_dir) + "/"
        result = self._run_mc("mirror", src, dst, "--overwrite", "--json", check=False)
        return _parse_mirror_output(result.stdout)

    # ------------------------------------------------------------------
    # Config file helpers (used at startup before sync loop)
    # ------------------------------------------------------------------

    def get_config(self) -> dict:
        """Pull and return the worker's openclaw.json."""
        self.setup()
        dest = self.local_dir / "openclaw.json"
        self._run_mc(
            "cp",
            f"{self.alias}/{self.bucket}/agents/{self.worker_name}/openclaw.json",
            str(dest),
        )
        with open(dest) as f:
            return json.load(f)

    def get_soul(self) -> str:
        """Pull and return SOUL.md content."""
        self.setup()
        dest = self.local_dir / "SOUL.md"
        try:
            self._run_mc(
                "cp",
                f"{self.alias}/{self.bucket}/agents/{self.worker_name}/SOUL.md",
                str(dest),
            )
            return dest.read_text()
        except RuntimeError:
            console.print("[yellow]SOUL.md not found in MinIO[/yellow]")
            return ""

    def get_agents_md(self) -> str:
        """Pull and return AGENTS.md content."""
        self.setup()
        dest = self.local_dir / "AGENTS.md"
        try:
            self._run_mc(
                "cp",
                f"{self.alias}/{self.bucket}/agents/{self.worker_name}/AGENTS.md",
                str(dest),
            )
            return dest.read_text()
        except RuntimeError:
            console.print("[yellow]AGENTS.md not found in MinIO[/yellow]")
            return ""


# ------------------------------------------------------------------
# Background sync loop
# ------------------------------------------------------------------

async def sync_loop(
    sync: FileSync,
    interval: int = 300,
    on_pull: Optional[callable] = None,
) -> None:
    """
    Periodically pull from MinIO and push local changes back.

    Args:
        sync: FileSync instance
        interval: Sync interval in seconds
        on_pull: Async callback(pulled_files: list[str]) invoked after a pull
    """
    loop = asyncio.get_event_loop()
    while True:
        await asyncio.sleep(interval)
        try:
            pulled = await loop.run_in_executor(None, sync.pull)
            if pulled and on_pull:
                await on_pull(pulled)

            await loop.run_in_executor(None, sync.push)
        except Exception as e:
            console.print(f"[yellow]Sync error: {e}[/yellow]")


# ------------------------------------------------------------------
# Helpers
# ------------------------------------------------------------------

def _parse_mirror_output(stdout: str) -> list[str]:
    """Parse mc mirror/cp --json output, return list of transferred object keys."""
    transferred = []
    for line in stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            obj = json.loads(line)
            if obj.get("status") == "success" and "key" in obj:
                transferred.append(obj["key"])
        except json.JSONDecodeError:
            pass
    return transferred
