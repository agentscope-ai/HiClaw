---
name: send-file
description: Send a local file as a Matrix attachment to the current chat room. Use when a user asks you to send, share, or transfer a file.
assign_when: Worker needs to send files to users via Matrix
---

# Send File

Send a local file as a Matrix attachment so the recipient can download it directly from the chat.

## Usage

```bash
bash ~/skills/send-file/scripts/send-file.sh <file_path> <room_id>
```

| Parameter | Description |
|-----------|-------------|
| `file_path` | Absolute path to the local file to send |
| `room_id` | Matrix room ID (e.g., `!abc123:domain`) — use the room you're currently chatting in |

On success, prints the `mxc://` URI. On failure, prints an error to stderr and exits non-zero.

## How to get the room ID

The room ID is available from your current conversation context. It's the Matrix room where the user sent you the message.

## Examples

### Send a file you just created

```bash
# Create a report
echo "# Report" > /tmp/report.md

# Send it to the user
bash ~/skills/send-file/scripts/send-file.sh /tmp/report.md '!roomid:domain'
```

### Download from OSS then send

```bash
# Download from MinIO/OSS to local
mc cp hiclaw/bucket/path/to/data.csv /tmp/data.csv

# Send to user
bash ~/skills/send-file/scripts/send-file.sh /tmp/data.csv '!roomid:domain'
```

### Send a task result

```bash
# After completing work, send the output file
bash ~/skills/send-file/scripts/send-file.sh ~/.copaw-worker/$(whoami)/shared/tasks/task-123/result.md '!roomid:domain'
```

## Important

- The file must exist locally before sending — download it first if it's on OSS/MinIO
- MIME type is auto-detected from the file content
- The script uses your Matrix credentials from `openclaw.json` (no manual config needed)
- Do NOT paste file contents as text when the user asks you to "send" a file — use this script to send it as a proper attachment
