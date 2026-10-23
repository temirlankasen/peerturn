# MCP request fixtures and parameter audit

`codex-0.163.0-discovery.jsonl` contains only the stock Codex requests recorded in
`research/lab/results/macos-live-3/codex-mcp-metadata.jsonl`. Environment probes,
responses and private host context are excluded. The captured tools/list request
uses numeric `_meta.progressToken: 0`; discovery must expose all seven tools
without selecting a native session or contacting a daemon.

`claude-2.1.306-initialize.json` is the existing sanitized macos-live-2 request.
The method audit tests extend its handshake with spec-defined request shapes;
those extensions are synthetic fixtures, not additional live Claude evidence.

The server negotiates `2024-11-05`, `2025-03-26`, `2025-06-18`, and `2025-11-25`.
All four use request `_meta` and list `cursor`. Informational extensions inside
clientInfo, capabilities and `_meta` remain extensible; other method parameters
and tool arguments remain strict. A compatibility union accepts the later task
shape on older negotiated revisions too, without advertising task execution.

| Method | Accepted parameters / behavior |
| --- | --- |
| initialize | Required protocolVersion, capabilities, clientInfo; optional bounded `_meta`. |
| notifications/initialized | Notification with omitted params, `{}`, or `_meta`; all notifications are ignored without responses or effects. An ID makes this an unsupported request. |
| ping | Omitted params, `{}`, or `_meta`. |
| tools/list | Omitted params, `{}`, `_meta`, optional string cursor. The fixed catalog fits one page and issues no continuation; empty cursor starts it, unknown nonempty cursors return -32602. |
| tools/call | Required name; optional arguments, `_meta`, task object with optional numeric ttl. No task capability is advertised: valid task metadata is ignored and the call executes synchronously. |
| resources/list, resources/templates/list, resources/read, prompts/list, prompts/get, tasks/* | Not implemented or advertised; requests return -32601, including with `_meta`. |

Request metadata is limited to 64 KiB within the frame limit; initialize keeps
its smaller 16 KiB total limit. Duplicate keys, invalid JSON/depth and invalid
metadata types are rejected. progressToken accepts strings and JSON numbers.
Metadata never selects authority except the pre-existing tools/call Codex
threadId verification against the daemon's exact native binding. Other methods
cannot establish it; the next call still needs its own valid thread context.

Primary schema sources for the audit:
[2024-11-05](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2024-11-05/schema.ts),
[2025-03-26](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2025-03-26/schema.ts),
[2025-06-18](https://modelcontextprotocol.io/specification/2025-06-18/schema),
[2025-11-25](https://modelcontextprotocol.io/specification/2025-11-25/schema).
See also [pagination](https://modelcontextprotocol.io/specification/2025-11-25/server/utilities/pagination)
and [task handling](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/tasks#task-support-and-handling).
