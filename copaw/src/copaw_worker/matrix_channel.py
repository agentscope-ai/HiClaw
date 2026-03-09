"""
MatrixChannel: CoPaw BaseChannel implementation for Matrix (via matrix-nio).

This file is installed into ~/.copaw/custom_channels/ at worker startup
so CoPaw's channel registry picks it up automatically.
"""
from __future__ import annotations

import asyncio
import logging
import re
from typing import Any, AsyncIterator, Callable, Dict, List, Optional

from nio import (
    AsyncClient,
    LoginResponse,
    MatrixRoom,
    RoomMessageText,
    SyncResponse,
)
from nio.responses import WhoamiResponse

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Lazy import of CoPaw base types so this file can be syntax-checked without
# copaw installed (it's only executed inside a copaw environment).
# ---------------------------------------------------------------------------
try:
    from copaw.app.channels.base import BaseChannel
    from copaw.app.channels.schema import ChannelType
    from agentscope_runtime.engine.schemas.agent_schemas import (
        ContentType,
        TextContent,
    )
    _COPAW_AVAILABLE = True
except ImportError:  # pragma: no cover
    _COPAW_AVAILABLE = False
    BaseChannel = object  # type: ignore[assignment,misc]
    ChannelType = str  # type: ignore[assignment]


CHANNEL_KEY = "matrix"


class MatrixChannelConfig:
    """Parsed config for MatrixChannel (read from config.json channels.matrix)."""

    def __init__(self, raw: dict[str, Any]) -> None:
        self.enabled: bool = raw.get("enabled", True)
        self.homeserver: str = raw.get("homeserver", "")
        self.access_token: str = raw.get("access_token", "")
        # username/password fallback (rarely used in hiclaw)
        self.username: str = raw.get("username", "")
        self.password: str = raw.get("password", "")
        self.device_name: str = raw.get("device_name", "copaw-worker")

        # Allowlist / policy
        self.dm_policy: str = raw.get("dm_policy", "allowlist")
        self.allow_from: list[str] = [
            _normalize_user_id(u) for u in raw.get("allow_from", [])
        ]
        self.group_policy: str = raw.get("group_policy", "allowlist")
        self.group_allow_from: list[str] = [
            _normalize_user_id(u) for u in raw.get("group_allow_from", [])
        ]
        # Per-room overrides: {"*": {"requireMention": true}, ...}
        self.groups: dict[str, Any] = raw.get("groups", {})

        self.bot_prefix: str = raw.get("bot_prefix", "")
        self.filter_tool_messages: bool = raw.get("filter_tool_messages", False)
        self.filter_thinking: bool = raw.get("filter_thinking", False)


def _normalize_user_id(uid: str) -> str:
    uid = uid.strip().lower()
    if not uid.startswith("@"):
        uid = "@" + uid
    return uid


