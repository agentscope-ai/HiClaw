"""hermes-agent Matrix adapter using ``matrix-nio``.

Replaces hermes-agent's stock ``gateway/platforms/matrix.py`` (which uses the
``mautrix`` SDK) with a ``matrix-nio`` based implementation that re-uses the
policies and behaviour HiClaw's CoPaw worker already runs in production:

  * DM allow-list and group allow-list (separate policies)
  * @mention requirement in groups, with optional ``free_response_rooms``
  * Per-room history buffering with copaw-style markers so the agent sees
    surrounding non-mentioned context when it finally is addressed
  * Vision input gated on the active model's capabilities
  * Optional end-to-end encryption (matrix-nio[e2e] + libolm)

The class name ``MatrixAdapter`` and the helper ``check_matrix_requirements``
are kept identical to the upstream module so the gateway's
``_create_adapter`` keeps working without changes:

    from gateway.platforms.matrix import MatrixAdapter, check_matrix_requirements

Environment variables consumed (all set by ``hermes_worker.bridge``):

  MATRIX_HOMESERVER          — homeserver base URL
  MATRIX_ACCESS_TOKEN        — bot access token (required)
  MATRIX_USER_ID             — bot mxid (whoami also resolves it)
  MATRIX_DEVICE_ID           — device id; required for E2EE persistence
  MATRIX_ENCRYPTION          — "true" / "false"
  MATRIX_DM_POLICY           — "open" | "allowlist" (default allowlist)
  MATRIX_GROUP_POLICY        — "open" | "allowlist" (default allowlist)
  MATRIX_ALLOWED_USERS       — CSV of mxids allowed to DM the bot
  MATRIX_GROUP_ALLOW_FROM    — CSV of mxids allowed to address the bot in groups
  MATRIX_REQUIRE_MENTION     — "true" / "false" (default true)
  MATRIX_FREE_RESPONSE_ROOMS — CSV of room ids exempt from mention requirement
  MATRIX_HOME_ROOM           — default room id for cron / push delivery
  MATRIX_AUTO_THREAD         — auto-create threads when replying in groups
  MATRIX_DM_MENTION_THREADS  — also create threads in DM on @mention
  MATRIX_VISION_ENABLED      — "true" / "false"; controls image download
  MATRIX_HISTORY_LIMIT       — max non-mentioned messages buffered per room
  MATRIX_FILTER_TOOL_MESSAGES — "true" / "false"
  MATRIX_FILTER_THINKING     — "true" / "false"
"""
from __future__ import annotations

import asyncio
import html
import logging
import mimetypes
import os
import re
import time
import urllib.parse
import uuid
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional, Set, Tuple

try:
    import httpx
except ImportError:  # pragma: no cover — httpx is a hard dep of nio
    httpx = None  # type: ignore[assignment]

try:
    from nio import (
        AsyncClient,
        AsyncClientConfig,
        InviteMemberEvent,
        JoinedMembersResponse,
        LoginResponse,
        MatrixRoom,
        MegolmEvent,
        RoomEncryptedAudio,
        RoomEncryptedFile,
        RoomEncryptedImage,
        RoomEncryptedVideo,
        RoomMessageAudio,
        RoomMessageFile,
        RoomMessageImage,
        RoomMessageText,
        RoomMessageVideo,
        UploadResponse,
        WhoamiResponse,
    )
except ImportError:  # pragma: no cover
    AsyncClient = None  # type: ignore[assignment]


from gateway.config import Platform, PlatformConfig
from gateway.platforms.base import (
    BasePlatformAdapter,
    MessageEvent,
    MessageType,
    SendResult,
    cache_image_from_bytes,
    get_audio_cache_dir,
    get_document_cache_dir,
)
from hermes_constants import get_hermes_dir

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Constants — ported from copaw/src/matrix/channel.py
# ---------------------------------------------------------------------------

DEFAULT_HISTORY_LIMIT = 50
SYNC_TIMEOUT_MS = 30_000
MATRIX_MAX_MESSAGE_CHARS = 4000

HISTORY_CONTEXT_MARKER = "[Chat messages since your last reply - for context]"
CURRENT_MESSAGE_MARKER = "[Current message - respond to this]"

# E2EE store path — per-profile so multiple agents on one host don't collide.
_STORE_DIR = get_hermes_dir("platforms/matrix/store", "matrix/store")


def _is_truthy(value: Optional[str]) -> bool:
    if value is None:
        return False
    return value.strip().lower() in ("true", "1", "yes", "on")


