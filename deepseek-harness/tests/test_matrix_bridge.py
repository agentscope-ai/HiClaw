import sys
import unittest
from pathlib import Path


sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))

from matrix_bridge import dsh_error_detail, text_events  # noqa: E402


class TextEventsTest(unittest.TestCase):
    def test_keeps_new_text_from_other_users_in_watched_rooms(self):
        payload = {
            "rooms": {
                "join": {
                    "!personal:matrix.local": {
                        "timeline": {
                            "events": [
                                {
                                    "event_id": "$task",
                                    "sender": "@admin:matrix.local",
                                    "type": "m.room.message",
                                    "unsigned": None,
                                    "content": {"msgtype": "m.text", "body": "do the task"},
                                },
                                {
                                    "event_id": "$self",
                                    "sender": "@dsh:matrix.local",
                                    "type": "m.room.message",
                                    "content": {"msgtype": "m.text", "body": "old answer"},
                                },
                                {
                                    "event_id": "$file",
                                    "sender": "@admin:matrix.local",
                                    "type": "m.room.message",
                                    "content": {"msgtype": "m.file", "body": "input.txt"},
                                },
                            ]
                        }
                    },
                    "!unwatched:matrix.local": {
                        "timeline": {
                            "events": [
                                {
                                    "event_id": "$ignored",
                                    "sender": "@admin:matrix.local",
                                    "type": "m.room.message",
                                    "content": {"msgtype": "m.text", "body": "ignore"},
                                }
                            ]
                        }
                    },
                }
            }
        }

        self.assertEqual(
            text_events(payload, {"!personal:matrix.local"}, "@dsh:matrix.local"),
            [
                {
                    "room_id": "!personal:matrix.local",
                    "event_id": "$task",
                    "sender": "@admin:matrix.local",
                    "body": "do the task",
                }
            ],
        )

    def test_ignores_blank_messages_and_redacted_events(self):
        payload = {
            "rooms": {
                "join": {
                    "!room:matrix.local": {
                        "timeline": {
                            "events": [
                                {
                                    "event_id": "$blank",
                                    "sender": "@admin:matrix.local",
                                    "type": "m.room.message",
                                    "content": {"msgtype": "m.text", "body": "  "},
                                },
                                {
                                    "event_id": "$redacted",
                                    "sender": "@admin:matrix.local",
                                    "type": "m.room.message",
                                    "unsigned": {"redacted_because": {}},
                                    "content": {"msgtype": "m.text", "body": "removed"},
                                },
                            ]
                        }
                    }
                }
            }
        }

        self.assertEqual(text_events(payload, {"!room:matrix.local"}, "@dsh:matrix.local"), [])

    def test_dsh_error_prefers_actionable_message_over_runtime_footer(self):
        stderr = """file:///dsh/lib/index.js:1
Error: dsh: HTTP_405: DeepSeek API error (HTTP 405)
    at boot (file:///dsh/lib/index.js:2)
Node.js v22.22.3
"""
        self.assertEqual(
            dsh_error_detail(stderr, 1),
            "Error: dsh: HTTP_405: DeepSeek API error (HTTP 405)",
        )


if __name__ == "__main__":
    unittest.main()
