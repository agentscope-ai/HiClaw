"""
Worker main loop - ties together Matrix, Sync, and Agent.

Implements message response rules:
- DM vs Group detection
- allowlist checking (dm.allowFrom / groupAllowFrom)
- requireMention for group rooms
- Message buffering (merge on mention)
- Per-room context isolation
"""

import asyncio
import json
import re
import signal
from pathlib import Path
from typing import Any, Optional

from rich.console import Console
from rich.panel import Panel

from copaw_worker.agent import Agent
from copaw_worker.config import WorkerConfig
from copaw_worker.matrix import MatrixClient, MessageContext, RoomState, RoomType
from copaw_worker.sync import FileSync, sync_loop

console = Console()


class MessageRules:
    """Parsed message response rules from openclaw.json channels.matrix config."""

    def __init__(self, raw_config: dict):
        self.raw = raw_config
        matrix = raw_config.get("channels", {}).get("matrix", {})

        # DM settings
        dm_config = matrix.get("dm", {})
        self.dm_policy = dm_config.get("policy", "allowlist")
        self.dm_allow_from: list[str] = dm_config.get("allowFrom", [])

        # Group settings
        self.group_policy = matrix.get("groupPolicy", "allowlist")
        self.group_allow_from: list[str] = matrix.get("groupAllowFrom", [])

        # Per-group/room settings
        self.groups: dict[str, dict] = matrix.get("groups", matrix.get("rooms", {}))

        # Normalize user IDs
        self.dm_allow_from = [self._normalize_user_id(u) for u in self.dm_allow_from]
        self.group_allow_from = [self._normalize_user_id(u) for u in self.group_allow_from]

    @staticmethod
    def _normalize_user_id(user_id: str) -> str:
        """Normalize Matrix user ID for comparison."""
        uid = user_id.strip().lower()
        if not uid.startswith("@"):
            uid = "@" + uid
        return uid

    def is_dm_allowed(self, sender_id: str) -> bool:
        """Check if sender is allowed to send DM."""
        if self.dm_policy == "open":
            return True
        if self.dm_policy == "disabled":
            return False
        # allowlist or pairing
        normalized = self._normalize_user_id(sender_id)
        return normalized in self.dm_allow_from

    def is_group_allowed(self, sender_id: str, room_id: str) -> bool:
        """Check if sender is allowed to send in group."""
        if self.group_policy == "open":
            return True
        if self.group_policy == "disabled":
            return False
        # allowlist
        normalized = self._normalize_user_id(sender_id)
        return normalized in self.group_allow_from

    def get_room_config(self, room_id: str) -> Optional[dict]:
        """Get per-room configuration."""
        # Try exact match first
        if room_id in self.groups:
            return self.groups[room_id]
        # Try alias match (would need to resolve aliases)
        for key, config in self.groups.items():
            if key.startswith("#") or key.startswith("!"):
                # Could be alias or room ID
                pass
        return None

    def require_mention(self, room_type: RoomType, room_id: str) -> bool:
        """Check if mention is required for this room."""
        if room_type == RoomType.DM:
            return False  # DM never requires mention

        # Check per-room config
        room_config = self.get_room_config(room_id)
        if room_config:
            # autoReply=true means no mention required
            if room_config.get("autoReply") is True:
                return False
            # explicit requireMention setting
            if "requireMention" in room_config:
                return room_config["requireMention"]

        # Default: require mention for group rooms
        return True


