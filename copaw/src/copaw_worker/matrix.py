"""
Matrix client for CoPaw Worker.
"""

import asyncio
import re
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Callable, Optional

from nio import (
    AsyncClient,
    Event,
    LoginResponse,
    MatrixRoom,
    RoomMessageText,
    SyncResponse,
)
from nio.responses import WhoamiResponse
from rich.console import Console

console = Console()


class RoomType(Enum):
    """Room type for message routing."""
    DM = "dm"
    GROUP = "group"


@dataclass
class MessageContext:
    """Context for an incoming Matrix message."""
    room_id: str
    room_type: RoomType
    sender_id: str
    sender_name: str
    content: str
    event_id: str
    timestamp: int
    was_mentioned: bool = False
    has_explicit_mention: bool = False
    # For group rooms
    room_name: str = ""
    room_alias: str = ""


@dataclass
class RoomState:
    """State for a single room/conversation."""
    room_id: str
    room_type: RoomType
    # Buffered messages waiting for mention
    pending_messages: list[MessageContext] = field(default_factory=list)
    # Conversation history for this room
    history: list[dict] = field(default_factory=list)
    # Last activity timestamp
    last_activity: int = 0


class MatrixClient:
    """Matrix client for worker communication."""

    def __init__(
        self,
        homeserver: str,
        username: str = "",
        password: str = "",
        access_token: str = "",
        device_name: str = "copaw-worker",
    ):
        """
        Initialize Matrix client.

        Args:
            homeserver: Matrix homeserver URL (e.g., "https://matrix-local.hiclaw.io:8080")
            username: Matrix username (without @ and domain) - optional if using token
            password: Matrix password - optional if using token
            access_token: Matrix access token - takes precedence over password login
            device_name: Device name for this client
        """
        self.homeserver = homeserver
        self.username = username
        self.password = password
        self._initial_access_token = access_token  # Store initial token
        self.device_name = device_name

        # Will be set after login
        self.user_id: Optional[str] = None
        self.device_id: Optional[str] = None
        self.access_token: Optional[str] = None  # Final token after login

        # nio client
        self.client: Optional[AsyncClient] = None

        # Message handlers
        self.message_handlers: list[Callable] = []

        # Room states (per-room context isolation)
        self.room_states: dict[str, RoomState] = {}

        # DM detection
        self._dm_rooms: set[str] = set()

    async def login(self) -> bool:
        """
        Log in to Matrix server.

        Supports two login methods:
        1. Access token (preferred) - directly use existing token
        2. Username + password - login to get token

        Returns:
            True if login successful
        """
        self.client = AsyncClient(self.homeserver, user="")

        # Method 1: Use existing access token
        if self._initial_access_token:
            console.print("[yellow]Logging in with access token...[/yellow]")
            self.client.access_token = self._initial_access_token

            # Verify token by calling whoami
            try:
                whoami = await self.client.whoami()
                if isinstance(whoami, WhoamiResponse):
                    self.user_id = whoami.user_id
                    self.device_id = whoami.device_id or "copaw-worker"
                    self.access_token = self._initial_access_token
                    # Set user_id on the nio client so it filters self-messages correctly
                    self.client.user_id = whoami.user_id
                    self.client.user = whoami.user_id
                    console.print(f"[green]Logged in as {self.user_id} (via token)[/green]")
                    return True
                else:
                    console.print(f"[red]Token login failed: {whoami}[/red]")
                    return False
            except Exception as e:
                console.print(f"[red]Token login error: {e}[/red]")
                return False

        # Method 2: Login with username/password
        if not self.username or not self.password:
            console.print("[red]No access_token or username/password provided[/red]")
            return False

        console.print(f"[yellow]Logging in as {self.username}...[/yellow]")
        try:
            response = await self.client.login(
                self.username,
                self.password,
                device_name=self.device_name,
            )

            if isinstance(response, LoginResponse):
                self.user_id = response.user_id
                self.device_id = response.device_id
                self.access_token = response.access_token

                # Verify whoami
                whoami = await self.client.whoami()
                if isinstance(whoami, WhoamiResponse):
                    console.print(f"[green]Logged in as {self.user_id}[/green]")
                    return True
                else:
                    console.print(f"[red]Whoami failed: {whoami}[/red]")
                    return False
            else:
                console.print(f"[red]Login failed: {response}[/red]")
                return False

        except Exception as e:
            console.print(f"[red]Login error: {e}[/red]")
            return False

    async def logout(self) -> None:
        """Log out and close client."""
        if self.client:
            await self.client.logout()
            await self.client.close()
            self.client = None

    def on_message(self, handler: Callable) -> None:
        """
        Register a message handler.

        Args:
            handler: Async function(ctx: MessageContext, room_state: RoomState) to handle messages
        """
        self.message_handlers.append(handler)

    def _detect_room_type(self, room: MatrixRoom) -> RoomType:
        """Detect if a room is DM or group."""
        # DM rooms typically have exactly 2 members
        if len(room.users) == 2:
            return RoomType.DM
        return RoomType.GROUP

    def _check_mention(
        self,
        content: dict,
        text: str,
        mention_regexes: list[re.Pattern],
    ) -> tuple[bool, bool]:
        """
        Check if the worker was mentioned.

        Returns:
            (was_mentioned, has_explicit_mention)
        """
        # Check m.mentions
        mentions = content.get("m.mentions", {})
        mentioned_users = set(mentions.get("user_ids", []))
        room_mention = mentions.get("room", False)

        has_explicit_mention = bool(mentions)
        was_mentioned = room_mention or (self.user_id in mentioned_users)

        # Check text patterns (e.g., @worker_name)
        if not was_mentioned:
            for pattern in mention_regexes:
                if pattern.search(text):
                    was_mentioned = True
                    break

        return was_mentioned, has_explicit_mention

    def _build_mention_regexes(self, worker_name: str) -> list[re.Pattern]:
        """Build regex patterns for mention detection."""
        patterns = [
            # @worker_name or @worker_name:server
            re.compile(rf"@?{re.escape(worker_name)}(?::[^:\s]+)?", re.IGNORECASE),
            # worker_name at word boundary
            re.compile(rf"\b{re.escape(worker_name)}\b", re.IGNORECASE),
        ]
        return patterns

    async def _handle_room_event(
        self,
        room: MatrixRoom,
        event: Event,
    ) -> None:
        """Handle incoming room event."""
        if not isinstance(event, RoomMessageText):
            return

        # Skip our own messages
        if event.sender == self.user_id:
            return

        room_id = room.room_id
        sender_id = event.sender
        content_dict = event.source.get("content", {})
        text = event.body or ""

        # Detect room type
        room_type = self._detect_room_type(room)

        # Get or create room state
        if room_id not in self.room_states:
            self.room_states[room_id] = RoomState(
                room_id=room_id,
                room_type=room_type,
            )
        room_state = self.room_states[room_id]
        room_state.last_activity = event.server_timestamp

        # Build mention regexes (using worker name from user_id)
        worker_name = self.user_id.split(":")[0].lstrip("@") if self.user_id else ""
        mention_regexes = self._build_mention_regexes(worker_name)

        # Check mention
        was_mentioned, has_explicit_mention = self._check_mention(
            content_dict, text, mention_regexes
        )

        # Get sender display name
        sender_name = ""
        if sender_id in room.users:
            sender_name = room.users[sender_id].display_name or sender_id

        # Build message context
        ctx = MessageContext(
            room_id=room_id,
            room_type=room_type,
            sender_id=sender_id,
            sender_name=sender_name,
            content=text,
            event_id=event.event_id,
            timestamp=event.server_timestamp,
            was_mentioned=was_mentioned,
            has_explicit_mention=has_explicit_mention,
            room_name=room.name or "",
            room_alias=room.canonical_alias or "",
        )

        # Call all registered handlers
        for handler in self.message_handlers:
            try:
                await handler(ctx, room_state)
            except Exception as e:
                console.print(f"[yellow]Handler error: {e}[/yellow]")

    async def send_text(
        self,
        room_id: str,
        text: str,
        mentions: Optional[list[str]] = None,
    ) -> bool:
        """
        Send a text message to a room.

        Args:
            room_id: Room ID to send to
            text: Message text
            mentions: List of user IDs to mention

        Returns:
            True if send successful
        """
        if not self.client:
            console.print("[red]Client not logged in[/red]")
            return False

        content = {
            "msgtype": "m.text",
            "body": text,
        }

        # Add mentions if provided
        if mentions:
            content["m.mentions"] = {"user_ids": mentions}

        try:
            await self.client.room_send(
                room_id,
                "m.room.message",
                content,
            )
            return True
        except Exception as e:
            console.print(f"[red]Send error: {e}[/red]")
            return False

    async def send_markdown(
        self,
        room_id: str,
        text: str,
        mentions: Optional[list[str]] = None,
    ) -> bool:
        """
        Send a markdown-formatted message to a room.

        Args:
            room_id: Room ID to send to
            text: Markdown text
            mentions: List of user IDs to mention

        Returns:
            True if send successful
        """
        if not self.client:
            console.print("[red]Client not logged in[/red]")
            return False

        # TODO: Convert markdown to HTML
        content = {
            "msgtype": "m.text",
            "body": text,
            "format": "org.matrix.custom.html",
            "formatted_body": text,
        }

        if mentions:
            content["m.mentions"] = {"user_ids": mentions}

        try:
            await self.client.room_send(
                room_id,
                "m.room.message",
                content,
            )
            return True
        except Exception as e:
            console.print(f"[red]Send error: {e}[/red]")
            return False

    async def sync_forever(
        self,
        timeout: int = 30000,
        since: Optional[str] = None,
    ) -> None:
        """
        Run sync loop forever, auto-joining invited rooms.

        Args:
            timeout: Sync timeout in milliseconds
            since: Sync token to resume from
        """
        if not self.client:
            raise RuntimeError("Client not logged in")

        # Set up callback
        self.client.add_event_callback(self._handle_room_event, (RoomMessageText,))

        # Manual sync loop so we can handle invites
        next_batch = since
        while True:
            try:
                response = await self.client.sync(
                    timeout=timeout,
                    since=next_batch,
                    full_state=(next_batch is None),
                )
                if isinstance(response, SyncResponse):
                    next_batch = response.next_batch
                    # Auto-join any invited rooms
                    for room_id in response.rooms.invite:
                        console.print(f"[yellow]Auto-joining invited room: {room_id}[/yellow]")
                        join_resp = await self.client.join(room_id)
                        console.print(f"[green]Joined room: {room_id} -> {join_resp}[/green]")
                else:
                    console.print(f"[yellow]Sync error: {response}[/yellow]")
                    await asyncio.sleep(5)
            except asyncio.CancelledError:
                break
            except Exception as e:
                console.print(f"[red]Sync exception: {e}[/red]")
                await asyncio.sleep(5)

    async def sync_once(self, timeout: int = 30000) -> SyncResponse:
        """
        Perform a single sync.

        Args:
            timeout: Sync timeout in milliseconds

        Returns:
            Sync response
        """
        if not self.client:
            raise RuntimeError("Client not logged in")

        return await self.client.sync(timeout=timeout, full_state=True)

    def get_joined_rooms(self) -> list[str]:
        """Get list of joined room IDs."""
        if not self.client:
            return []
        return list(self.client.rooms.keys())

    def get_room_state(self, room_id: str) -> Optional[RoomState]:
        """Get room state for a specific room."""
        return self.room_states.get(room_id)
