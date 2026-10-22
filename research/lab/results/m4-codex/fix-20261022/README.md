# M4 corrections — pinned Codex live recheck

Run: 21 October 2026 UTC / 22 October local date. Codex CLI **0.163.0**,
Debian **12.15**, Linux ARM64, `gpt-5.4-mini`, effort `low`. Real shared-daemon
TUIs preserved owner `never`/`danger-full-access`. TLS verification remained
on with a disposable lab certificate. This is a sanitized private-run report.

Final clean-custody binary SHA256:
`313cfc22c7a91f64537b7b2fd7e1854c49b8edc59c35bf0a590e2591b2950eb9`.
An earlier behavioral artifact
`67b818cb990c62dc0c3b2274b425fb242b7af0bbe988eb3d2dfa79b64f86049f`
had separate outcomes; its successes were not transferred to the final pin.

| Final control | Observed result |
| --- | --- |
| Inform no wake | Rollout hash and hook count unchanged during a 33-second observation; message pending. |
| Coauthor separation | Idle coauthor completed in the same TUI without consuming inform. |
| Fixed owner notice | Developer message contained 128 bytes of count/fingerprint/inbox metadata; peer body absent. |
| Offer versus read | `offered/hook_notice` preceded explicit inbox read; remote receipts omitted native identifiers. |
| Real subagent | Distinct native child input contained neither notice nor body; inform remained pending. |
| Explicit inbox | Body arrived only in native CLI tool output; receipts advanced to delivered/read. |
| Journal cleanup | No empty pending key or leftover reservation in sampled notice/read/FIFO states. |
| Busy FIFO | Owner completed before A input, then B input; queue emptied. |
| Owner control | Actual owner inbox and write canary succeeded under the original permission mode. |
| Ask | Exit 6 with `responder_unavailable/protected_path_policy_failed`; no fork launched. |
| Local queued revoke | Actual native queue item removed; local receipt terminal `revoked`, outcome `dequeued`. |
| After owner completion | Target input/ACK absent; rollout remained stable and queue empty in later observations. |
| Remote revoke receipt | Still stale after 52 seconds; not evidence of remote terminal notification. |

Metadata showed five priority controls stored and acknowledged by the relay,
while sender admission rejected the last control with `budget_exceeded`.
Control-budget exhaustion was consistent with the stale receipt. A later
`forbidden` transport status was a separate observation of a revoked relay
permit. Local native deletion and the local authority fence were observed
independently. A post-revoke send accepted only local custody (`sender_stored`)
and did not produce receiver model input; CLI success was not delivery.

On the earlier artifact, a model invoked inbox despite an owner no-tools request.
That run established body separation, not adherence to the request. The final
fixture explicitly requested inbox later. Automatic hook trust invalidation on
binary change was not proven.

Arbitrary external root initiators, specialized hook origins, restart/crash races,
ambiguous deletion, encrypted-state recovery, hardened `ask`, Internet/NAT, and
exactly-once effects were not qualified. The integrator's historical full CI log
reported success; publication sanitization did not rerun it. Containers were
removed after collection.
