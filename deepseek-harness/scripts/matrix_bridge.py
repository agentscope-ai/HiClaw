#!/usr/bin/env python3
"""Minimal Matrix channel loop for an AgentTeams-managed DSH worker."""

from __future__ import annotations

import json
import os
from pathlib import Path
import signal
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
import urllib.error
import urllib.parse
import urllib.request
import uuid


STOP = threading.Event()


def text_events(payload: dict[str, Any], watched_rooms: set[str], own_user_id: str) -> list[dict[str, str]]:
    """Extract actionable plain-text Matrix events from watched joined rooms."""
    found: list[dict[str, str]] = []
    joined = payload.get("rooms", {}).get("join", {})
    if not isinstance(joined, dict):
        return found
    for room_id, room in joined.items():
        if room_id not in watched_rooms or not isinstance(room, dict):
            continue
        events = room.get("timeline", {}).get("events", [])
        if not isinstance(events, list):
            continue
        for event in events:
            if not isinstance(event, dict) or event.get("type") != "m.room.message":
                continue
            unsigned = event.get("unsigned")
            is_redacted = isinstance(unsigned, dict) and unsigned.get("redacted_because") is not None
            if event.get("sender") == own_user_id or is_redacted:
                continue
            content = event.get("content")
            if not isinstance(content, dict) or content.get("msgtype") != "m.text":
                continue
            body = str(content.get("body") or "").strip()
            event_id = str(event.get("event_id") or "").strip()
            sender = str(event.get("sender") or "").strip()
            if body and event_id and sender:
                found.append({"room_id": room_id, "event_id": event_id, "sender": sender, "body": body})
    return found


def required_env(name: str) -> str:
    value = os.getenv(name, "").strip()
    if not value:
        raise RuntimeError(f"{name} is required")
    return value


def runtime_section(path: Path, section: str) -> dict[str, str]:
    """Read scalar values from one top-level controller-generated YAML section."""
    values: dict[str, str] = {}
    active = False
    for raw in path.read_text(encoding="utf-8").splitlines():
        if raw == f"{section}:":
            active = True
            continue
        if active and raw and not raw.startswith(" "):
            break
        if not active or not raw.startswith("  ") or raw.startswith("    "):
            continue
        key, separator, value = raw.strip().partition(":")
        if separator:
            values[key] = value.strip().strip("'\"")
    return values