class Worker:
    """
    Main worker class that coordinates Matrix, Sync, and Agent.
    """

    def __init__(self, config: "WorkerConfig"):
        """
        Initialize worker.

        Args:
            config: Worker configuration
        """
        self.config = config
        self.worker_name = config.worker_name

        # Components
        self.sync: Optional[FileSync] = None
        self.matrix: Optional[MatrixClient] = None
        self.agent: Optional[Agent] = None

        # Parsed openclaw.json message rules
        self.message_rules: Optional[MessageRules] = None

        # State
        self.running = False

        # Per-room agents (context isolation)
        self.room_agents: dict[str, Agent] = {}

    def _is_running_in_container(self) -> bool:
        """Check if running inside a Docker container."""
        try:
            return Path("/.dockerenv").exists()
        except Exception:
            return False

    async def start(self) -> bool:
        """
        Start the worker.

        Returns:
            True if startup successful
        """
        console.print(
            Panel.fit(
                f"[bold green]CoPaw Worker[/bold green]\n"
                f"Worker: [cyan]{self.worker_name}[/cyan]",
                title="Starting",
            )
        )

        # 1. Initialize file sync
        console.print("[yellow]Initializing file sync...[/yellow]")
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
            openclaw_config = self.sync.get_config()
            soul_content = self.sync.get_soul()
            agents_content = self.sync.get_agents_md()
        except Exception as e:
            console.print(f"[red]Failed to pull config: {e}[/red]")
            return False

        # Parse openclaw.json for message rules
        self.message_rules = MessageRules(openclaw_config)

        # Extract Matrix config from openclaw.json
        matrix_config = openclaw_config.get("channels", {}).get("matrix", {})
        matrix_server = matrix_config.get("homeserver", self.config.matrix_server)
        
        # Handle port mapping: container internal (8080) -> external (18080)
        # This is needed when running outside the manager container
        if matrix_server and ":8080" in matrix_server and not self._is_running_in_container():
            matrix_server = matrix_server.replace(":8080", ":18080")
            console.print(f"[dim]Adjusted Matrix server to external port: {matrix_server}[/dim]")
        
        matrix_user = self.config.matrix_user or self.worker_name
        
        # Prefer access token from config, fallback to password
        matrix_access_token = matrix_config.get("accessToken", "")
        matrix_password = self.config.matrix_password if not matrix_access_token else ""

        # Extract AI Gateway config from openclaw.json
        ai_config = openclaw_config.get("models", {}).get("providers", {})
        gateway_token = ""
        gateway_url = self.config.ai_gateway
        
        # Try to get gateway token from providers config
        for provider_name, provider_config in ai_config.items():
            if "apiKey" in provider_config:
                gateway_token = provider_config["apiKey"]
                if "baseUrl" in provider_config:
                    gateway_url = provider_config["baseUrl"].replace("/v1", "")
                    # Handle port mapping: container internal (8080) -> external (18080)
                    if gateway_url and ":8080" in gateway_url and not self._is_running_in_container():
                        gateway_url = gateway_url.replace(":8080", ":18080")
                        console.print(f"[dim]Adjusted AI Gateway to external port: {gateway_url}[/dim]")
                break
        
        # Fallback to env config
        if not gateway_token:
            gateway_token = self.config.gateway_token
        if not gateway_url:
            gateway_url = self.config.ai_gateway

        # Get model name
        model = ai_config.get("default", self.config.model) if isinstance(ai_config.get("default"), str) else self.config.model

        # Save for room agents
        self._gateway_url = gateway_url
        self._gateway_token = gateway_token
        self._model = model

        # Build system prompt
        system_prompt = self._build_system_prompt(soul_content, agents_content)

        # 3. Initialize Matrix client
        console.print("[yellow]Connecting to Matrix...[/yellow]")
        self.matrix = MatrixClient(
            homeserver=matrix_server,
            username=matrix_user,
            password=matrix_password,
            access_token=matrix_access_token,
            device_name=f"copaw-{self.worker_name}",
        )

        if not await self.matrix.login():
            console.print("[red]Failed to login to Matrix[/red]")
            return False

        # Register message handler
        self.matrix.on_message(self._handle_matrix_message)

        # 4. Initialize default agent (for DMs without room agent)
        console.print("[yellow]Initializing Agent...[/yellow]")
        self.agent = Agent(
            gateway_url=gateway_url,
            gateway_token=gateway_token,
            model=model,
            system_prompt=system_prompt,
        )

        # 5. Start background sync
        console.print("[yellow]Starting background sync...[/yellow]")
        asyncio.create_task(
            sync_loop(
                self.sync,
                interval=self.config.sync_interval,
                on_pull=self._on_files_pulled,
            )
        )

        self.running = True
        console.print("[bold green]Worker started successfully![/bold green]")

        return True

    async def stop(self) -> None:
        """Stop the worker."""
        console.print("[yellow]Stopping worker...[/yellow]")
        self.running = False

        if self.matrix:
            await self.matrix.logout()

        if self.agent:
            await self.agent.close()

        # Close all room agents
        for agent in self.room_agents.values():
            await agent.close()

        console.print("[green]Worker stopped.[/green]")

    async def run(self) -> None:
        """Run the main event loop."""
        if not await self.start():
            return

        try:
            # Run Matrix sync forever
            await self.matrix.sync_forever()
        except asyncio.CancelledError:
            pass
        finally:
            await self.stop()

    def _build_system_prompt(self, soul: str, agents: str) -> str:
        """Build system prompt from SOUL.md and AGENTS.md."""
        parts = []

        if soul:
            parts.append(f"# Your Identity\n\n{soul}")

        if agents:
            parts.append(f"# Guidelines\n\n{agents}")

        # Add worker context
        parts.append(
            f"\n# Worker Context\n\n"
            f"You are a worker agent named **{self.worker_name}**.\n"
            f"You communicate via Matrix chat rooms.\n"
            f"When someone @mentions you, respond helpfully.\n"
        )

        return "\n\n---\n\n".join(parts)

    def _get_or_create_room_agent(self, room_id: str, system_prompt: str) -> Agent:
        """Get or create an agent for a specific room (context isolation)."""
        if room_id not in self.room_agents:
            self.room_agents[room_id] = Agent(
                gateway_url=self._gateway_url,
                gateway_token=self._gateway_token,
                model=self._model,
                system_prompt=system_prompt,
            )
        return self.room_agents[room_id]

    async def _handle_matrix_message(
        self,
        ctx: MessageContext,
        room_state: RoomState,
    ) -> None:
        """
        Handle incoming Matrix message with response rules.

        Rules:
        1. DM: check dm.allowFrom, no mention required
        2. Group: check groupAllowFrom, requireMention by default
        3. Buffer non-mention messages, merge on mention
        4. Per-room context isolation
        """
        sender_id = ctx.sender_id
        room_id = ctx.room_id

        console.print(
            f"[dim]Message from {sender_id} in {room_id} "
            f"(type={ctx.room_type.value}, mention={ctx.was_mentioned})[/dim]"
        )

        # Step 1: Check allowlist
        if ctx.room_type == RoomType.DM:
            if not self.message_rules.is_dm_allowed(sender_id):
                console.print(f"[yellow]DM blocked: {sender_id} not in allowlist[/yellow]")
                return
        else:  # Group
            if not self.message_rules.is_group_allowed(sender_id, room_id):
                console.print(f"[yellow]Group message blocked: {sender_id} not in allowlist[/yellow]")
                return

        # Step 2: Check requireMention
        require_mention = self.message_rules.require_mention(ctx.room_type, room_id)

        if ctx.room_type == RoomType.GROUP and require_mention:
            if not ctx.was_mentioned:
                # Buffer message for later merge
                room_state.pending_messages.append(ctx)
                console.print(
                    f"[dim]Buffered message (no mention, pending={len(room_state.pending_messages)})[/dim]"
                )
                return
            else:
                # Mention received - merge pending messages
                if room_state.pending_messages:
                    console.print(
                        f"[cyan]Mention received, merging {len(room_state.pending_messages)} buffered messages[/cyan]"
                    )
                    # Build combined context from pending messages
                    combined_content = self._merge_pending_messages(
                        room_state.pending_messages + [ctx]
                    )
                    ctx = MessageContext(
                        room_id=ctx.room_id,
                        room_type=ctx.room_type,
                        sender_id=ctx.sender_id,
                        sender_name=ctx.sender_name,
                        content=combined_content,
                        event_id=ctx.event_id,
                        timestamp=ctx.timestamp,
                        was_mentioned=True,
                        has_explicit_mention=ctx.has_explicit_mention,
                        room_name=ctx.room_name,
                        room_alias=ctx.room_alias,
                    )
                    room_state.pending_messages.clear()

        # Step 3: Process message with agent (per-room context isolation)
        try:
            # Get room-specific agent
            room_agent = self._get_or_create_room_agent(
                room_id,
                self.agent.system_prompt or ""
            )

            # Add context to history
            room_state.history.append({
                "role": "user",
                "sender": ctx.sender_name,
                "content": ctx.content,
                "timestamp": ctx.timestamp,
            })

            # Keep only last 20 messages
            if len(room_state.history) > 20:
                room_state.history = room_state.history[-20:]

            # Get agent response
            response = await room_agent.chat(ctx.content)

            # Send response
            mentions = [sender_id] if sender_id else None
            await self.matrix.send_text(room_id, response, mentions=mentions)

            # Add to history
            room_state.history.append({
                "role": "assistant",
                "content": response,
            })

            console.print(f"[green]Response sent to {room_id}[/green]")

        except Exception as e:
            console.print(f"[red]Error processing message: {e}[/red]")
            await self.matrix.send_text(
                room_id,
                f"Sorry, I encountered an error: {e}",
                mentions=[sender_id] if sender_id else None,
            )

    def _merge_pending_messages(self, messages: list[MessageContext]) -> str:
        """Merge pending messages into a single context string."""
        if not messages:
            return ""

        if len(messages) == 1:
            return messages[0].content

        parts = []
        for msg in messages:
            timestamp_str = ""
            if msg.timestamp:
                # Convert to readable time
                import datetime
                ts = datetime.datetime.fromtimestamp(msg.timestamp / 1000)
                timestamp_str = ts.strftime("%H:%M")

            sender = msg.sender_name or msg.sender_id.split(":")[0].lstrip("@")
            parts.append(f"[{timestamp_str}] {sender}: {msg.content}")

        return "\n".join(parts)

    async def _on_files_pulled(self, pulled_files: list[str]) -> None:
        """Handle files pulled from MinIO."""
        console.print(f"[yellow]Pulled {len(pulled_files)} files from MinIO[/yellow]")

        # Check for config changes
        for f in pulled_files:
            if "SOUL.md" in f or "AGENTS.md" in f:
                console.print("[yellow]Config changed, reloading...[/yellow]")
                try:
                    soul = self.sync.get_soul()
                    agents = self.sync.get_agents_md()
                    system_prompt = self._build_system_prompt(soul, agents)
                    self.agent.set_system_prompt(system_prompt)
                    # Also update all room agents
                    for room_agent in self.room_agents.values():
                        room_agent.set_system_prompt(system_prompt)
                    console.print("[green]Config reloaded.[/green]")
                except Exception as e:
                    console.print(f"[red]Failed to reload config: {e}[/red]")

            if "openclaw.json" in f:
                console.print("[yellow]openclaw.json changed, reloading message rules...[/yellow]")
                try:
                    openclaw_config = self.sync.get_config()
                    self.message_rules = MessageRules(openclaw_config)
                    console.print("[green]Message rules reloaded.[/green]")
                except Exception as e:
                    console.print(f"[red]Failed to reload openclaw.json: {e}[/red]")


async def main() -> None:
    """Main entry point."""
    import os
    from copaw_worker.config import load_config

    # Bypass proxy for local hiclaw.io domains so matrix-nio (aiohttp) and
    # other HTTP clients don't route through any configured http_proxy
    existing_no_proxy = os.environ.get("no_proxy", os.environ.get("NO_PROXY", ""))
    hiclaw_domains = "hiclaw.io,localhost,127.0.0.1"
    if existing_no_proxy:
        os.environ["no_proxy"] = f"{existing_no_proxy},{hiclaw_domains}"
        os.environ["NO_PROXY"] = f"{existing_no_proxy},{hiclaw_domains}"
    else:
        os.environ["no_proxy"] = hiclaw_domains
        os.environ["NO_PROXY"] = hiclaw_domains

    config = load_config()
    worker = Worker(config)

    # Set up signal handlers
    loop = asyncio.get_event_loop()

    def handle_signal():
        console.print("\n[yellow]Received shutdown signal...[/yellow]")
        asyncio.create_task(worker.stop())

    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, handle_signal)

    # Run worker
    await worker.run()


if __name__ == "__main__":
    asyncio.run(main())
