---
name: channel-management
description: Manage multiple communication channels, admin identity recognition, and primary channel configuration
assign_when: Not assigned to workers — this is a manager-only capability
---

# Channel Management Skill

## Identity Recognition

When receiving a message in any room, determine the sender's identity. The rules differ by context:

### DM (any channel)

All DM senders are **Human Admin** — OpenClaw allowlist guarantees only the admin can DM you.

### Matrix Group Room

Matrix rooms may contain the admin, Workers, trusted contacts, and unknown users. Identify by Matrix user ID:

| Sender | How to identify | Action |
|--------|----------------|--------|
| **Human Admin** | `@${HICLAW_ADMIN_USER}:${HICLAW_MATRIX_DOMAIN}` | Full trust — execute any request |
| **Worker** | Registered in `~/workers-registry.json` | Normal Worker interaction (task handoffs, status updates) |
| **Trusted Contact** | `{"channel": "matrix", "sender_id": "<matrix_user_id>"}` in `~/trusted-contacts.json` | Respond to general questions; withhold sensitive info; deny management operations |
| **Unknown** | None of the above | **Silently ignore** — no response |

### Non-Matrix Group Room (Discord, Telegram, etc.)

| Sender | How to identify | Action |
|--------|----------------|--------|
| **Human Admin** | `sender_id` matches `primary-channel.json`'s `sender_id` (same channel type) | Full trust |
| **Trusted Contact** | `{channel, sender_id}` in `~/trusted-contacts.json` | Restricted trust (same rules as above) |
| **Unknown** | None of the above | **Silently ignore** |

## Trusted Contacts

File: `~/trusted-contacts.json`

```json
{
  "contacts": [
    {
      "channel": "discord",
      "sender_id": "987654321098765432",
      "approved_at": "2026-02-23T10:00:00Z",
      "note": "optional label"
    }
  ]
}
```

### Adding a Trusted Contact

Trigger: unknown sender messages in a group room → silently ignore. If the human admin then says "you can talk to the person who just messaged" (or equivalent):

1. Identify the most recent unknown sender's `channel` and `sender_id` from the current session context
2. Append to `trusted-contacts.json`:
   ```bash
   # Read existing, append, write back (use jq)
   jq --arg ch "<channel>" --arg sid "<sender_id>" --arg ts "<ISO-8601 now>" \
     '.contacts += [{"channel": $ch, "sender_id": $sid, "approved_at": $ts, "note": ""}]' \
     ~/trusted-contacts.json > /tmp/tc.json && \
     mv /tmp/tc.json ~/trusted-contacts.json
   ```
   If file doesn't exist yet: `echo '{"contacts":[]}' > ~/trusted-contacts.json` first.
3. Confirm to admin in their language, e.g.: "OK, I'll engage with them. Note: I won't share any sensitive information with them."

### Communicating with Trusted Contacts

When a trusted contact sends a message:
- Respond normally to general questions
- **Never share**: API keys, tokens, passwords, Worker credentials, internal system configuration, or any other sensitive operational data
- **Never execute**: management operations (create/delete workers, change config, assign tasks, etc.)
- If they ask for something outside their role, decline politely and suggest they contact the admin directly

### Removing a Trusted Contact

When admin revokes access ("don't talk to X anymore"):
```bash
jq --arg ch "<channel>" --arg sid "<sender_id>" \
  '.contacts |= map(select(.channel != $ch or .sender_id != $sid))' \
  ~/trusted-contacts.json > /tmp/tc.json && \
  mv /tmp/tc.json ~/trusted-contacts.json
```

## Primary Channel State

File: `~/primary-channel.json`

```json
{
  "confirmed": true,
  "channel": "discord",
  "to": "user:123456789012345678",
  "sender_id": "123456789012345678",
  "channel_name": "Discord",
  "confirmed_at": "2026-02-22T10:00:00Z"
}
```

Fields:
- `confirmed`: `true` = use this channel for proactive notifications; `false` = Matrix DM fallback
- `channel`: channel identifier string, used as the `channel` parameter when calling the `message` tool (e.g. `"discord"`, `"telegram"`, `"slack"`)
- `to`: recipient identifier, used as the `target` parameter when calling the `message` tool. Format varies by channel:
  - Discord DM: `user:USER_ID` (e.g. `user:123456789012345678`)
  - Feishu DM: open_id，即 `ou_` 开头的字符串（e.g. `ou_abc123def456`）；群聊则用 `chat_id`（`oc_` 开头）
  - Telegram: chat ID (e.g. `123456789`)
  - WhatsApp/Signal: phone number (e.g. `+15551234567`)
- `sender_id`: the admin's raw ID on that channel (used for identity tracking)
- `channel_name`: human-readable name for display (e.g. `"Discord"`, `"飞书"`)
- `confirmed_at`: ISO-8601 timestamp when the admin confirmed this choice

Read with fallback:
```bash
cat ~/primary-channel.json 2>/dev/null || echo '{"confirmed":false}'
```

## Sending Messages to Primary Channel

