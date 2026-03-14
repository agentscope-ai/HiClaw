"""
Worker main entry point.

Bootstrap flow:
1. Pull openclaw.json + SOUL.md + AGENTS.md from MinIO
2. Bridge openclaw.json -> CoPaw config.json + providers.json
3. Install MatrixChannel into CoPaw's custom_channels dir
4. Start CoPaw AgentRunner + ChannelManager (Matrix channel)
"""
from __future__ import annotations

import asyncio
import logging
import os
import platform
import shutil
import stat
from pathlib import Path
from typing import Optional

from rich.console import Console
from rich.panel import Panel

from copaw_worker.config import WorkerConfig
from copaw_worker.sync import FileSync, sync_loop, push_loop
from copaw_worker.bridge import bridge_openclaw_to_copaw

console = Console()
logger = logging.getLogger(__name__)


class Worker:
    def __init__(self, config: WorkerConfig) -> None:
        self.config = config
        self.worker_name = config.worker_name
        self.sync: Optional[FileSync] = None
        self._copaw_working_dir: Optional[Path] = None
        self._runner = None
        self._channel_manager = None

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def run(self) -> None:
        if not await self.start():
            return
        try:
            await self._run_copaw()
        except asyncio.CancelledError:
            pass
        finally:
            await self.stop()

    async def stop(self) -> None:
        console.print("[yellow]Stopping worker...[/yellow]")
        if self._channel_manager is not None:
            try:
                await self._channel_manager.stop_all()
            except Exception:
                pass
        if self._runner is not None:
            try:
                await self._runner.stop()
            except Exception:
                pass
        console.print("[green]Worker stopped.[/green]")

    # ------------------------------------------------------------------
    # Startup
    # ------------------------------------------------------------------

    async def start(self) -> bool:
        console.print(
            Panel.fit(
                f"[bold green]CoPaw Worker[/bold green]\n"
                f"Worker: [cyan]{self.worker_name}[/cyan]",
                title="Starting",
            )
        )

        # 1. Ensure mc (MinIO Client) is available
        self._ensure_mc()

        # 2. Init file sync
        self.sync = FileSync(
            endpoint=self.config.minio_endpoint,
            access_key=self.config.minio_access_key,
            secret_key=self.config.minio_secret_key,
            bucket=self.config.minio_bucket,
            worker_name=self.worker_name,
            secure=self.config.minio_secure,
            local_dir=self.config.install_dir / self.worker_name,
        )

        # 2. Pull config from MinIO
        console.print("[yellow]Pulling configuration from MinIO...[/yellow]")
        try:
            openclaw_cfg = self.sync.get_config()
            soul_content = self.sync.get_soul()
            agents_content = self.sync.get_agents_md()
        except Exception as exc:
            console.print(f"[red]Failed to pull config: {exc}[/red]")
            return False

        # 3. Set up CoPaw working directory
        self._copaw_working_dir = self.config.install_dir / self.worker_name / ".copaw"
        self._copaw_working_dir.mkdir(parents=True, exist_ok=True)

        # Write SOUL.md / AGENTS.md into CoPaw working dir
        if soul_content:
            (self._copaw_working_dir / "SOUL.md").write_text(soul_content)
        if agents_content:
            (self._copaw_working_dir / "AGENTS.md").write_text(agents_content)

        # 4. Bridge openclaw.json -> CoPaw config.json + providers.json
        console.print("[yellow]Bridging configuration to CoPaw...[/yellow]")
        try:
            bridge_openclaw_to_copaw(openclaw_cfg, self._copaw_working_dir)
        except Exception as exc:
            console.print(f"[red]Config bridge failed: {exc}[/red]")
            return False

        # 5. Install MatrixChannel into CoPaw's custom_channels dir
        self._install_matrix_channel()

        # 6. Sync skills from MinIO into CoPaw's active_skills dir
        self._sync_skills()

        # 7. Start background MinIO sync
        asyncio.create_task(
            sync_loop(
                self.sync,
                interval=self.config.sync_interval,
                on_pull=self._on_files_pulled,
            )
        )
        # Local -> Remote: change-triggered push (every 5s, mirrors openclaw worker behavior)
        asyncio.create_task(push_loop(self.sync, check_interval=5))

        console.print("[bold green]Worker initialized.[/bold green]")
        if self.config.console_port:
            console.print(
                f"[dim]Note: web console enabled on port {self.config.console_port} "
                f"(~500MB extra RAM). Remove --console-port to save memory.[/dim]"
            )
        else:
            console.print(
                "[dim]Tip: add --console-port 8088 to enable the web console "
                "(costs ~500MB extra RAM).[/dim]"
            )
        return True

    # ------------------------------------------------------------------
    # CoPaw runner
    # ------------------------------------------------------------------

    async def _run_copaw(self) -> None:
        """Start CoPaw. If console_port is set, run the full FastAPI app via
        uvicorn (gives access to the web console). Otherwise start the runner
        and channel manager directly (lightweight, no HTTP server)."""
        if self.config.console_port:
            await self._run_copaw_with_console(self.config.console_port)
        else:
            await self._run_copaw_headless()

    async def _run_copaw_with_console(self, port: int) -> None:
        """Run CoPaw's full FastAPI app (runner + channels + web console)."""
        import uvicorn
        from copaw.app.channels.registry import clear_builtin_channel_cache

        clear_builtin_channel_cache()

        uv_config = uvicorn.Config(
            "copaw.app._app:app",
            host="0.0.0.0",
            port=port,
            log_level="info",
        )
        server = uvicorn.Server(uv_config)
        console.print(
            f"[bold green]CoPaw console available at "
            f"http://127.0.0.1:{port}/[/bold green]"
        )
        try:
            await server.serve()
        except asyncio.CancelledError:
            server.should_exit = True

    async def _run_copaw_headless(self) -> None:
        """Start CoPaw's AgentRunner + ChannelManager (no HTTP server)."""
        from copaw.app.runner.runner import AgentRunner
        from copaw.config.utils import load_config
        from copaw.app.channels.manager import ChannelManager
        from copaw.app.channels.utils import make_process_from_runner
        from copaw.app.channels.registry import clear_builtin_channel_cache

        # Force registry reload so newly installed matrix_channel.py is picked up
        clear_builtin_channel_cache()

        self._runner = AgentRunner()
        await self._runner.start()

        # load_config reads COPAW_WORKING_DIR/config.json (set by bridge.py)
        config = load_config()
        self._channel_manager = ChannelManager.from_config(
            process=make_process_from_runner(self._runner),
            config=config,
            on_last_dispatch=None,
        )
        await self._channel_manager.start_all()

        console.print("[bold green]CoPaw channels started. Worker is running.[/bold green]")

        try:
            while True:
                await asyncio.sleep(60)
        except asyncio.CancelledError:
            pass
        finally:
            await self._channel_manager.stop_all()
            await self._runner.stop()
            # Clear refs so stop() doesn't double-call
            self._channel_manager = None
            self._runner = None

    # ------------------------------------------------------------------
    # mc (MinIO Client) auto-install
    # ------------------------------------------------------------------

    def _ensure_mc(self) -> None:
        """Ensure mc (MinIO Client) binary is available on PATH.

        If not found, downloads the latest release from dl.min.io and installs
        it to ~/.local/bin/mc (created if needed, added to PATH for this process).
        """
        if shutil.which("mc"):
            logger.debug("mc already available")
            return

        system = platform.system().lower()   # linux / darwin
        machine = platform.machine().lower() # x86_64 / aarch64 / arm64

        arch_map = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}
        arch = arch_map.get(machine, machine)

        if system == "windows":
            url = "https://dl.min.io/client/mc/release/windows-amd64/mc.exe"
            install_dir = Path.home() / ".local" / "bin"
            install_dir.mkdir(parents=True, exist_ok=True)
            dest = install_dir / "mc.exe"
        elif system in ("linux", "darwin"):
            url = f"https://dl.min.io/client/mc/release/{system}-{arch}/mc"
            install_dir = Path.home() / ".local" / "bin"
            install_dir.mkdir(parents=True, exist_ok=True)
            dest = install_dir / "mc"
        else:
            console.print(f"[yellow]mc auto-install not supported on {system}, please install mc manually[/yellow]")
            return

        console.print(f"[yellow]mc not found, downloading from {url}...[/yellow]")
        try:
            import httpx
            with httpx.stream("GET", url, follow_redirects=True, timeout=60) as resp:
                resp.raise_for_status()
                with open(dest, "wb") as f:
                    for chunk in resp.iter_bytes(chunk_size=65536):
                        f.write(chunk)
            if system != "windows":
                dest.chmod(dest.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
            os.environ["PATH"] = str(install_dir) + os.pathsep + os.environ.get("PATH", "")
            console.print(f"[green]mc installed to {dest}[/green]")
        except Exception as exc:
            console.print(f"[yellow]mc auto-install failed: {exc}. Please install mc manually.[/yellow]")

    # ------------------------------------------------------------------
    # Skills sync
    # ------------------------------------------------------------------

    def _sync_skills(self) -> None:
        """Pull skills from MinIO and install into CoPaw's active_skills dir.

        First seeds all CoPaw built-in skills (pdf, xlsx, docx, etc.) as a base
        layer, then overlays skills pushed from MinIO by the Manager (which take
        precedence and can override built-ins).
        """
        active_skills_dir = self._copaw_working_dir / "active_skills"
        active_skills_dir.mkdir(parents=True, exist_ok=True)

        # 1. Seed CoPaw built-in skills as base layer.
        # bridge.py has already patched copaw.constant.ACTIVE_SKILLS_DIR to point
        # here, so sync_skills_to_working_dir() writes to the correct directory.
        try:
            from copaw.agents.skills_manager import sync_skills_to_working_dir
            synced, skipped = sync_skills_to_working_dir(skill_names=None, force=False)
            logger.info(
                "Seeded CoPaw built-in skills: %d installed, %d already existed",
                synced, skipped,
            )
        except Exception as exc:
            logger.warning("Failed to seed CoPaw built-in skills: %s", exc)

        # 2. Overlay with Manager-pushed skills from MinIO (higher priority).
        skill_names = self.sync.list_skills()
        if not skill_names:
            logger.info("No extra skills in MinIO for worker %s", self.worker_name)
            return

        for skill_name in skill_names:
            skill_md = self.sync.get_skill_md(skill_name)
            if not skill_md:
                continue
            skill_dir = active_skills_dir / skill_name
            skill_dir.mkdir(parents=True, exist_ok=True)
            (skill_dir / "SKILL.md").write_text(skill_md)
            logger.info("Installed MinIO skill: %s", skill_name)

        console.print(f"[green]Skills installed: {', '.join(skill_names)}[/green]")

    # ------------------------------------------------------------------
    # MatrixChannel installation
    # ------------------------------------------------------------------

    def _install_matrix_channel(self) -> None:
        """Copy matrix_channel.py into COPAW_WORKING_DIR/custom_channels/.

        CoPaw's CUSTOM_CHANNELS_DIR = WORKING_DIR / "custom_channels", and
        WORKING_DIR is read from COPAW_WORKING_DIR env var at import time.
        We set COPAW_WORKING_DIR in bridge.py before this runs, so the
        directory is already correct.
        """
        custom_channels_dir = self._copaw_working_dir / "custom_channels"
        custom_channels_dir.mkdir(parents=True, exist_ok=True)
        src = Path(__file__).parent / "matrix_channel.py"
        dst = custom_channels_dir / "matrix_channel.py"
        shutil.copy2(src, dst)
        logger.debug("MatrixChannel installed to %s", dst)

    # ------------------------------------------------------------------
    # File sync callback
    # ------------------------------------------------------------------

    async def _on_files_pulled(self, pulled_files: list[str]) -> None:
        """Re-bridge config when openclaw.json / SOUL.md / AGENTS.md change."""
        # Re-sync skills if any skill file changed
        if any(f.startswith("skills/") for f in pulled_files):
            self._sync_skills()

        needs_rebridge = any(
            name in f
            for f in pulled_files
            for name in ("openclaw.json", "SOUL.md", "AGENTS.md")
        )
        if not needs_rebridge:
            return

        console.print("[yellow]Config changed, re-bridging...[/yellow]")
        try:
            openclaw_cfg = self.sync.get_config()
            soul = self.sync.get_soul()
            agents = self.sync.get_agents_md()

            if soul:
                (self._copaw_working_dir / "SOUL.md").write_text(soul)
            if agents:
                (self._copaw_working_dir / "AGENTS.md").write_text(agents)

            bridge_openclaw_to_copaw(openclaw_cfg, self._copaw_working_dir)
            console.print("[green]Config re-bridged.[/green]")
        except Exception as exc:
            console.print(f"[red]Re-bridge failed: {exc}[/red]")