class MatrixChannel(BaseChannel):
    """CoPaw channel that connects to a Matrix homeserver via matrix-nio."""

    channel = CHANNEL_KEY  # type: ignore[assignment]
    uses_manager_queue: bool = True

    def __init__(
        self,
        process: Callable,
        config: MatrixChannelConfig,
        on_reply_sent: Optional[Callable] = None,
        show_tool_details: bool = True,
        filter_tool_messages: bool = False,
        filter_thinking: bool = False,
    ) -> None:
        super().__init__(
            process=process,
            on_reply_sent=on_reply_sent,
            show_tool_details=show_tool_details,
            filter_tool_messages=filter_tool_messages,
            filter_thinking=filter_thinking,
        )
        self._cfg = config
        self._client: Optional[AsyncClient] = None
        self._user_id: Optional[str] = None
        self._sync_task: Optional[asyncio.Task] = None
        self._typing_tasks: Dict[str, asyncio.Task] = {}  # room_id -> renewal task

    # ------------------------------------------------------------------
    # Factory
    # ------------------------------------------------------------------

    @classmethod
    def from_config(
        cls,
        process: Callable,
        config: Any,
        on_reply_sent: Optional[Callable] = None,
        show_tool_details: bool = True,
        filter_tool_messages: bool = False,
        filter_thinking: bool = False,
    ) -> "MatrixChannel":
        if isinstance(config, dict):
            cfg = MatrixChannelConfig(config)
        elif isinstance(config, MatrixChannelConfig):
            cfg = config
        else:
            # SimpleNamespace or other object — convert to dict via __dict__
            cfg = MatrixChannelConfig(vars(config))
        return cls(
            process=process,
            config=cfg,
            on_reply_sent=on_reply_sent,
            show_tool_details=show_tool_details,
            filter_tool_messages=filter_tool_messages or cfg.filter_tool_messages,
            filter_thinking=filter_thinking or cfg.filter_thinking,
        )

    @classmethod
    def from_env(cls, process: Callable, on_reply_sent=None) -> "MatrixChannel":
        import os
        cfg = MatrixChannelConfig({
            "homeserver": os.environ.get("HICLAW_MATRIX_SERVER", ""),
            "access_token": os.environ.get("HICLAW_MATRIX_TOKEN", ""),
        })
        return cls(process=process, config=cfg, on_reply_sent=on_reply_sent)

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def start(self) -> None:
        if not self._cfg.homeserver:
            logger.warning("MatrixChannel: homeserver not configured, skipping")
            return

        self._client = AsyncClient(self._cfg.homeserver, user="")

        # Login
        if self._cfg.access_token:
            self._client.access_token = self._cfg.access_token
            whoami = await self._client.whoami()
            if isinstance(whoami, WhoamiResponse):
                self._user_id = whoami.user_id
                self._client.user_id = whoami.user_id
                self._client.user = whoami.user_id
                logger.info("MatrixChannel: logged in as %s (token)", self._user_id)
            else:
                logger.error("MatrixChannel: token login failed: %s", whoami)
                return
        elif self._cfg.username and self._cfg.password:
            resp = await self._client.login(
                self._cfg.username,
                self._cfg.password,
                device_name=self._cfg.device_name,
            )
            if isinstance(resp, LoginResponse):
                self._user_id = resp.user_id
                logger.info("MatrixChannel: logged in as %s (password)", self._user_id)
            else:
                logger.error("MatrixChannel: password login failed: %s", resp)
                return
        else:
            logger.error("MatrixChannel: no credentials configured")
            return

        # Register event callback and start sync loop
        self._client.add_event_callback(self._on_room_event, (RoomMessageText,))
        self._sync_task = asyncio.create_task(self._sync_loop())
        logger.info("MatrixChannel: sync loop started")

    async def stop(self) -> None:
        if self._sync_task:
            self._sync_task.cancel()
            try:
                await self._sync_task
            except asyncio.CancelledError:
                pass
        if self._client:
            await self._client.close()
        logger.info("MatrixChannel: stopped")

    # ------------------------------------------------------------------
    # Sync loop
    # ------------------------------------------------------------------

    async def _sync_loop(self) -> None:
        next_batch: Optional[str] = None
        while True:
            try:
                resp = await self._client.sync(
                    timeout=30000,
                    since=next_batch,
                    full_state=(next_batch is None),
                )
                if isinstance(resp, SyncResponse):
                    next_batch = resp.next_batch
                    # Auto-join invited rooms
                    for room_id in resp.rooms.invite:
                        logger.info("MatrixChannel: auto-joining %s", room_id)
                        await self._client.join(room_id)
                else:
                    logger.warning("MatrixChannel: sync error: %s", resp)
                    await asyncio.sleep(5)
            except asyncio.CancelledError:
                break
            except Exception as exc:
                logger.exception("MatrixChannel: sync exception: %s", exc)
                await asyncio.sleep(5)

    # ------------------------------------------------------------------
    # Incoming message handling
    # ------------------------------------------------------------------

    async def _on_room_event(self, room: MatrixRoom, event: RoomMessageText) -> None:
        # Skip own messages
        if event.sender == self._user_id:
            return

        sender_id = event.sender
        room_id = room.room_id
        text = event.body or ""
        is_dm = len(room.users) == 2

        # Allowlist check
        normalized_sender = _normalize_user_id(sender_id)
        if is_dm:
            if self._cfg.dm_policy == "disabled":
                return
            if self._cfg.dm_policy == "allowlist":
                if normalized_sender not in self._cfg.allow_from:
                    logger.debug("MatrixChannel: DM blocked from %s", sender_id)
                    return
        else:
            if self._cfg.group_policy == "disabled":
                return
            if self._cfg.group_policy == "allowlist":
                if normalized_sender not in self._cfg.group_allow_from:
                    logger.debug("MatrixChannel: group msg blocked from %s", sender_id)
                    return

        # Mention check for group rooms
        if not is_dm:
            require_mention = self._require_mention(room_id)
            if require_mention and not self._was_mentioned(event, text):
                return  # silently ignore non-mention group messages

        # Mark as read + start typing immediately so the sender sees feedback
        await self._send_read_receipt(room_id, event.event_id)
        await self._send_typing(room_id, True)

        # Build native payload and enqueue
        worker_name = (self._user_id or "").split(":")[0].lstrip("@")
        payload = {
            "channel_id": CHANNEL_KEY,
            "sender_id": sender_id,
            "content_parts": [
                {"type": "text", "text": text}
            ],
            "meta": {
                "room_id": room_id,
                "is_dm": is_dm,
                "worker_name": worker_name,
                "event_id": event.event_id,
            },
        }

        if self._enqueue:
            self._enqueue(payload)

    def _require_mention(self, room_id: str) -> bool:
        """Check per-room config, fall back to global default (require mention)."""
        room_cfg = self._cfg.groups.get(room_id) or self._cfg.groups.get("*")
        if room_cfg:
            if room_cfg.get("autoReply") is True:
                return False
            if "requireMention" in room_cfg:
                return bool(room_cfg["requireMention"])
        return True  # default: require mention in group rooms

    # ------------------------------------------------------------------
    # Read receipt & typing indicator
    # ------------------------------------------------------------------

    async def _send_read_receipt(self, room_id: str, event_id: str) -> None:
        """Mark a message as read (sends both read receipt and read marker)."""
        if not self._client or not event_id:
            return
        try:
            await self._client.room_read_markers(room_id, fully_read_event=event_id, read_event=event_id)
        except Exception as exc:
            logger.debug("MatrixChannel: read receipt failed for %s: %s", event_id, exc)

    async def _send_typing(self, room_id: str, typing: bool, timeout: int = 30000) -> None:
        """Set typing indicator on/off for a room.

        When turning on, starts a background renewal task that re-sends the
        typing indicator every 25s (before the 30s server timeout expires),
        up to a 2-minute hard cap. When turning off, cancels the renewal task.
        """
        if not self._client:
            return
        # Cancel any existing renewal task for this room
        existing = self._typing_tasks.pop(room_id, None)
        if existing and not existing.done():
            existing.cancel()
        try:
            await self._client.room_typing(room_id, typing_state=typing, timeout=timeout)
        except Exception as exc:
            logger.debug("MatrixChannel: typing indicator failed for %s: %s", room_id, exc)
        # Start renewal loop if turning on
        if typing:
            self._typing_tasks[room_id] = asyncio.create_task(
                self._typing_renewal_loop(room_id, timeout)
            )

    async def _typing_renewal_loop(self, room_id: str, timeout: int = 30000) -> None:
        """Re-send typing=true every 25s, hard-capped at 2 minutes."""
        max_duration = 120  # seconds
        renewal_interval = 25  # seconds (renew before 30s server timeout)
        elapsed = 0
        try:
            while elapsed < max_duration:
                await asyncio.sleep(renewal_interval)
                elapsed += renewal_interval
                if not self._client:
                    break
                await self._client.room_typing(room_id, typing_state=True, timeout=timeout)
        except asyncio.CancelledError:
            pass
        except Exception as exc:
            logger.debug("MatrixChannel: typing renewal failed for %s: %s", room_id, exc)
        finally:
            # If we hit the 2-min cap, explicitly stop typing
            if elapsed >= max_duration and self._client:
                try:
                    await self._client.room_typing(room_id, typing_state=False)
                except Exception:
                    pass
            self._typing_tasks.pop(room_id, None)

    def _was_mentioned(self, event: RoomMessageText, text: str) -> bool:
        if not self._user_id:
            return False
        # 1. Check m.mentions (structured mention from Matrix spec)
        content = event.source.get("content", {})
        mentions = content.get("m.mentions", {})
        if self._user_id in mentions.get("user_ids", []):
            return True
        if mentions.get("room"):
            return True
        # 2. Check formatted_body for matrix.to mention links (Element HTML format)
        formatted_body = content.get("formatted_body", "")
        if formatted_body and self._user_id:
            import urllib.parse
            escaped_uid = re.escape(self._user_id)
            if re.search(rf'href=["\']https://matrix\.to/#/{escaped_uid}["\']', formatted_body, re.IGNORECASE):
                return True
            encoded_uid = re.escape(urllib.parse.quote(self._user_id))
            if re.search(rf'href=["\']https://matrix\.to/#/{encoded_uid}["\']', formatted_body, re.IGNORECASE):
                return True
        # 3. Fallback: match full MXID in plain text (e.g. @data:matrix-local.hiclaw.io:18080)
        if self._user_id and re.search(re.escape(self._user_id), text, re.IGNORECASE):
            return True
        return False

    # ------------------------------------------------------------------
    # build_agent_request_from_native (BaseChannel protocol)
    # ------------------------------------------------------------------

    def build_agent_request_from_native(self, native_payload: Any) -> Any:
        parts = native_payload.get("content_parts", [])
        meta = native_payload.get("meta", {})
        sender_id = native_payload.get("sender_id", "")
        room_id = meta.get("room_id", sender_id)
        session_id = f"matrix:{room_id}"

        content = [
            TextContent(type=ContentType.TEXT, text=p["text"])
            for p in parts
            if p.get("type") == "text" and p.get("text")
        ]
        if not content:
            content = [TextContent(type=ContentType.TEXT, text="")]

        req = self.build_agent_request_from_user_content(
            channel_id=CHANNEL_KEY,
            sender_id=sender_id,
            session_id=session_id,
            content_parts=content,
            channel_meta=meta,
        )
        req.channel_meta = meta  # type: ignore[attr-defined]
        return req

    def resolve_session_id(self, sender_id: str, channel_meta=None) -> str:
        room_id = (channel_meta or {}).get("room_id", sender_id)
        return f"matrix:{room_id}"

    def get_to_handle_from_request(self, request: Any) -> str:
        meta = getattr(request, "channel_meta", {}) or {}
        return meta.get("room_id", getattr(request, "user_id", ""))

    # ------------------------------------------------------------------
    # Outgoing send
    # ------------------------------------------------------------------

    async def send(
        self,
        to_handle: str,
        text: str,
        meta: Optional[Dict[str, Any]] = None,
    ) -> None:
        if not self._client:
            logger.error("MatrixChannel: send called but client not ready")
            return

        room_id = to_handle
        content: dict[str, Any] = {"msgtype": "m.text", "body": text}

        # Mention the original sender if available
        sender_id = (meta or {}).get("sender_id") or (meta or {}).get("user_id")
        if sender_id:
            content["m.mentions"] = {"user_ids": [sender_id]}

        try:
            await self._client.room_send(room_id, "m.room.message", content)
        except Exception as exc:
            logger.exception("MatrixChannel: send failed to %s: %s", room_id, exc)
        finally:
            # Stop typing indicator after reply is sent (or failed)
            await self._send_typing(room_id, False)