Use the built-in `message` tool to send proactive notifications (daily reminders, heartbeat check-ins, task updates, escalation questions) to the admin on their primary channel.

### Steps

1. Read `primary-channel.json`:
   ```bash
   cat ~/primary-channel.json 2>/dev/null || echo '{"confirmed":false}'
   ```
2. If `confirmed` is `true` and `channel` is not `"matrix"`, call the `message` tool:

   | Parameter | Value |
   |-----------|-------|
   | `channel` | `.channel` from the file (e.g. `"discord"`) |
   | `target`  | `.to` from the file (e.g. `"user:123456789012345678"`) |
   | `message` | Your notification text |

3. If `confirmed` is `false`, `.channel` is `"matrix"`, or the file doesn't exist — fall back to Matrix DM.

### Example

Given `primary-channel.json`:
```json
{"confirmed": true, "channel": "discord", "to": "user:123456789012345678"}
```

Call the `message` tool with:
- `channel`: `"discord"`
- `target`: `"user:123456789012345678"`
- `message`: `"You have 2 tasks pending review. Want me to summarize?"`

### Notes

- The `message` tool is a built-in OpenClaw tool — no HTTP calls or scripts needed.
- When calling `message` from within a Matrix session, you MUST explicitly set `channel` and `target`; otherwise the message goes to the current Matrix room.
- The `target` parameter corresponds to the `to` field in `primary-channel.json`. Despite the name difference, pass the value directly without transformation.

## First-Contact Protocol

Trigger: admin sends a DM from a channel that doesn't match `primary-channel.json`'s `.channel`.

Steps:
1. Read `primary-channel.json` — check if `.channel` matches the current session's channel
2. Respond to the admin's message normally
3. Send a follow-up asking about primary channel preference, **in the same language the admin used in their message**:
   > I noticed this is your first time contacting me via [Channel Name]. Would you like to set [Channel Name] as your primary channel? If so, my daily reminders and proactive notifications will be sent here instead of Matrix DM. Reply "yes" to confirm, or "no" to keep using Matrix DM.
4. On **"yes" / "confirm" / 「是」/ 「确认」** (or equivalent in their language):
   ```bash
   cat > ~/primary-channel.json << 'EOF'
   {
     "confirmed": true,
     "channel": "<channel>",
     "to": "<to>",
     "sender_id": "<sender_id>",
     "channel_name": "<Channel Name>",
     "confirmed_at": "<ISO-8601 now>"
   }
   EOF
   ```
5. On **「否」/ "no"**: write `{"confirmed": false}` or leave unchanged; Matrix DM remains primary
6. On no reply (session ends): leave unchanged; Matrix DM remains primary

## Changing Primary Channel

When admin requests a switch (e.g. "switch to Discord as primary", "切换到飞书作为主频道", etc.), in any language:

1. Read current `primary-channel.json`
2. Update: `channel`, `to`, `sender_id`, `channel_name`, `confirmed: true`, `confirmed_at` = now
3. Write updated file
4. Confirm in the admin's language, e.g.: "Primary channel switched to [Channel Name]. Daily reminders and proactive notifications will now be sent there."

## Cross-Channel Escalation

When blocked on an admin decision while working in a Matrix room:

### When to Use
- Blocked on irreversible action needing explicit approval
- Worker/project is stalled and needs admin judgment call
- Cannot wait for next heartbeat or scheduled DM check-in

### How to Escalate

1. Read `primary-channel.json` (see "Primary Channel State" above)
2. If a non-Matrix primary channel is confirmed, use the `message` tool to send the question directly:

   | Parameter | Value |
   |-----------|-------|
   | `channel` | `.channel` from the file |
   | `target`  | `.to` from the file |
   | `message` | A clear, friendly escalation message containing the question and asking the admin to reply |

3. After sending, note the pending escalation in your memory so you can connect the admin's reply back to the blocked workflow when it arrives.

### Reply Handling

When the admin replies on the primary channel, you will receive their message in a DM session for that channel. At that point:
1. Recognize the reply as the answer to the pending escalation
2. Continue the blocked workflow in the original Matrix room
3. @mention relevant workers in the room with the outcome

### Fallback

If `primary-channel.json` is missing, `confirmed` is `false`, or channel is `matrix`: fall back to @mentioning the admin in the current Matrix room.

## Troubleshooting

**通知发送到错误目标**：管理员反映收不到通知。检查 `primary-channel.json` 的 `to` 字段是否正确，向管理员确认其频道 ID 后重新写入。

**Message tool send failure**: 使用 `message` 工具发送到主用频道失败时：
1. Is openclaw running? (`ps aux | grep openclaw`)
2. Is the target channel configured in openclaw? (check the corresponding channel config, e.g. `channels.discord.enabled`)
3. Is the `to` value in `primary-channel.json` correct for the channel format? (see "Primary Channel State" field descriptions above)
4. Fallback to Matrix DM is automatic; no manual intervention needed for individual failures

**Admin confirmed wrong channel**: Admin wants to revert to Matrix DM. Write `{"confirmed": false}` to `primary-channel.json`.

