---
name: send-file
description: Send a local file as a Matrix attachment to a chat room. Use when admin asks you to send or share a file.
---

# Send File

Send a local file as a Matrix attachment so the recipient can download it directly from the chat.

## Usage

```bash
bash /opt/hiclaw/agent/worker-skills/send-file/scripts/send-file.sh <file_path> <room_id>
```

| Parameter | Description |
|-----------|-------------|
| `file_path` | Absolute path to the local file to send |
| `room_id` | Matrix room ID (e.g., `!abc123:domain`) — the target room |

On success, prints the `mxc://` URI. On failure, prints an error to stderr and exits non-zero.

## Examples

### Send a file to admin's DM

```bash
bash /opt/hiclaw/agent/worker-skills/send-file/scripts/send-file.sh /root/hiclaw-fs/shared/tasks/task-123/result.md '!roomid:domain'
```

### Pull Worker output from MinIO then send to admin

```bash
# Pull from MinIO
mc cp ${HICLAW_STORAGE_PREFIX}/shared/tasks/task-123/output.csv /tmp/output.csv

# Send as attachment
bash /opt/hiclaw/agent/worker-skills/send-file/scripts/send-file.sh /tmp/output.csv '!roomid:domain'
```

## Important

- The file must exist locally before sending — pull from MinIO first if needed
- MIME type is auto-detected from the file content
- Uses `MANAGER_MATRIX_TOKEN` or `MATRIX_ACCESS_TOKEN` env var for authentication
- Do NOT paste file contents as text — use this script to send as a proper downloadable attachment