class MatrixClient:
    def __init__(self, homeserver: str, token: str) -> None:
        self.homeserver = homeserver.rstrip("/")
        self.token = token

    def request(self, method: str, path: str, body: dict[str, Any] | None = None, timeout: int = 45) -> dict[str, Any]:
        data = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
        request = urllib.request.Request(
            f"{self.homeserver}{path}",
            data=data,
            method=method,
            headers={"Authorization": f"Bearer {self.token}", "Content-Type": "application/json"},
        )
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return json.loads(response.read().decode("utf-8") or "{}")

    def sync(self, since: str | None, timeout_ms: int) -> dict[str, Any]:
        query: dict[str, str | int] = {"timeout": timeout_ms}
        if since:
            query["since"] = since
        return self.request("GET", f"/_matrix/client/v3/sync?{urllib.parse.urlencode(query)}", timeout=max(10, timeout_ms // 1000 + 10))

    def send_text(self, room_id: str, text: str, reply_to: str | None = None) -> str:
        content: dict[str, Any] = {"msgtype": "m.text", "body": text}
        if reply_to:
            content["m.relates_to"] = {"m.in_reply_to": {"event_id": reply_to}}
        room = urllib.parse.quote(room_id, safe="")
        txn = f"dsh-{uuid.uuid4().hex}"
        result = self.request("PUT", f"/_matrix/client/v3/rooms/{room}/send/m.room.message/{txn}", content)
        return str(result.get("event_id") or "")


def atomic_write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(text, encoding="utf-8")
    temporary.replace(path)


def mc(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["mc", *args], check=check, text=True, capture_output=True)


def push_runtime_state(worker_name: str, state_path: Path) -> None:
    prefix = required_env("AGENTTEAMS_STORAGE_PREFIX").rstrip("/")
    mc("cp", str(state_path), f"{prefix}/agents/{worker_name}/runtime/{state_path.name}")


def sync_sessions(worker_name: str) -> None:
    dsh_home = Path(required_env("DSH_HOME"))
    sessions = dsh_home / "sessions"
    if not sessions.exists():
        return
    prefix = required_env("AGENTTEAMS_STORAGE_PREFIX").rstrip("/")
    mc("mirror", f"{sessions}/", f"{prefix}/agents/{worker_name}/.dsh/sessions/", "--overwrite")


def dsh_error_detail(stderr: str, returncode: int) -> str:
    lines = [line.strip() for line in stderr.splitlines() if line.strip()]
    for line in lines:
        if line.startswith("Error: dsh:") or line.startswith("dsh:"):
            return line
    useful = [line for line in lines if not line.startswith("Node.js v") and not line.startswith("command terminated")]
    return useful[-1] if useful else f"exit {returncode}"


def run_dsh(task: str, workspace: Path, timeout_seconds: int) -> str:
    completed = subprocess.run(
        ["agentteams-dsh", task],
        cwd=workspace,
        text=True,
        capture_output=True,
        timeout=timeout_seconds,
    )
    answer = completed.stdout.strip()
    if completed.returncode != 0:
        detail = dsh_error_detail(completed.stderr, completed.returncode)
        raise RuntimeError(f"DeepSeek Harness task failed: {detail}")
    if not answer:
        raise RuntimeError("DeepSeek Harness returned an empty answer")
    return answer


class HealthHandler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:  # noqa: N802
        if self.path != "/healthz":
            self.send_response(404)
            self.end_headers()
            return
        body = b'{"ok":true,"runtime":"deepseek-harness"}\n'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, _format: str, *_args: object) -> None:
        return


def serve_health() -> None:
    port = int(os.getenv("AGENTTEAMS_CONSOLE_PORT", "8088"))
    server = ThreadingHTTPServer(("0.0.0.0", port), HealthHandler)
    server.timeout = 1
    while not STOP.is_set():
        server.handle_request()
    server.server_close()


def main() -> int:
    worker_name = required_env("AGENTTEAMS_WORKER_NAME")
    homeserver = required_env("AGENTTEAMS_MATRIX_URL")
    token = required_env("AGENTTEAMS_WORKER_MATRIX_TOKEN")
    runtime_path = Path(required_env("TEAMHARNESS_RUNTIME_CONFIG"))
    workspace = Path(required_env("TEAMHARNESS_WORKSPACE"))
    workspace.mkdir(parents=True, exist_ok=True)
    member = runtime_section(runtime_path, "member")
    team = runtime_section(runtime_path, "team")
    own_user_id = member.get("matrixUserId") or f"@{worker_name}:{required_env('AGENTTEAMS_MATRIX_DOMAIN')}"
    watched_rooms = {room for room in (member.get("personalRoomId"), team.get("teamRoomId")) if room}
    if not watched_rooms:
        raise RuntimeError("runtime.yaml contains no personalRoomId or teamRoomId")

    state_path = runtime_path.parent / "matrix-next-batch"
    since = state_path.read_text(encoding="utf-8").strip() if state_path.exists() else None
    client = MatrixClient(homeserver, token)
    threading.Thread(target=serve_health, name="health", daemon=True).start()

    if not since:
        initial = client.sync(None, 0)
        since = str(initial.get("next_batch") or "")
        if not since:
            raise RuntimeError("initial Matrix sync returned no next_batch")
        atomic_write(state_path, since)
        push_runtime_state(worker_name, state_path)

    timeout_seconds = int(os.getenv("AGENTTEAMS_DSH_TASK_TIMEOUT_SECONDS", "3600"))
    print(f"[agentteams-dsh-worker] ready worker={worker_name} rooms={len(watched_rooms)}", flush=True)
    while not STOP.is_set():
        try:
            payload = client.sync(since, 30_000)
            for event in text_events(payload, watched_rooms, own_user_id):
                try:
                    answer = run_dsh(event["body"], workspace, timeout_seconds)
                except Exception as error:  # keep the channel loop alive and make failure visible
                    answer = f"DeepSeek Harness 任务执行失败：{error}"
                client.send_text(event["room_id"], answer, event["event_id"])
                sync_sessions(worker_name)
            next_batch = str(payload.get("next_batch") or "")
            if next_batch and next_batch != since:
                since = next_batch
                atomic_write(state_path, since)
                push_runtime_state(worker_name, state_path)
        except (urllib.error.URLError, TimeoutError, OSError, json.JSONDecodeError, subprocess.SubprocessError) as error:
            print(f"[agentteams-dsh-worker] channel error: {error}", flush=True)
            STOP.wait(2)
    return 0


def stop(_signum: int, _frame: object) -> None:
    STOP.set()


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    raise SystemExit(main())
