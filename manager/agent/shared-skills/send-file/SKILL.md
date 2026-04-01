---
name: send-file
description: Send a local file as a Matrix attachment to a chat room. Use when someone asks you to send, share, or transfer a file.
assign_when: Agent needs to send files to users via Matrix
---

# Send File

Send a local file as a Matrix attachment so the recipient can download it directly from the chat.

## Usage

**Manager:**
```bash
bash /opt/hiclaw/agent/shared-skills/send-file/scripts/send-file.sh <file_path> <room_id>
```

**Worker:**
```bash
bash ~/skills/send-file/scripts/send-file.sh <file_path> <room_id>
```

| Parameter | Description |
|-----------|-------------|
| `file_path` | Absolute path to the local file to send |
| `room_id` | Matrix room ID (e.g., `!abc123:domain`) — the target room |

On success, prints the `mxc://` URI. On failure, prints an error to stderr and exits non-zero.

## Examples

### Send a file you just created

```bash
echo "# Report" > /tmp/report.md
bash ~/skills/send-file/scripts/send-file.sh /tmp/report.md '!roomid:domain'
```

### Download from OSS/MinIO then send

```bash
mc cp ${HICLAW_STORAGE_PREFIX}/shared/tasks/task-123/output.csv /tmp/output.csv
bash ~/skills/send-file/scripts/send-file.sh /tmp/output.csv '!roomid:domain'
```

### Send a task result

```bash
bash ~/skills/send-file/scripts/send-file.sh ~/shared/tasks/task-123/result.md '!roomid:domain'
```

## Important

- The file must exist locally before sending — download it first if it's on OSS/MinIO
- MIME type is auto-detected from the file content
- Credentials are auto-resolved from environment variables or `openclaw.json`
- Do NOT paste file contents as text when asked to "send" a file — use this script to send as a proper downloadable attachment
