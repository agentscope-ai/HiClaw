#!/usr/bin/env python3
"""
copaw-sync - Manual sync trigger for CoPaw Worker

Reads MinIO credentials from environment variables and triggers an immediate
sync of config files (openclaw.json, SOUL.md, AGENTS.md, skills) from MinIO.

Environment variables (reads HICLAW_* set by the container, with COPAW_* fallback
for backward compatibility with remote/pip-installed workers):
- HICLAW_WORKER_NAME: Worker name
- HICLAW_FS_ENDPOINT: MinIO endpoint (e.g., http://fs-local.hiclaw.io:18080)
- HICLAW_FS_ACCESS_KEY: MinIO access key (worker name)
- HICLAW_FS_SECRET_KEY: MinIO secret key
- HICLAW_FS_BUCKET: MinIO bucket (default: hiclaw-storage)
- COPAW_WORKING_DIR: CoPaw working directory (default: ~/.copaw-worker/<worker_name>/.copaw)
  (set at runtime by bridge.py, not a container env var)
"""
import os
import sys
from pathlib import Path

# ---------------------------------------------------------------------------
# Ensure we run inside the correct venv.
#
# The copaw-worker package is installed in /opt/venv/standard (or /opt/venv/lite),
# NOT in the system Python.  When the Agent calls `python3 <this-script>`, it
# uses the system interpreter which cannot find copaw_worker.
#
# Fix: if the import fails AND we detect a venv with copaw_worker installed,
# re-exec this script with that venv's python so all dependencies are available.
# ---------------------------------------------------------------------------
_VENV_REEXEC_MARKER = "_COPAW_SYNC_VENV_REEXEC"

def _find_venv_python() -> str | None:
    """Return the first venv python that has copaw_worker installed."""
    for venv in ("/opt/venv/lite", "/opt/venv/standard"):
        py = Path(venv) / "bin" / "python3"
        if py.exists():
            return str(py)
    return None

# Try to import copaw_worker - it should be installed via pip
try:
    from copaw_worker.sync import FileSync
    from copaw_worker.bridge import bridge_openclaw_to_copaw
except ImportError:
    # If not installed, try to add source path (for development)
    src_path = Path(__file__).parent.parent.parent.parent / "src"
    if src_path.exists():
        sys.path.insert(0, str(src_path))
        try:
            from copaw_worker.sync import FileSync
            from copaw_worker.bridge import bridge_openclaw_to_copaw
        except ImportError:
            pass  # fall through to venv re-exec below
    # Attempt venv re-exec (only once to avoid infinite loop)
    if "copaw_worker" not in sys.modules and not os.environ.get(_VENV_REEXEC_MARKER):
        venv_py = _find_venv_python()
        if venv_py:
            os.environ[_VENV_REEXEC_MARKER] = "1"
            os.execv(venv_py, [venv_py] + sys.argv)
        print("Error: copaw-worker package not found and no venv detected.", file=sys.stderr)
        print("Please install it with: pip install copaw-worker", file=sys.stderr)
        sys.exit(1)
    elif "copaw_worker" not in sys.modules:
        print("Error: copaw-worker package not found even in venv.", file=sys.stderr)
        sys.exit(1)


def main():
    # Read environment variables.
    # Primary: HICLAW_* (set by container and entrypoint).
    # Fallback: COPAW_* (backward compat for remote/pip-installed workers).
    worker_name = os.getenv("HICLAW_WORKER_NAME") or os.getenv("COPAW_WORKER_NAME")
    minio_endpoint = os.getenv("HICLAW_FS_ENDPOINT") or os.getenv("COPAW_MINIO_ENDPOINT")
    minio_access_key = os.getenv("HICLAW_FS_ACCESS_KEY") or os.getenv("COPAW_MINIO_ACCESS_KEY")
    minio_secret_key = os.getenv("HICLAW_FS_SECRET_KEY") or os.getenv("COPAW_MINIO_SECRET_KEY")
    minio_bucket = os.getenv("HICLAW_FS_BUCKET") or os.getenv("COPAW_MINIO_BUCKET") or "hiclaw-storage"
    working_dir = os.getenv("COPAW_WORKING_DIR")

    if not all([worker_name, minio_endpoint, minio_access_key, minio_secret_key]):
        print("Error: Missing required environment variables", file=sys.stderr)
        print("Required: HICLAW_WORKER_NAME, HICLAW_FS_ENDPOINT, "
              "HICLAW_FS_ACCESS_KEY, HICLAW_FS_SECRET_KEY", file=sys.stderr)
        sys.exit(1)

    if not working_dir:
        working_dir = Path.home() / ".copaw-worker" / worker_name / ".copaw"
    else:
        working_dir = Path(working_dir)

    print(f"Syncing files for worker: {worker_name}")
    print(f"MinIO endpoint: {minio_endpoint}")
    print(f"Working directory: {working_dir}")

    # Initialize FileSync
    sync = FileSync(
        endpoint=minio_endpoint,
        access_key=minio_access_key,
        secret_key=minio_secret_key,
        bucket=minio_bucket,
        worker_name=worker_name,
        secure=minio_endpoint.startswith("https://"),
        local_dir=working_dir.parent,
    )

    # Pull all files
    try:
        changed = sync.pull_all()
        if changed:
            print(f"✓ Synced {len(changed)} file(s): {', '.join(changed)}")
            
            # Re-bridge config if openclaw.json changed
            if any("openclaw.json" in f for f in changed):
                print("Re-bridging openclaw.json to CoPaw config...")
                openclaw_cfg = sync.get_config()
                soul = sync.get_soul()
                agents = sync.get_agents_md()
                
                if soul:
                    (working_dir / "SOUL.md").write_text(soul)
                if agents:
                    (working_dir / "AGENTS.md").write_text(agents)
                
                bridge_openclaw_to_copaw(openclaw_cfg, working_dir)
                print("✓ Config re-bridged. CoPaw will hot-reload automatically.")
        else:
            print("✓ No changes detected. All files are up to date.")
    except Exception as exc:
        print(f"✗ Sync failed: {exc}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