def _csv_set(value: Optional[str]) -> Set[str]:
    if not value:
        return set()
    return {p.strip() for p in value.split(",") if p.strip()}


def _normalize_user_id(uid: str) -> str:
    """Lowercase MXID and ensure leading ``@`` for allow-list set membership."""
    uid = (uid or "").strip().lower()
    if uid and not uid.startswith("@"):
        uid = "@" + uid
    return uid


def _md_to_html(text: str) -> str:
    """Render Matrix ``formatted_body`` HTML from markdown.

    Falls back to ``html.escape + <br>`` when ``markdown-it-py`` isn't present
    so a missing optional dep doesn't break message delivery.
    """
    try:
        from markdown_it import MarkdownIt

        md = MarkdownIt(
            "commonmark",
            {
                "html": False,
                "linkify": True,
                "breaks": True,
                "typographer": False,
            },
        )
        md.enable("strikethrough")
        md.enable("table")
        return md.render(text).rstrip("\n")
    except ImportError:
        return html.escape(text).replace("\n", "<br>\n")


# ---------------------------------------------------------------------------
# History entry
# ---------------------------------------------------------------------------

@dataclass
class _HistoryEntry:
    sender: str
    body: str
    timestamp: Optional[int] = None
    message_id: Optional[str] = None
    media_urls: List[str] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Public requirement check (used by gateway.run._create_adapter)
# ---------------------------------------------------------------------------

def check_matrix_requirements() -> bool:
    """Return True iff the Matrix adapter can usefully start."""
    if AsyncClient is None:
        logger.warning(
            "Matrix: matrix-nio not installed. "
            "Run: pip install 'matrix-nio[e2e]'"
        )
        return False

    homeserver = os.getenv("MATRIX_HOMESERVER", "").strip()
    token = os.getenv("MATRIX_ACCESS_TOKEN", "").strip()
    if not homeserver:
        logger.debug("Matrix: MATRIX_HOMESERVER not set")
        return False
    if not token:
        logger.debug("Matrix: MATRIX_ACCESS_TOKEN not set")
        return False

    # E2EE is optional — we don't fail-closed if libolm isn't available; the
    # ``connect()`` method downgrades to plaintext-only with a warning.
    return True


# ---------------------------------------------------------------------------
# Adapter
# ---------------------------------------------------------------------------

