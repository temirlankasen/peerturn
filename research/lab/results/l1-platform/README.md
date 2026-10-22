# L1 — native ingress feasibility

Run: 21 October 2026, 11:15–11:41 UTC; collection continued to 11:46 UTC.
Disposable Linux ARM64 containers ran Claude Code **2.1.306** with DeepSeek
`deepseek-flash` and Codex CLI/shared app-server **0.163.0** with `gpt-5.4-mini`
through a test gateway. Owner permission modes remained unchanged. This is a
sanitized summary of a private run; identifiers and raw transcripts are omitted.

| Path | Observation | Limit |
| --- | --- | --- |
| Claude native peer inbox | Natural peers woke idle receivers; busy messages entered after an active tool result in observed order. | Local process attestation and sender mode do not authorize a remote peer. |
| Claude external socket sender | Accepted under the tested inbound policy; missing mode attestation could hold input for approval. | A socket write was not a model acknowledgement. |
| Claude plugin `$.prompt.submit` | HTTP polling submitted idle input and waited for the active turn while busy; input had plugin provenance. | Reload and timers were not a transactional outbox. |
| Codex shared-daemon queue | Exact loaded threads accepted queued input and woke when idle; `turn/start`/`turn/steer` were available. | Queue acceptance differed from model input and completion. |
| HTTP MCP | Tool connectivity worked. | Connectivity alone did not wake an idle model. |
| Claude Monitor / Channels | Unavailable in this profile. | The [macOS Monitor observation](../host-monitor-ws.md) used another login profile. |

Repeated external Claude messages were both consumed. This was not a repeated
`msg_id` test and does not establish native deduplication. A queued marker
followed by process termination was not consumed after resume; a fresh message
to the new live process was consumed. Stale discovery made name-based addressing
ambiguous. Exact live identity and liveness checks were necessary.

A private local bridge required a disposable Linux porting patch. Its local
trust model did not authorize remote peers. A source snapshot was read at
`c419fd01a70dd1d05356da443ac44328bda2e6f4`; source/release correspondence was
not established. Binary observations were kept separate from source inference.

Native ingress was feasible for these versions. PeerTurn still needed exact
binding, directional grants, durable custody, explicit receipts, and conservative
ambiguous-submit handling. Universal restart, delivery, or restricted-turn safety
was not established. Containers were removed after collection.
