# Claude Monitor — macOS WebSocket wake

Run: 21 October 2026. Claude Code **2.1.306** on macOS with a Claude account
login exposed Monitor, unlike the DeepSeek profile in [L1](l1-platform/README.md).
This is a sanitized report of a private host experiment.

A loopback WebSocket server emitted a synthetic frame three seconds after
connection, then periodically. Monitor accepted the URL. After the session
finished its turn and became idle, a frame arrived as a native `task-notification`
identified as an automated background event. It began a new turn without new
owner input.

The tool description specified a 30-minute maximum lifetime, event coalescing
within 200 ms, and shutdown for excessive events. These limits were not
independently stress-tested. Reconnection requires an agent turn, and availability
depends on the provider profile.

This proves idle wake in the named profile. It does not establish E2E encryption,
remote grants, reconnect durability, or a production adapter. A plaintext relay
WebSocket exposes its frames to the relay unless decryption happens locally.
Native wake is separate from safe peer authority.
