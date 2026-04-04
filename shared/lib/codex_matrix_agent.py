#!/usr/bin/env python3
"""Run a HiClaw Manager/Worker as a Codex-backed Matrix bot.

This runtime keeps HiClaw's existing Matrix rooms, MinIO layout, and shell
scripts, but replaces the OpenClaw/CoPaw LLM loop with Codex app-server.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import queue
import random
import re
import select
import subprocess
import sys
import threading
import time
import uuid
from pathlib import Path
from typing import Any
from urllib import error as urlerror
from urllib import parse as urlparse
from urllib import request as urlrequest


NO_REPLY = "[[NO_REPLY]]"


def log(message: str) -> None:
    ts = time.strftime("%Y-%m-%d %H:%M:%S")
    print(f"[hiclaw-codex {ts}] {message}", flush=True)


def load_json(path: Path, default: Any) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return default


def save_json(path: Path, data: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
    tmp.replace(path)


class MatrixClient:
    def __init__(self, homeserver: str, access_token: str) -> None:
        self.homeserver = homeserver.rstrip("/")
        self.access_token = access_token

    def _request(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | None = None,
        timeout: int = 90,
    ) -> dict[str, Any]:
        data = None
        headers = {
            "Authorization": f"Bearer {self.access_token}",
            "Accept": "application/json",
        }
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        req = urlrequest.Request(
            self.homeserver + path,
            data=data,
            headers=headers,
            method=method,
        )
        try:
            with urlrequest.urlopen(req, timeout=timeout) as resp:
                raw = resp.read()
        except urlerror.HTTPError as exc:
            raw = exc.read()
            detail = raw.decode("utf-8", errors="replace")
            raise RuntimeError(f"Matrix HTTP {exc.code} {path}: {detail}") from exc
        except Exception as exc:
            raise RuntimeError(f"Matrix request failed {method} {path}: {exc}") from exc

        if not raw:
            return {}
        return json.loads(raw.decode("utf-8"))

    def whoami(self) -> str:
        data = self._request("GET", "/_matrix/client/v3/account/whoami")
        user_id = data.get("user_id", "")
        if not user_id:
            raise RuntimeError("Matrix whoami returned no user_id")
        return user_id

    def sync(self, since: str | None, timeout_ms: int = 30000) -> dict[str, Any]:
        params = {"timeout": str(timeout_ms)}
        if since:
            params["since"] = since
        query = urlparse.urlencode(params)
        return self._request("GET", f"/_matrix/client/v3/sync?{query}", timeout=max(90, timeout_ms // 1000 + 30))

    def joined_members_count(self, room_id: str) -> int:
        encoded = urlparse.quote(room_id, safe="")
        data = self._request("GET", f"/_matrix/client/v3/rooms/{encoded}/joined_members")
        joined = data.get("joined", {})
        if isinstance(joined, dict):
            return len(joined)
        return 0

    def send_text(self, room_id: str, body: str, mentions: list[str] | None = None) -> None:
        encoded = urlparse.quote(room_id, safe="")
        txn_id = f"hiclaw-codex-{uuid.uuid4().hex}"
        payload: dict[str, Any] = {
            "msgtype": "m.text",
            "body": body,
        }
        if mentions:
            payload["m.mentions"] = {"user_ids": mentions}
        self._request(
            "PUT",
            f"/_matrix/client/v3/rooms/{encoded}/send/m.room.message/{txn_id}",
            body=payload,
        )

    def join_room(self, room_id: str) -> None:
        encoded = urlparse.quote(room_id, safe="")
        self._request(
            "POST",
            f"/_matrix/client/v3/rooms/{encoded}/join",
            body={},
        )

    def set_typing(
        self,
        room_id: str,
        user_id: str,
        typing: bool,
        timeout_ms: int = 30000,
    ) -> None:
        encoded_room = urlparse.quote(room_id, safe="")
        encoded_user = urlparse.quote(user_id, safe="")
        payload: dict[str, Any] = {"typing": typing}
        if typing:
            payload["timeout"] = timeout_ms
        self._request(
            "PUT",
            f"/_matrix/client/v3/rooms/{encoded_room}/typing/{encoded_user}",
            body=payload,
        )


class TypingPulse:
    def __init__(
        self,
        matrix: MatrixClient,
        room_id: str,
        user_id: str,
        *,
        timeout_ms: int = 30000,
        interval_min: float = 20.0,
        interval_max: float = 25.0,
    ) -> None:
        self.matrix = matrix
        self.room_id = room_id
        self.user_id = user_id
        self.timeout_ms = timeout_ms
        self.interval_min = interval_min
        self.interval_max = interval_max
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None

    def _set_typing(self, typing: bool) -> None:
        try:
            self.matrix.set_typing(
                self.room_id,
                self.user_id,
                typing,
                timeout_ms=self.timeout_ms,
            )
        except Exception as exc:
            log(f"room {self.room_id}: failed to set typing={typing}: {exc}")

    def _run(self) -> None:
        while not self._stop.wait(random.uniform(self.interval_min, self.interval_max)):
            self._set_typing(True)

    def start(self) -> None:
        self._set_typing(True)
        self._thread = threading.Thread(target=self._run, daemon=True)
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()
        if self._thread is not None:
            self._thread.join(timeout=2)
        self._set_typing(False)


class CodexRunResult:
    def __init__(self, thread_id: str, text: str) -> None:
        self.thread_id = thread_id
        self.text = text


class CodexRunner:
    def __init__(
        self,
        workspace: Path,
        model: str,
        system_prompt: str,
        code_home: Path,
        timeout_seconds: int,
    ) -> None:
        self.workspace = workspace
        self.model = model
        self.system_prompt = system_prompt
        self.code_home = code_home
        self.timeout_seconds = timeout_seconds
        self.code_home.mkdir(parents=True, exist_ok=True)
        self._seed_codex_home()
        self._proc: subprocess.Popen[str] | None = None
        self._stderr_queue: queue.Queue[str | None] | None = None
        self._next_id = 0
        self._initialized = False
        self._lock = threading.Lock()

    def _sandbox_mode(self) -> str:
        mode = os.environ.get("HICLAW_CODEX_SANDBOX", "danger-full-access").strip()
        if mode not in {"read-only", "workspace-write", "danger-full-access"}:
            return "danger-full-access"
        return mode

    def _sandbox_policy(self) -> dict[str, Any]:
        mode = self._sandbox_mode()
        if mode == "read-only":
            return {
                "type": "readOnly",
                "networkAccess": True,
            }
        if mode == "workspace-write":
            return {
                "type": "workspaceWrite",
                "networkAccess": True,
                "writableRoots": [str(self.workspace)],
            }
        return {"type": "dangerFullAccess"}

    def _seed_codex_home(self) -> None:
        shared_home = Path(os.environ.get("HICLAW_CODEX_SHARED_HOME", "/root/.codex-host"))
        if shared_home.exists():
            for name in ("auth.json",):
                src = shared_home / name
                dst = self.code_home / name
                if src.exists() and not dst.exists():
                    try:
                        os.symlink(src, dst)
                    except FileExistsError:
                        pass
            for name in ("config.json", "config.toml", "instructions.md"):
                src = shared_home / name
                dst = self.code_home / name
                if src.exists() and not dst.exists():
                    dst.write_bytes(src.read_bytes())

        workspace_skills = self.workspace / "skills"
        codex_skills = self.code_home / "skills"
        if workspace_skills.is_dir():
            if codex_skills.is_symlink() or codex_skills.exists():
                try:
                    if codex_skills.resolve() == workspace_skills.resolve():
                        return
                except Exception:
                    pass
                if codex_skills.is_symlink() or codex_skills.is_file():
                    codex_skills.unlink(missing_ok=True)
            if not codex_skills.exists():
                try:
                    os.symlink(workspace_skills, codex_skills)
                except FileExistsError:
                    pass

    def _start_server(self) -> None:
        env = os.environ.copy()
        env["CODEX_HOME"] = str(self.code_home)

        cmd = [
            "codex",
            "app-server",
            "--listen",
            "stdio://",
        ]
        proc = subprocess.Popen(
            cmd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
            cwd=str(self.workspace),
            env=env,
        )
        assert proc.stdin is not None
        assert proc.stdout is not None
        assert proc.stderr is not None

        stderr_queue: queue.Queue[str | None] = queue.Queue()

        def _drain_stderr() -> None:
            for line in proc.stderr:
                stderr_queue.put(line.rstrip())
            stderr_queue.put(None)

        stderr_thread = threading.Thread(target=_drain_stderr, daemon=True)
        stderr_thread.start()
        self._proc = proc
        self._stderr_queue = stderr_queue
        self._next_id = 0
        self._initialized = False
        log("codex app-server started")

    def _stop_server(self) -> None:
        proc = self._proc
        self._proc = None
        self._stderr_queue = None
        self._initialized = False
        self._next_id = 0
        if proc is None:
            return
        try:
            if proc.stdin is not None:
                proc.stdin.close()
        except Exception:
            pass
        try:
            proc.terminate()
        except Exception:
            pass
        try:
            proc.wait(timeout=10)
        except Exception:
            try:
                proc.kill()
            except Exception:
                pass

    def _server_exited(self) -> bool:
        return self._proc is None or self._proc.poll() is not None

    def _send_message(self, payload: dict[str, Any]) -> None:
        proc = self._proc
        if proc is None or proc.stdin is None:
            raise RuntimeError("Codex app-server is not running")
        try:
            proc.stdin.write(json.dumps(payload) + "\n")
            proc.stdin.flush()
        except Exception as exc:
            raise RuntimeError(f"Codex stdin write failed: {exc}") from exc

    def _read_message(self, timeout: float | None = None) -> dict[str, Any] | None:
        proc = self._proc
        if proc is None or proc.stdout is None:
            return None

        deadline = None if timeout is None else time.time() + timeout
        while True:
            try:
                while True:
                    if self._stderr_queue is None:
                        break
                    line = self._stderr_queue.get_nowait()
                    if line is None:
                        break
                    log(f"codex stderr: {line}")
            except queue.Empty:
                pass

            if deadline is None:
                line = proc.stdout.readline()
            else:
                remaining = deadline - time.time()
                if remaining <= 0:
                    return None
                ready, _, _ = select.select([proc.stdout], [], [], remaining)
                if not ready:
                    return None
                line = proc.stdout.readline()
            if not line:
                return None
            line = line.strip()
            if not line:
                continue
            try:
                return json.loads(line)
            except json.JSONDecodeError:
                log(f"ignored invalid codex JSON line: {line[:200]}")

    def _handle_server_request(self, msg: dict[str, Any]) -> None:
        req_id = msg.get("id")
        method = msg.get("method", "")
        if method in {
            "item/commandExecution/requestApproval",
            "item/fileChange/requestApproval",
            "item/permissions/requestApproval",
            "execCommandApproval",
            "applyPatchApproval",
        }:
            result: Any = {"decision": "accept"}
        else:
            result = {}
        self._send_message({"jsonrpc": "2.0", "id": req_id, "result": result})

    def _request(
        self,
        method: str,
        params: dict[str, Any],
        notification_handler: Any | None = None,
    ) -> Any:
        self._next_id += 1
        req_id = self._next_id
        self._send_message(
            {
                "jsonrpc": "2.0",
                "id": req_id,
                "method": method,
                "params": params,
            }
        )

        while True:
            msg = self._read_message()
            if msg is None:
                raise RuntimeError(f"Codex exited while waiting for {method}")

            if "id" in msg and "method" in msg:
                self._handle_server_request(msg)
                continue

            if msg.get("id") == req_id and ("result" in msg or "error" in msg):
                if "error" in msg:
                    raise RuntimeError(f"{method} failed: {msg['error']}")
                return msg.get("result")

            if notification_handler is not None and "method" in msg:
                notification_handler(msg["method"], msg.get("params", {}))

    def _initialize_server(self) -> None:
        if self._initialized:
            return
        self._request(
            "initialize",
            {
                "clientInfo": {
                    "name": "hiclaw-codex-runtime",
                    "title": "HiClaw Codex Runtime",
                    "version": "0.1.0",
                },
                "capabilities": {"experimentalApi": True},
            },
        )
        self._send_message({"jsonrpc": "2.0", "method": "initialized"})
        self._initialized = True

    def _ensure_server(self) -> None:
        if self._server_exited():
            self._stop_server()
            self._start_server()
        self._initialize_server()

    def _drain_notifications(
        self,
        notification_handler: Any | None = None,
        *,
        idle_timeout: float = 0.05,
        max_total: float = 0.5,
    ) -> None:
        deadline = time.time() + max_total
        while time.time() < deadline:
            msg = self._read_message(timeout=idle_timeout)
            if msg is None:
                return
            if "id" in msg and "method" in msg:
                self._handle_server_request(msg)
                continue
            if notification_handler is not None and "method" in msg:
                notification_handler(msg["method"], msg.get("params", {}))

    def _should_restart_after_error(self, exc: Exception) -> bool:
        if self._server_exited():
            return True
        msg = str(exc)
        return "Codex exited while waiting" in msg or "Codex stdin write failed" in msg

    def _run_turn_locked(self, prompt: str, thread_id: str | None) -> CodexRunResult:
        done = False
        final_texts: list[str] = []
        fallback_texts: list[str] = []
        message_buffers: dict[str, str] = {}
        current_thread_id = thread_id or ""
        completed_turn_ids: set[str] = set()
        active_turn_id = ""

        def extract_nested(source: dict[str, Any], *keys: str) -> str:
            cur: Any = source
            for key in keys:
                if not isinstance(cur, dict):
                    return ""
                cur = cur.get(key)
            return cur if isinstance(cur, str) else ""

        def handle_item_notification(method: str, params: dict[str, Any]) -> None:
            nonlocal done
            if method == "item/agentMessage/delta":
                item_id = params.get("itemId")
                delta = params.get("delta")
                if isinstance(item_id, str) and isinstance(delta, str):
                    message_buffers[item_id] = message_buffers.get(item_id, "") + delta
                return

            item = params.get("item")
            if not isinstance(item, dict):
                return
            item_type = item.get("type")
            if method == "item/completed" and item_type == "agentMessage":
                item_id = item.get("id")
                text = item.get("text")
                if (not isinstance(text, str) or not text) and isinstance(item_id, str):
                    text = message_buffers.get(item_id, "")
                if isinstance(text, str) and text:
                    phase = item.get("phase")
                    if phase == "final_answer":
                        final_texts.append(text)
                    elif phase in {"", None}:
                        fallback_texts.append(text)
                if isinstance(item_id, str):
                    message_buffers.pop(item_id, None)
                if item.get("phase") == "final_answer":
                    done = True

        def handle_notification(method: str, params: dict[str, Any]) -> None:
            nonlocal done, current_thread_id
            if method == "thread/started":
                thread = params.get("thread")
                if isinstance(thread, dict):
                    current_thread_id = thread.get("id", current_thread_id)
                return
            if method == "turn/completed":
                turn_id = extract_nested(params, "turn", "id")
                if not active_turn_id:
                    return
                if not turn_id or turn_id != active_turn_id:
                    return
                if turn_id in completed_turn_ids:
                    return
                completed_turn_ids.add(turn_id)
                done = True
                return
            if method == "thread/status/changed":
                if not active_turn_id:
                    return
                if extract_nested(params, "status", "type") == "idle":
                    done = True
                return
            if method.startswith("item/"):
                handle_item_notification(method, params)

        self._drain_notifications()

        resume_ok = False
        if thread_id:
            try:
                self._request(
                    "thread/resume",
                    {
                        "threadId": thread_id,
                        "cwd": str(self.workspace),
                        "developerInstructions": self.system_prompt,
                        "model": self.model,
                        "approvalPolicy": "never",
                        "sandbox": self._sandbox_mode(),
                        "personality": "pragmatic",
                    },
                    notification_handler=handle_notification,
                )
                current_thread_id = thread_id
                resume_ok = True
            except Exception as exc:
                log(f"codex thread resume failed, starting fresh: {exc}")

        if not resume_ok:
            result = self._request(
                "thread/start",
                {
                    "cwd": str(self.workspace),
                    "developerInstructions": self.system_prompt,
                    "model": self.model,
                    "approvalPolicy": "never",
                    "sandbox": self._sandbox_mode(),
                    "personality": "pragmatic",
                },
                notification_handler=handle_notification,
            )
            if isinstance(result, dict):
                thread = result.get("thread", {})
                if isinstance(thread, dict):
                    current_thread_id = thread.get("id", current_thread_id)

        if not current_thread_id:
            raise RuntimeError("Codex did not return a thread id")

        turn_result = self._request(
            "turn/start",
            {
                "threadId": current_thread_id,
                "cwd": str(self.workspace),
                "input": [{"type": "text", "text": prompt}],
                "model": self.model,
                "approvalPolicy": "never",
                "sandboxPolicy": self._sandbox_policy(),
                "personality": "pragmatic",
            },
            notification_handler=handle_notification,
        )
        if isinstance(turn_result, dict):
            turn = turn_result.get("turn", {})
            if isinstance(turn, dict):
                active_turn_id = turn.get("id", "")
        done = False

        deadline = time.time() + self.timeout_seconds
        while not done and time.time() < deadline:
            msg = self._read_message()
            if msg is None:
                break
            if "id" in msg and "method" in msg:
                self._handle_server_request(msg)
            elif "method" in msg:
                handle_notification(msg["method"], msg.get("params", {}))

        if not done:
            raise RuntimeError(f"Codex turn timed out after {self.timeout_seconds}s")

        self._drain_notifications(handle_notification)
        source_texts = final_texts or fallback_texts
        text = ""
        for part in reversed(source_texts):
            part = part.strip()
            if part:
                text = part
                break
        return CodexRunResult(current_thread_id, text)

    def run_turn(self, prompt: str, thread_id: str | None) -> CodexRunResult:
        with self._lock:
            self._ensure_server()
            try:
                return self._run_turn_locked(prompt, thread_id)
            except Exception as exc:
                if not self._should_restart_after_error(exc):
                    raise
                log(f"codex app-server failed, restarting once: {exc}")
                self._stop_server()
                self._ensure_server()
                return self._run_turn_locked(prompt, thread_id)


class HiClawCodexAgent:
    def __init__(self, workspace: Path, role: str, timeout_seconds: int) -> None:
        self.workspace = workspace
        self.role = role
        self.timeout_seconds = timeout_seconds
        self.config_path = workspace / "openclaw.json"
        self.config_mtime = 0.0
        self.state_path = workspace / ".codex-agent" / "state.json"
        self.ready_path = workspace / ".codex-agent" / "ready"
        self.state = load_json(
            self.state_path,
            {
                "since": None,
                "rooms": {},
            },
        )
        self.room_threads: dict[str, str] = {}
        self.config: dict[str, Any] = {}
        self.homeserver = ""
        self.access_token = ""
        self.model = "gpt-5.4"
        self.matrix: MatrixClient | None = None
        self.user_id = ""
        self.localpart = ""
        self.dm_allow: set[str] = set()
        self.group_allow: set[str] = set()
        self.groups_cfg: dict[str, Any] = {}
        self.expected_room_id = os.environ.get("HICLAW_WORKER_ROOM_ID", "").strip() if role == "worker" else ""
        # Prompt caching state
        self._prompt_file_hashes: dict[str, str] = {}
        self._full_system_prompt: str = ""
        self._condensed_system_prompt: str = ""
        self._prompt_turn_count: int = 0
        self.CONDENSED_THRESHOLD = 6000
        self.FULL_PROMPT_INTERVAL = 10
        # Router state
        self._router_model = os.environ.get("HICLAW_CODEX_ROUTER_MODEL", "gpt-5-nano").strip()
        self._router_timeout = int(os.environ.get("HICLAW_CODEX_ROUTER_TIMEOUT", "20"))
        self._router_gateway_url = ""
        self._router_gateway_key = ""
        self.system_prompt = self._load_system_prompt()
        self._full_system_prompt = self.system_prompt
        self._condensed_system_prompt = self._condense_system_prompt(self.system_prompt)
        self._prompt_file_hashes = {
            name: self._hash_file(self.workspace / name)
            for name in ("AGENTS.md", "SOUL.md", "TOOLS.md")
        }
        self._sanitize_state()
        self._reload_config(force=True)
        self.runner = CodexRunner(
            workspace=workspace,
            model=self.model,
            system_prompt=self.system_prompt,
            code_home=workspace / ".codex-home",
            timeout_seconds=timeout_seconds,
        )

    def _reload_config(self, force: bool = False) -> None:
        try:
            stat = self.config_path.stat()
        except FileNotFoundError as exc:
            raise RuntimeError(f"Missing {self.config_path}") from exc

        if not force and stat.st_mtime <= self.config_mtime:
            return

        config = load_json(self.config_path, {})
        if not config:
            raise RuntimeError(f"Missing or invalid {self.config_path}")

        matrix_cfg = config.get("channels", {}).get("matrix", {})
        homeserver = matrix_cfg.get("homeserver", "")
        access_token = matrix_cfg.get("accessToken", "")
        if not homeserver or not access_token:
            raise RuntimeError("Matrix homeserver/accessToken missing in openclaw.json")

        primary = (
            config.get("agents", {})
            .get("defaults", {})
            .get("model", {})
            .get("primary", "")
        )
        model = os.environ.get("HICLAW_DEFAULT_MODEL", "")
        if not model and "/" in primary:
            model = primary.split("/", 1)[1]
        if not model:
            model = "gpt-5.4"

        self.config = config
        self.config_mtime = stat.st_mtime
        self.homeserver = homeserver
        self.access_token = access_token
        self.model = model
        self.dm_allow = set(matrix_cfg.get("dm", {}).get("allowFrom", []))
        self.group_allow = set(matrix_cfg.get("groupAllowFrom", []))
        groups_cfg = matrix_cfg.get("groups", {})
        self.groups_cfg = groups_cfg if isinstance(groups_cfg, dict) else {}
        if hasattr(self, "runner"):
            self.runner.model = model

        # Resolve AI gateway URL/key for lightweight router
        gw_domain = os.environ.get("HICLAW_AI_GATEWAY_DOMAIN", "aigw-local.hiclaw.io").strip()
        gw_url = os.environ.get("HICLAW_AI_GATEWAY_URL", "").strip()
        if not gw_url and gw_domain:
            gw_url = f"http://{gw_domain}:8080/v1/chat/completions"
        gw_key = os.environ.get("HICLAW_MANAGER_GATEWAY_KEY", "").strip()
        if not gw_key:
            providers = config.get("models", {}).get("providers", {})
            for prov in providers.values():
                if isinstance(prov, dict) and prov.get("apiKey"):
                    gw_key = prov["apiKey"]
                    break
        self._router_gateway_url = gw_url
        self._router_gateway_key = gw_key

        if self.matrix is None or self.matrix.homeserver != homeserver or self.matrix.access_token != access_token:
            self.matrix = MatrixClient(homeserver, access_token)
            self.user_id = self.matrix.whoami()
            self.localpart = self.user_id.split(":", 1)[0]
        else:
            self.matrix.homeserver = homeserver
            self.matrix.access_token = access_token

    def _load_system_prompt(self) -> str:
        sections: list[str] = []
        for name in ("AGENTS.md", "SOUL.md", "TOOLS.md"):
            path = self.workspace / name
            if path.is_file():
                sections.append(f"# {name}\n\n{path.read_text(encoding='utf-8')}")
        sections.append(
            "You are running inside HiClaw as a Matrix bot backed by Codex.\n"
            "Reply with the exact Matrix message body only.\n"
            f"If no reply is needed, return exactly {NO_REPLY}.\n"
            "Never emit commentary, progress updates, or tool-call narration.\n"
            "Do not simulate commentary-channel messages.\n"
            "Use the files and shell scripts available in the current workspace to perform work.\n"
            "Do not mention hidden implementation details unless the room explicitly asks.\n"
            "Prefer concise replies."
        )
        return "\n\n".join(sections)

    @staticmethod
    def _hash_file(path: Path) -> str:
        try:
            return hashlib.sha256(path.read_bytes()).hexdigest()
        except Exception:
            return ""

    _CONDENSE_MAP: dict[str, str] = {
        # AGENTS.md — Manager
        "Host File Access Permissions": "Never access host files without explicit admin permission.",
        "Every Session": "Read SOUL.md and today's memory file each session. In DM also read MEMORY.md.",
        "MinIO Storage": "Use ${HICLAW_STORAGE_PREFIX} for mc commands. Never hardcode paths.",
        "Memory": "Update memory files after significant events.",
        "Write It Down": "",
        "MEMORY.md — Long-Term Memory": "",
        "Tools": "Check each skill's SKILL.md for tool usage.",
        "Management Skills": "See TOOLS.md for skill routing.",
        "Worker Unresponsiveness": "Worker timeout is 30 min.",
        "Heartbeat": "Follow HEARTBEAT.md. Batch checks; use cron for exact schedules.",
        "Heartbeat vs Cron": "",
        # AGENTS.md — Worker
        "Communication": "Use @mentions with full Matrix ID for all group-room communication.",
        "Task Execution": "Follow task workflow: sync, read spec, plan, execute, push, report.",
        "Task Directory Structure": "",
        "plan.md Template": "",
        "Skills": "Skills in skills/. Read SKILL.md before use. Builtins are read-only.",
        # SOUL.md
        "AI Identity": "You and Workers are AI agents. No rest needed. Use specific time units.",
        "About Yourself": "",
        "About Workers": "",
        "Task Management": "",
        "Identity & Personality": "",
        "Core Nature": "Delegate to Workers. Only do management-skill work yourself.",
        # TOOLS.md
        "Skill Boundary": "worker-management for lifecycle; hiclaw-find-worker for Nacos import.",
        "Cross-Skill Combos": "Load related skills together for multi-skill workflows. See TOOLS.md.",
    }

    # Sections whose content MUST be kept in full (critical operational rules).
    _KEEP_SECTIONS: set[str] = {
        "Gotchas",
        "@Mention Protocol",
        "When to Speak",
        "NO_REPLY — Correct Usage",
        "NO_REPLY",
        "Safety",
        "Security Rules",
        "Mandatory Routing",
        "Group Rooms",
        "Incoming Message Format",
    }

    def _condense_system_prompt(self, full_prompt: str) -> str:
        if len(full_prompt) <= self.CONDENSED_THRESHOLD:
            return full_prompt

        lines = full_prompt.split("\n")
        out: list[str] = []
        skipping = False
        current_h2 = ""

        for line in lines:
            stripped = line.strip()

            # Detect H1/H2/H3 headers
            if stripped.startswith("## "):
                section_name = stripped[3:].strip()
                current_h2 = section_name
                skipping = False

                if section_name in self._KEEP_SECTIONS:
                    out.append(line)
                    continue

                replacement = self._CONDENSE_MAP.get(section_name)
                if replacement is not None:
                    if replacement:
                        out.append(line)
                        out.append("")
                        out.append(replacement)
                        out.append("")
                    # Empty replacement means skip entirely (sub-section of an
                    # already-condensed parent).
                    skipping = True
                    continue

                # Unknown section: keep in full
                out.append(line)
                continue

            if stripped.startswith("### "):
                subsection = stripped[4:].strip()
                # If parent H2 is being skipped, skip sub-sections too —
                # unless the sub-section itself is in the keep-set.
                if skipping and subsection not in self._KEEP_SECTIONS:
                    continue
                skipping = False
                out.append(line)
                continue

            if stripped.startswith("# "):
                # H1 resets everything
                current_h2 = ""
                skipping = False
                out.append(line)
                continue

            if skipping:
                continue

            out.append(line)

        return "\n".join(out)

    def _refresh_system_prompt(self) -> None:
        files_changed = False
        for name in ("AGENTS.md", "SOUL.md", "TOOLS.md"):
            new_hash = self._hash_file(self.workspace / name)
            if new_hash != self._prompt_file_hashes.get(name, ""):
                files_changed = True
                self._prompt_file_hashes[name] = new_hash

        if files_changed:
            self._full_system_prompt = self._load_system_prompt()
            self._condensed_system_prompt = self._condense_system_prompt(
                self._full_system_prompt
            )
            self._prompt_turn_count = 0
            log("system prompt reloaded (file change detected)")

        self._prompt_turn_count += 1

        use_full = (
            self._prompt_turn_count == 1
            or self._prompt_turn_count % self.FULL_PROMPT_INTERVAL == 0
            or len(self._full_system_prompt) <= self.CONDENSED_THRESHOLD
        )

        self.system_prompt = (
            self._full_system_prompt if use_full else self._condensed_system_prompt
        )
        if hasattr(self, "runner"):
            self.runner.system_prompt = self.system_prompt

    def _room_state(self, room_id: str) -> dict[str, Any]:
        rooms = self.state.setdefault("rooms", {})
        return rooms.setdefault(
            room_id,
            {
                "last_ts": 0,
                "room_type": "unknown",
            },
        )

    def _sanitize_state(self) -> None:
        rooms = self.state.get("rooms", {})
        if not isinstance(rooms, dict):
            self.state["rooms"] = {}
            return

        removed_threads = 0
        for value in rooms.values():
            if not isinstance(value, dict):
                continue
            if value.pop("thread_id", None):
                removed_threads += 1

        if removed_threads:
            log(f"cleared {removed_threads} persisted Codex thread id(s) from state.json")
            self._save_state()

    def _save_state(self) -> None:
        save_json(self.state_path, self.state)

    def _ensure_ready_file(self) -> None:
        self.ready_path.parent.mkdir(parents=True, exist_ok=True)
        self.ready_path.write_text("ok\n", encoding="utf-8")

    def _ensure_expected_room_joined(self) -> None:
        if self.role != "worker" or not self.expected_room_id or self.matrix is None:
            return

        state = self._room_state(self.expected_room_id)
        try:
            count = self.matrix.joined_members_count(self.expected_room_id)
        except Exception:
            count = 0

        if count > 0:
            room_type = "dm" if count == 2 else "group"
            if state.get("room_type") != room_type:
                log(
                    f"worker expected room {self.expected_room_id}: "
                    f"room_type updated {state.get('room_type', 'unknown')} -> {room_type} "
                    f"(joined members: {count})"
                )
            state["room_type"] = room_type
            return

        try:
            self.matrix.join_room(self.expected_room_id)
            count = self.matrix.joined_members_count(self.expected_room_id)
        except Exception as exc:
            log(f"worker failed to join expected room {self.expected_room_id}: {exc}")
            return

        room_type = "dm" if count == 2 else "group"
        state["room_type"] = room_type
        log(f"worker joined expected room {self.expected_room_id} (joined members: {count})")

    def _determine_room_type(self, room_id: str, state: dict[str, Any]) -> str:
        cached_room_type = state.get("room_type", "unknown")
        try:
            count = self.matrix.joined_members_count(room_id)
        except Exception as exc:
            if cached_room_type in {"dm", "group"}:
                log(
                    f"room member lookup failed for {room_id}, "
                    f"reusing cached room_type={cached_room_type}: {exc}"
                )
                return cached_room_type
            log(f"room member lookup failed for {room_id}: {exc}")
            return "group"

        room_type = "dm" if count == 2 else "group"
        if room_type != cached_room_type:
            log(f"room {room_id}: room_type updated {cached_room_type} -> {room_type} (joined members: {count})")
        state["room_type"] = room_type
        return room_type

    def _message_body(self, event: dict[str, Any]) -> str:
        content = event.get("content", {})
        if not isinstance(content, dict):
            return ""
        if content.get("msgtype") != "m.text":
            return ""
        body = content.get("body", "")
        return body if isinstance(body, str) else ""

    def _extract_mentions(self, content: dict[str, Any], body: str) -> set[str]:
        mentions: set[str] = set()
        raw_mentions = content.get("m.mentions", {})
        if isinstance(raw_mentions, dict):
            for user_id in raw_mentions.get("user_ids", []) or []:
                if isinstance(user_id, str):
                    mentions.add(user_id)
        if self.user_id in body:
            mentions.add(self.user_id)
        if self.localpart in body:
            mentions.add(self.user_id)
        return mentions

    def _should_trigger(self, room_type: str, event: dict[str, Any], body: str) -> bool:
        sender = event.get("sender", "")
        if not isinstance(sender, str):
            return False
        content = event.get("content", {})
        if not isinstance(content, dict):
            return False

        if room_type == "dm":
            return sender in self.dm_allow

        if sender not in self.group_allow:
            return False

        room_rule = self.groups_cfg.get(event.get("room_id", ""), {})
        if not isinstance(room_rule, dict):
            room_rule = {}
        default_rule = self.groups_cfg.get("*", {})
        if not isinstance(default_rule, dict):
            default_rule = {}
        require_mention = room_rule.get("requireMention")
        if require_mention is None:
            require_mention = default_rule.get("requireMention", True)
        if not require_mention:
            return True
        mentions = self._extract_mentions(content, body)
        return self.user_id in mentions

    def _has_explicit_self_mention(self, event: dict[str, Any], body: str) -> bool:
        content = event.get("content", {})
        if not isinstance(content, dict):
            return False
        mentions = self._extract_mentions(content, body)
        return self.user_id in mentions

    def _router_should_reply(
        self,
        room_id: str,
        room_type: str,
        events: list[dict[str, Any]],
    ) -> bool:
        """Use a cheap model via AI Gateway to decide if a reply is needed.

        Returns True (proceed with full Codex turn) or False (skip).
        Fails open: any error → True.
        """
        if not self._router_gateway_url or not self._router_gateway_key:
            return True

        # Collect the last 3 message bodies
        recent: list[str] = []
        for event in events[-3:]:
            sender = event.get("sender", "")
            body = self._message_body(event)
            if body:
                recent.append(f"- {sender}: {body}")
        if not recent:
            return True

        prompt_text = (
            "You are a routing filter for a Matrix chat bot.\n"
            "Recent messages in a group room:\n"
            + "\n".join(recent)
            + "\n\n"
            "Does the last message require the bot to produce a substantive response?\n"
            "Messages like acknowledgments, thanks, farewells, emoji-only, or status\n"
            "updates needing no action → NO.\n"
            "Questions, task assignments, requests for information, error reports,\n"
            "or messages that need action → YES.\n"
            "Reply with exactly one word: YES or NO"
        )

        payload = json.dumps(
            {
                "model": self._router_model,
                "messages": [{"role": "user", "content": prompt_text}],
                "max_tokens": 3,
                "temperature": 0,
            }
        ).encode("utf-8")

        req = urlrequest.Request(
            self._router_gateway_url,
            data=payload,
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {self._router_gateway_key}",
            },
            method="POST",
        )

        try:
            with urlrequest.urlopen(req, timeout=self._router_timeout) as resp:
                result = json.loads(resp.read().decode("utf-8"))
            answer = (
                result.get("choices", [{}])[0]
                .get("message", {})
                .get("content", "")
                .strip()
                .upper()
            )
            if answer == "NO":
                log(f"room {room_id}: router({self._router_model}) decided no reply needed")
                return False
            return True
        except Exception as exc:
            log(f"room {room_id}: router call failed (fail-open): {exc}")
            return True

    def _build_prompt(self, room_id: str, room_type: str, events: list[dict[str, Any]]) -> str:
        lines = [
            f"Role: {self.role}",
            f"Matrix room id: {room_id}",
            f"Room type: {room_type}",
            f"Your Matrix user id: {self.user_id}",
            "",
            "New chat messages to handle:",
        ]
        for event in events:
            sender = event.get("sender", "")
            ts = event.get("origin_server_ts", 0)
            body = self._message_body(event)
            lines.append(f"- [{ts}] {sender}: {body}")
        lines.extend(
            [
                "",
                "Instructions:",
                f"- If you should stay silent, reply with exactly {NO_REPLY}.",
                "- Otherwise reply with the exact message body to send back to this Matrix room.",
                "- Keep the reply concise unless the room explicitly asks for detail.",
                "- Use the same language as the latest user message.",
            ]
        )
        return "\n".join(lines)

    def _mentions_from_reply(self, reply: str) -> list[str]:
        matches = set(re.findall(r"@[A-Za-z0-9._=-]+:[A-Za-z0-9._:-]+", reply))
        return sorted(matches)

    def process_room(self, room_id: str, events: list[dict[str, Any]]) -> None:
        self._refresh_system_prompt()
        state = self._room_state(room_id)
        last_ts = int(state.get("last_ts", 0) or 0)
        room_type = self._determine_room_type(room_id, state)

        fresh_events = []
        trigger = False
        explicit_mention = False
        max_ts = last_ts

        for event in events:
            if event.get("type") != "m.room.message":
                continue
            if event.get("sender") == self.user_id:
                continue
            ts = int(event.get("origin_server_ts", 0) or 0)
            max_ts = max(max_ts, ts)
            if ts <= last_ts:
                continue
            body = self._message_body(event)
            if not body:
                continue
            event["room_id"] = room_id
            fresh_events.append(event)
            if self._should_trigger(room_type, event, body):
                trigger = True
                if room_type == "group" and self._has_explicit_self_mention(event, body):
                    explicit_mention = True

        if max_ts > last_ts:
            state["last_ts"] = max_ts

        if not fresh_events or not trigger:
            return

        # Lightweight router: for group rooms, ask a cheap model if reply is needed
        if room_type == "group":
            if explicit_mention:
                log(f"room {room_id}: explicit @mention detected, skipping router")
            elif not self._router_should_reply(room_id, room_type, fresh_events):
                return

        prompt = self._build_prompt(room_id, room_type, fresh_events)
        log(f"handling room {room_id} with {len(fresh_events)} new message(s)")
        prior_thread_id = self.room_threads.get(room_id) or None
        typing_pulse = TypingPulse(self.matrix, room_id, self.user_id)
        typing_pulse.start()
        try:
            try:
                result = self.runner.run_turn(prompt, prior_thread_id)
            except Exception as exc:
                log(f"codex turn failed for {room_id}: {exc}")
                return

            reply = result.text.strip()
            should_retry_fresh = False
            if not reply and prior_thread_id:
                should_retry_fresh = True
            elif reply == NO_REPLY and prior_thread_id and room_type == "dm":
                should_retry_fresh = True

            if should_retry_fresh:
                log(f"room {room_id}: resumed thread returned no usable reply, retrying with a fresh thread")
                try:
                    result = self.runner.run_turn(prompt, None)
                except Exception as exc:
                    log(f"codex retry failed for {room_id}: {exc}")
                    return
                reply = result.text.strip()

            self.room_threads[room_id] = result.thread_id
            if not reply or reply == NO_REPLY:
                log(f"room {room_id}: no reply")
                return

            mentions = self._mentions_from_reply(reply)
            self.matrix.send_text(room_id, reply, mentions=mentions or None)
            log(f"room {room_id}: reply sent")
        finally:
            typing_pulse.stop()

    def run_forever(self) -> None:
        self._reload_config(force=True)
        self._ensure_expected_room_joined()
        if not self.state.get("since"):
            log("performing catch-up sync (old messages suppressed)")
            assert self.matrix is not None
            data = self.matrix.sync(None, timeout_ms=0)
            self.state["since"] = data.get("next_batch")
            self._save_state()

        self._ensure_ready_file()
        while True:
            self._reload_config()
            self._ensure_expected_room_joined()
            assert self.matrix is not None
            data = self.matrix.sync(self.state.get("since"), timeout_ms=30000)
            self.state["since"] = data.get("next_batch")
            joined = data.get("rooms", {}).get("join", {})
            if isinstance(joined, dict):
                for room_id, room_data in joined.items():
                    timeline = room_data.get("timeline", {}).get("events", [])
                    if isinstance(timeline, list) and timeline:
                        self.process_room(room_id, timeline)
            self._save_state()


def main() -> int:
    parser = argparse.ArgumentParser(description="HiClaw Codex Matrix agent")
    parser.add_argument("--workspace", required=True, help="Agent workspace path")
    parser.add_argument("--role", default="worker", choices=["manager", "worker"], help="Logical agent role")
    parser.add_argument("--timeout-seconds", type=int, default=1800, help="Per-turn Codex timeout")
    args = parser.parse_args()

    workspace = Path(args.workspace).resolve()
    if not workspace.exists():
        raise SystemExit(f"workspace not found: {workspace}")

    agent = HiClawCodexAgent(
        workspace=workspace,
        role=args.role,
        timeout_seconds=args.timeout_seconds,
    )
    log(f"starting Codex Matrix agent for {args.role} at {workspace}")
    agent.run_forever()
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130)