class MatrixAdapter(BasePlatformAdapter):
    """Hermes Matrix adapter using ``matrix-nio``.

    All copaw policies are implemented in-line (no copaw runtime import) so
    the worker container does not need to bundle the agentscope-runtime stack.
    """

    def __init__(self, config: PlatformConfig):
        super().__init__(config, Platform.MATRIX)

        self._homeserver: str = (
            (config.extra.get("homeserver") if isinstance(config.extra, dict) else None)
            or os.getenv("MATRIX_HOMESERVER", "")
        ).rstrip("/")
        self._access_token: str = (
            config.token
            or os.getenv("MATRIX_ACCESS_TOKEN", "")
        )
        self._user_id: str = (
            (config.extra.get("user_id") if isinstance(config.extra, dict) else None)
            or os.getenv("MATRIX_USER_ID", "")
        )
        self._device_id: str = os.getenv("MATRIX_DEVICE_ID", "")
        self._encryption: bool = _is_truthy(os.getenv("MATRIX_ENCRYPTION"))

        # Policies — defaults match copaw's defaults for hiclaw deployments.
        self._dm_policy: str = (
            os.getenv("MATRIX_DM_POLICY", "allowlist") or "allowlist"
        ).lower()
        self._group_policy: str = (
            os.getenv("MATRIX_GROUP_POLICY", "allowlist") or "allowlist"
        ).lower()
        self._dm_allow: Set[str] = {
            _normalize_user_id(u) for u in _csv_set(os.getenv("MATRIX_ALLOWED_USERS"))
        }
        self._group_allow: Set[str] = {
            _normalize_user_id(u) for u in _csv_set(os.getenv("MATRIX_GROUP_ALLOW_FROM"))
        }

        self._require_mention: bool = _is_truthy(
            os.getenv("MATRIX_REQUIRE_MENTION", "true")
        )
        self._free_rooms: Set[str] = _csv_set(os.getenv("MATRIX_FREE_RESPONSE_ROOMS"))
        self._home_room: str = os.getenv("MATRIX_HOME_ROOM", "")
        self._auto_thread: bool = _is_truthy(
            os.getenv("MATRIX_AUTO_THREAD", "true")
        )
        self._dm_mention_threads: bool = _is_truthy(
            os.getenv("MATRIX_DM_MENTION_THREADS")
        )
        self._vision_enabled: bool = _is_truthy(
            os.getenv("MATRIX_VISION_ENABLED")
        )
        self._filter_tool_messages: bool = _is_truthy(
            os.getenv("MATRIX_FILTER_TOOL_MESSAGES", "true")
        )
        self._filter_thinking: bool = _is_truthy(
            os.getenv("MATRIX_FILTER_THINKING", "true")
        )

        try:
            self._history_limit: int = max(
                0, int(os.getenv("MATRIX_HISTORY_LIMIT", str(DEFAULT_HISTORY_LIMIT)))
            )
        except ValueError:
            self._history_limit = DEFAULT_HISTORY_LIMIT

        # Runtime state
        self._client: Optional[AsyncClient] = None
        self._sync_task: Optional[asyncio.Task] = None
        self._http_client: Optional[Any] = None  # httpx.AsyncClient
        self._room_history: Dict[str, List[_HistoryEntry]] = {}
        self._dm_room_cache: Dict[str, Tuple[bool, float]] = {}
        # Startup grace — drop replays older than start time so a restart
        # doesn't process the entire backlog.
        self._startup_ts: float = 0.0
        # Event de-duplication for at-least-once sync delivery.
        from collections import deque
        self._processed_events: deque = deque(maxlen=2000)
        self._processed_events_set: Set[str] = set()

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def connect(self) -> bool:
        if AsyncClient is None:
            self._set_fatal_error(
                "matrix_nio_missing",
                "matrix-nio not installed; cannot start Matrix adapter",
                retryable=False,
            )
            return False
        if not self._homeserver:
            self._set_fatal_error(
                "matrix_no_homeserver",
                "MATRIX_HOMESERVER is empty",
                retryable=False,
            )
            return False
        if not self._access_token:
            self._set_fatal_error(
                "matrix_no_token",
                "MATRIX_ACCESS_TOKEN is empty",
                retryable=False,
            )
            return False

        store_path: Optional[Path] = None
        if self._encryption:
            store_path = _STORE_DIR
            store_path.mkdir(parents=True, exist_ok=True)

        client_config = AsyncClientConfig(
            store_sync_tokens=True,
            encryption_enabled=self._encryption,
            request_timeout=max(SYNC_TIMEOUT_MS / 1000 + 30, 60),
        )
        self._client = AsyncClient(
            self._homeserver,
            user="",
            store_path=str(store_path) if store_path else "",
            config=client_config,
            device_id=self._device_id or None,
        )
        self._client.access_token = self._access_token

        whoami = await self._client.whoami()
        if not isinstance(whoami, WhoamiResponse):
            logger.error("Matrix: whoami failed: %s", whoami)
            self._set_fatal_error(
                "matrix_whoami_failed",
                f"Matrix whoami failed: {whoami}",
                retryable=True,
            )
            return False

        self._user_id = whoami.user_id
        self._client.user_id = whoami.user_id
        self._client.user = whoami.user_id
        if whoami.device_id:
            self._client.device_id = whoami.device_id
            self._device_id = whoami.device_id

        if self._encryption and store_path and self._client.device_id:
            try:
                self._client.load_store()
            except Exception as exc:
                logger.warning(
                    "Matrix: failed to load crypto store (E2EE may not work): %s", exc
                )
            try:
                if self._client.should_upload_keys:
                    await self._client.keys_upload()
            except Exception as exc:
                logger.warning("Matrix: keys_upload failed: %s", exc)
        elif self._encryption and not self._client.device_id:
            logger.error(
                "Matrix: encryption requested but whoami returned no device_id; "
                "disabling E2EE for this session."
            )
            self._encryption = False

        # Register event callbacks
        self._client.add_event_callback(
            self._on_room_message_text, (RoomMessageText,)
        )
        self._client.add_event_callback(
            self._on_room_message_media,
            (
                RoomMessageImage,
                RoomMessageFile,
                RoomMessageAudio,
                RoomMessageVideo,
            ),
        )
        self._client.add_event_callback(self._on_invite, (InviteMemberEvent,))
        if self._encryption:
            self._client.add_event_callback(
                self._on_room_message_media,
                (
                    RoomEncryptedImage,
                    RoomEncryptedFile,
                    RoomEncryptedAudio,
                    RoomEncryptedVideo,
                ),
            )
            self._client.add_event_callback(self._on_megolm, (MegolmEvent,))

        if httpx is not None:
            self._http_client = httpx.AsyncClient(
                follow_redirects=True, timeout=60.0
            )

        self._startup_ts = time.time()
        self._mark_connected()
        self._sync_task = asyncio.create_task(self._sync_loop())
        logger.info(
            "Matrix: logged in as %s (device=%s, encryption=%s)",
            self._user_id, self._device_id, self._encryption,
        )
        return True

    async def disconnect(self) -> None:
        if self._sync_task and not self._sync_task.done():
            self._sync_task.cancel()
            try:
                await self._sync_task
            except (asyncio.CancelledError, Exception):
                pass
        if self._http_client is not None:
            try:
                await self._http_client.aclose()
            except Exception:
                pass
            self._http_client = None
        if self._client is not None:
            try:
                await self._client.close()
            except Exception:
                pass
        self._mark_disconnected()
        logger.info("Matrix: disconnected")

    # ------------------------------------------------------------------
    # Sync loop
    # ------------------------------------------------------------------

    async def _sync_loop(self) -> None:
        assert self._client is not None
        backoff = 1.0
        while not self._closing_state():
            try:
                await self._client.sync_forever(
                    timeout=SYNC_TIMEOUT_MS,
                    full_state=False,
                    loop_sleep_time=0,
                )
                # sync_forever returns only on explicit cancel
                return
            except asyncio.CancelledError:
                return
            except Exception as exc:
                logger.warning("Matrix sync error (retry in %.1fs): %s", backoff, exc)
                await asyncio.sleep(backoff)
                backoff = min(backoff * 2, 60.0)

    def _closing_state(self) -> bool:
        return not self._running or self._client is None

    # ------------------------------------------------------------------
    # Auto-join invites — copaw runs explicit invitations from a Manager.
    # ------------------------------------------------------------------

    async def _on_invite(self, room: MatrixRoom, event: InviteMemberEvent) -> None:
        if event.state_key != self._user_id:
            return
        try:
            await self._client.join(room.room_id)
            logger.info("Matrix: auto-joined %s on invite from %s",
                        room.room_id, event.sender)
        except Exception as exc:
            logger.warning("Matrix: failed to join %s: %s", room.room_id, exc)

    async def _on_megolm(self, room: MatrixRoom, event: MegolmEvent) -> None:
        # Undecryptable event — we already requested the room key during
        # sync; nothing more to do besides log so operators notice missing
        # session keys.
        logger.warning(
            "Matrix: failed to decrypt event %s in %s (sender=%s)",
            getattr(event, "event_id", "?"), room.room_id, event.sender,
        )

    # ------------------------------------------------------------------
    # Policy helpers — DM detection, allow-list, mention parsing
    # ------------------------------------------------------------------

    def _is_dm_room(self, room: MatrixRoom) -> bool:
        cached = self._dm_room_cache.get(room.room_id)
        if cached and cached[1] > time.time() - 30:
            return cached[0]

        member_ids = list(getattr(room, "users", {}) or {}) or []
        # nio populates ``room.users`` after the first sync; fall back to
        # explicit member count when needed.
        if not member_ids:
            member_count = getattr(room, "member_count", 0) or 0
            is_dm = member_count == 2
        else:
            is_dm = len(member_ids) == 2 and self._user_id in member_ids
        self._dm_room_cache[room.room_id] = (is_dm, time.time())
        return is_dm

    def _check_allowed(self, room: MatrixRoom, sender: str) -> bool:
        """Return True iff the sender may address the bot in this room."""
        sender_norm = _normalize_user_id(sender)
        is_dm = self._is_dm_room(room)
        if is_dm:
            if self._dm_policy == "open":
                return True
            return sender_norm in self._dm_allow
        # Group / channel
        if self._group_policy == "open":
            return True
        return (
            sender_norm in self._group_allow
            or sender_norm in self._dm_allow
        )

    def _was_mentioned(self, event: Any, body: str) -> bool:
        """Detect @mentions of the bot.

        Considers both ``m.mentions`` (Matrix 1.7+) when present in the event
        source and a textual scan of ``body`` for ``@localpart`` /
        ``@localpart:server`` patterns.
        """
        # m.mentions structured field
        try:
            mentions = (event.source or {}).get("content", {}).get("m.mentions", {})
            user_ids = mentions.get("user_ids") if isinstance(mentions, dict) else None
            if isinstance(user_ids, list) and self._user_id in user_ids:
                return True
        except Exception:
            pass

        if not body or not self._user_id:
            return False
        localpart = self._user_id.split(":", 1)[0].lstrip("@")
        if not localpart:
            return False
        body_lower = body.lower()
        if f"@{self._user_id.lower()}" in body_lower:
            return True
        if f"@{localpart.lower()}" in body_lower:
            return True
        return False

    def _strip_mention_prefix(self, text: str) -> str:
        """Remove a leading ``@bot`` prefix so the agent sees a clean prompt."""
        if not self._user_id or not text:
            return text
        localpart = self._user_id.split(":", 1)[0].lstrip("@")
        candidates = [
            f"@{self._user_id}",
            f"@{localpart}:{self._homeserver_host()}",
            f"@{localpart}",
        ]
        stripped = text.lstrip()
        for cand in candidates:
            if stripped.lower().startswith(cand.lower()):
                stripped = stripped[len(cand):].lstrip(":,")
                stripped = stripped.lstrip()
                break
        return stripped or text

    def _homeserver_host(self) -> str:
        try:
            return urllib.parse.urlsplit(self._homeserver).netloc
        except Exception:
            return ""

    # ------------------------------------------------------------------
    # History buffering
    # ------------------------------------------------------------------

    def _record_history(self, room_id: str, entry: _HistoryEntry) -> None:
        if self._history_limit <= 0:
            return
        bucket = self._room_history.setdefault(room_id, [])
        bucket.append(entry)
        if len(bucket) > self._history_limit:
            del bucket[: len(bucket) - self._history_limit]

    def _take_history_prefix(self, room_id: str) -> Tuple[str, List[str]]:
        """Drain buffered history for *room_id* into a markdown prefix.

        Returns ``(prefix_text, image_urls)``.  ``prefix_text`` is empty when
        no history is buffered or when buffering is disabled.
        """
        bucket = self._room_history.pop(room_id, None)
        if not bucket:
            return "", []
        lines: List[str] = [HISTORY_CONTEXT_MARKER]
        images: List[str] = []
        for entry in bucket:
            sender = entry.sender or "unknown"
            body = (entry.body or "").rstrip()
            lines.append(f"- {sender}: {body}")
            images.extend(entry.media_urls)
        return "\n".join(lines) + "\n\n", images

    # ------------------------------------------------------------------
    # Inbound event handlers
    # ------------------------------------------------------------------

    def _is_self_event(self, event: Any) -> bool:
        return getattr(event, "sender", "") == self._user_id

    def _is_old_event(self, event: Any) -> bool:
        """Return True if the event predates startup grace period."""
        ts_ms = getattr(event, "server_timestamp", None)
        if ts_ms is None:
            return False
        return (ts_ms / 1000.0) < self._startup_ts - 5

    def _seen(self, event_id: Optional[str]) -> bool:
        if not event_id:
            return False
        if event_id in self._processed_events_set:
            return True
        if len(self._processed_events) == self._processed_events.maxlen:
            evicted = self._processed_events[0]
            self._processed_events_set.discard(evicted)
        self._processed_events.append(event_id)
        self._processed_events_set.add(event_id)
        return False

    async def _on_room_message_text(
        self, room: MatrixRoom, event: RoomMessageText
    ) -> None:
        if self._is_self_event(event) or self._is_old_event(event):
            return
        if self._seen(getattr(event, "event_id", None)):
            return

        body = (event.body or "").strip()
        if not body:
            return

        is_dm = self._is_dm_room(room)
        sender = event.sender or ""

        # Allow-list applies before mention check so unknown senders never
        # trigger any response, not even @mention probing.
        if not self._check_allowed(room, sender):
            return

        mentioned = self._was_mentioned(event, body)
        room_is_free = (
            not self._require_mention
            or room.room_id in self._free_rooms
        )
        should_respond = is_dm or mentioned or room_is_free

        if not should_respond:
            display_name = (
                getattr(room, "user_name", None)(sender)  # type: ignore[misc]
                if callable(getattr(room, "user_name", None))
                else sender
            )
            self._record_history(
                room.room_id,
                _HistoryEntry(
                    sender=display_name or sender,
                    body=body,
                    timestamp=getattr(event, "server_timestamp", None),
                    message_id=getattr(event, "event_id", None),
                ),
            )
            return

        cleaned_body = self._strip_mention_prefix(body) if mentioned else body
        history_prefix, history_images = self._take_history_prefix(room.room_id)
        if history_prefix:
            text = f"{history_prefix}{CURRENT_MESSAGE_MARKER}\n{cleaned_body}"
        else:
            text = cleaned_body

        source = self.build_source(
            chat_id=room.room_id,
            chat_name=getattr(room, "display_name", None) or room.room_id,
            chat_type="dm" if is_dm else "group",
            user_id=sender,
            user_name=getattr(room, "user_name", None)(sender)
            if callable(getattr(room, "user_name", None))
            else sender,
            thread_id=self._thread_root_for_event(event),
        )
        msg_event = MessageEvent(
            text=text,
            message_type=MessageType.TEXT,
            source=source,
            raw_message=event,
            message_id=getattr(event, "event_id", None),
            media_urls=history_images,
            media_types=["image"] * len(history_images),
        )
        await self.handle_message(msg_event)

    async def _on_room_message_media(
        self, room: MatrixRoom, event: Any
    ) -> None:
        if self._is_self_event(event) or self._is_old_event(event):
            return
        if self._seen(getattr(event, "event_id", None)):
            return

        sender = event.sender or ""
        if not self._check_allowed(room, sender):
            return

        is_dm = self._is_dm_room(room)
        # In groups, only consume media if the bot was @mentioned in the
        # event's body, or if the room is free-response. This matches copaw's
        # default policy (avoids noisy auto-responses to every image).
        body = (getattr(event, "body", "") or "").strip()
        mentioned = self._was_mentioned(event, body) if body else False
        room_is_free = (
            not self._require_mention
            or room.room_id in self._free_rooms
        )

        media_path: Optional[str] = None
        media_type = self._matrix_media_type(event)
        if media_type == "image" and self._vision_enabled:
            media_path = await self._download_media(event, ".jpg")
        elif media_type == "audio":
            media_path = await self._download_media(event, ".ogg")
        elif media_type == "video":
            media_path = await self._download_media(event, ".mp4")
        elif media_type == "file":
            ext = Path(getattr(event, "body", "") or "").suffix or ".bin"
            media_path = await self._download_media(event, ext)

        if not (is_dm or mentioned or room_is_free):
            # Buffer the media into history so the bot still has context if
            # it gets pinged later in the room.
            display_name = (
                getattr(room, "user_name", None)(sender)
                if callable(getattr(room, "user_name", None))
                else sender
            )
            self._record_history(
                room.room_id,
                _HistoryEntry(
                    sender=display_name or sender,
                    body=body or f"<sent {media_type or 'media'}>",
                    timestamp=getattr(event, "server_timestamp", None),
                    message_id=getattr(event, "event_id", None),
                    media_urls=[media_path] if media_path else [],
                ),
            )
            return

        history_prefix, history_images = self._take_history_prefix(room.room_id)
        text_body = self._strip_mention_prefix(body) if mentioned else body
        if not text_body:
            text_body = f"<{media_type or 'media'} attachment>"
        if history_prefix:
            text_body = (
                f"{history_prefix}{CURRENT_MESSAGE_MARKER}\n{text_body}"
            )

        media_urls = list(history_images)
        media_types: List[str] = ["image"] * len(history_images)
        if media_path:
            media_urls.append(media_path)
            media_types.append(media_type or "file")

        msg_type = MessageType.PHOTO if media_type == "image" else MessageType.DOCUMENT
        if media_type == "audio":
            msg_type = MessageType.VOICE
        elif media_type == "video":
            msg_type = MessageType.VIDEO

        source = self.build_source(
            chat_id=room.room_id,
            chat_name=getattr(room, "display_name", None) or room.room_id,
            chat_type="dm" if is_dm else "group",
            user_id=sender,
            user_name=getattr(room, "user_name", None)(sender)
            if callable(getattr(room, "user_name", None))
            else sender,
            thread_id=self._thread_root_for_event(event),
        )
        await self.handle_message(MessageEvent(
            text=text_body,
            message_type=msg_type,
            source=source,
            raw_message=event,
            message_id=getattr(event, "event_id", None),
            media_urls=media_urls,
            media_types=media_types,
        ))

    @staticmethod
    def _matrix_media_type(event: Any) -> Optional[str]:
        if isinstance(event, (RoomMessageImage, RoomEncryptedImage)):
            return "image"
        if isinstance(event, (RoomMessageAudio, RoomEncryptedAudio)):
            return "audio"
        if isinstance(event, (RoomMessageVideo, RoomEncryptedVideo)):
            return "video"
        if isinstance(event, (RoomMessageFile, RoomEncryptedFile)):
            return "file"
        return None

    def _thread_root_for_event(self, event: Any) -> Optional[str]:
        """Return the thread root event id if the message is in a thread.

        Matrix threads use a relation block with ``rel_type = m.thread``.
        """
        try:
            content = (event.source or {}).get("content", {})
        except Exception:
            return None
        relates = content.get("m.relates_to") or {}
        if relates.get("rel_type") == "m.thread":
            return relates.get("event_id")
        return None

    # ------------------------------------------------------------------
    # Media download (mxc:// → local cache)
    # ------------------------------------------------------------------

    async def _download_media(self, event: Any, default_ext: str) -> Optional[str]:
        if self._client is None:
            return None
        url = getattr(event, "url", "") or ""
        # Encrypted media events expose the file via .file
        file_meta = getattr(event, "file", None)
        try:
            if file_meta is not None and not url:
                url = getattr(file_meta, "url", "") or ""
            if not url or not url.startswith("mxc://"):
                return None
            mxc = url[len("mxc://"):]
            server, _, media_id = mxc.partition("/")
            if not server or not media_id:
                return None
            resp = await self._client.download(server, media_id)
            data = getattr(resp, "body", None) or getattr(resp, "data", None)
            if not data:
                logger.warning("Matrix: media download returned no body for %s", url)
                return None
            ext = self._infer_ext(event, default_ext)
            if ext.lower() in (".jpg", ".jpeg", ".png", ".gif", ".webp"):
                try:
                    return cache_image_from_bytes(data, ext)
                except ValueError as exc:
                    logger.warning("Matrix: not a valid image (%s); saving as raw", exc)
            target_dir = (
                get_audio_cache_dir() if ext.lower() in (".ogg", ".mp3", ".wav", ".m4a")
                else get_document_cache_dir()
            )
            filename = f"matrix_{uuid.uuid4().hex[:12]}{ext}"
            path = target_dir / filename
            path.write_bytes(data)
            return str(path)
        except Exception as exc:
            logger.warning("Matrix: media download failed (%s): %s", url, exc)
            return None

    @staticmethod
    def _infer_ext(event: Any, default_ext: str) -> str:
        body = getattr(event, "body", "") or ""
        if "." in body:
            ext = "." + body.rsplit(".", 1)[-1]
            if 1 < len(ext) <= 6:
                return ext
        info = (getattr(event, "source", {}) or {}).get("content", {}).get("info", {})
        mime = info.get("mimetype") if isinstance(info, dict) else None
        if mime:
            guess = mimetypes.guess_extension(mime)
            if guess:
                return guess
        return default_ext

    # ------------------------------------------------------------------
    # Outbound API — implements BasePlatformAdapter abstract methods
    # ------------------------------------------------------------------

    async def send(
        self,
        chat_id: str,
        content: str,
        reply_to: Optional[str] = None,
        metadata: Optional[Dict[str, Any]] = None,
    ) -> SendResult:
        if self._client is None:
            return SendResult(success=False, error="not connected", retryable=True)
        if not content:
            return SendResult(success=True)

        # Long messages are chunked by the base class via truncate_message; here
        # we still cap individual chunks so a buggy upstream call cannot DoS the
        # homeserver.
        if len(content) > MATRIX_MAX_MESSAGE_CHARS * 2:
            chunks = self.truncate_message(content, MATRIX_MAX_MESSAGE_CHARS)
        else:
            chunks = [content]

        thread_root = (metadata or {}).get("thread_id") if metadata else None
        last_event_id: Optional[str] = None

        for idx, chunk in enumerate(chunks):
            content_payload: Dict[str, Any] = {
                "msgtype": "m.text",
                "body": chunk,
                "format": "org.matrix.custom.html",
                "formatted_body": _md_to_html(chunk),
            }

            relates: Dict[str, Any] = {}
            if thread_root:
                relates["rel_type"] = "m.thread"
                relates["event_id"] = thread_root
                if reply_to and idx == 0:
                    relates["m.in_reply_to"] = {"event_id": reply_to}
                    relates["is_falling_back"] = True
            elif reply_to and idx == 0:
                relates["m.in_reply_to"] = {"event_id": reply_to}

            if relates:
                content_payload["m.relates_to"] = relates

            try:
                resp = await self._client.room_send(
                    room_id=chat_id,
                    message_type="m.room.message",
                    content=content_payload,
                    ignore_unverified_devices=True,
                )
                last_event_id = getattr(resp, "event_id", None) or last_event_id
            except Exception as exc:
                logger.warning("Matrix send failed: %s", exc)
                return SendResult(
                    success=False,
                    error=str(exc),
                    retryable=True,
                )

        return SendResult(success=True, message_id=last_event_id)

    async def send_image_file(
        self,
        chat_id: str,
        image_path: str,
        caption: Optional[str] = None,
        reply_to: Optional[str] = None,
        **kwargs,
    ) -> SendResult:
        url = await self._upload_file(image_path)
        if not url:
            return SendResult(success=False, error="upload_failed", retryable=True)
        if self._client is None:
            return SendResult(success=False, error="not connected", retryable=True)
        try:
            content_payload = {
                "msgtype": "m.image",
                "body": Path(image_path).name,
                "url": url,
            }
            await self._client.room_send(
                room_id=chat_id,
                message_type="m.room.message",
                content=content_payload,
                ignore_unverified_devices=True,
            )
        except Exception as exc:
            logger.warning("Matrix image send failed: %s", exc)
            return SendResult(success=False, error=str(exc), retryable=True)
        if caption:
            await self.send(chat_id=chat_id, content=caption, reply_to=reply_to)
        return SendResult(success=True)

    async def send_voice(
        self,
        chat_id: str,
        audio_path: str,
        caption: Optional[str] = None,
        reply_to: Optional[str] = None,
        **kwargs,
    ) -> SendResult:
        url = await self._upload_file(audio_path)
        if not url or self._client is None:
            return SendResult(success=False, error="upload_failed", retryable=True)
        try:
            await self._client.room_send(
                room_id=chat_id,
                message_type="m.room.message",
                content={
                    "msgtype": "m.audio",
                    "body": Path(audio_path).name,
                    "url": url,
                },
                ignore_unverified_devices=True,
            )
        except Exception as exc:
            return SendResult(success=False, error=str(exc), retryable=True)
        if caption:
            await self.send(chat_id=chat_id, content=caption, reply_to=reply_to)
        return SendResult(success=True)

    async def send_document(
        self,
        chat_id: str,
        file_path: str,
        caption: Optional[str] = None,
        file_name: Optional[str] = None,
        reply_to: Optional[str] = None,
        **kwargs,
    ) -> SendResult:
        url = await self._upload_file(file_path)
        if not url or self._client is None:
            return SendResult(success=False, error="upload_failed", retryable=True)
        try:
            await self._client.room_send(
                room_id=chat_id,
                message_type="m.room.message",
                content={
                    "msgtype": "m.file",
                    "body": file_name or Path(file_path).name,
                    "url": url,
                },
                ignore_unverified_devices=True,
            )
        except Exception as exc:
            return SendResult(success=False, error=str(exc), retryable=True)
        if caption:
            await self.send(chat_id=chat_id, content=caption, reply_to=reply_to)
        return SendResult(success=True)

    async def _upload_file(self, file_path: str) -> Optional[str]:
        if self._client is None:
            return None
        path = Path(file_path)
        if not path.exists() or not path.is_file():
            logger.warning("Matrix upload: %s does not exist", file_path)
            return None
        mime, _ = mimetypes.guess_type(file_path)
        if not mime:
            mime = "application/octet-stream"
        try:
            with path.open("rb") as fp:
                resp, _ = await self._client.upload(
                    fp,
                    content_type=mime,
                    filename=path.name,
                    filesize=path.stat().st_size,
                )
        except Exception as exc:
            logger.warning("Matrix upload failed: %s", exc)
            return None
        if isinstance(resp, UploadResponse):
            return resp.content_uri
        logger.warning("Matrix upload error response: %s", resp)
        return None

    # ------------------------------------------------------------------
    # Misc adapter API
    # ------------------------------------------------------------------

    async def get_chat_info(self, chat_id: str) -> Dict[str, Any]:
        if self._client is None:
            return {"name": chat_id, "type": "unknown"}
        room = self._client.rooms.get(chat_id)
        if room is None:
            return {"name": chat_id, "type": "unknown"}
        is_dm = self._is_dm_room(room)
        return {
            "name": getattr(room, "display_name", None) or chat_id,
            "type": "dm" if is_dm else "group",
            "members": list(getattr(room, "users", {}) or {}),
        }

    async def send_typing(
        self, chat_id: str, metadata: Optional[Dict[str, Any]] = None
    ) -> None:
        if self._client is None:
            return
        try:
            await self._client.room_typing(
                chat_id, typing_state=True, timeout=15000
            )
        except Exception as exc:
            logger.debug("Matrix room_typing failed: %s", exc)

    async def stop_typing(self, chat_id: str) -> None:
        if self._client is None:
            return
        try:
            await self._client.room_typing(chat_id, typing_state=False)
        except Exception as exc:
            logger.debug("Matrix stop_typing failed: %s", exc)
