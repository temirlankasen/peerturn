# Mixed integration — Claude ↔ Codex

Run: 21 October 2026 UTC / 22 October local date. Separate PeerTurn clients/state
used one TLS relay in disposable Linux ARM64 containers. Claude Code
**2.1.306** used DeepSeek; Codex **0.163.0** used `gpt-5.4-mini`, effort `low`.
Owner permission/model profiles remained unchanged. TLS verification was enabled.
This is a sanitized report; synthetic prompts and raw native identifiers remain
in a private archive.

Final locked binary SHA256:
`c9fd8b70b25c59087078fcf0f92045221e9fa4be6990871a522c2b97039d53c3`.
Onboarding-only and diagnostic artifacts had distinct scope. The final run kept
existing identities/conversations after updates; it was not clean-state restart
acceptance. Real native tool children performed current-session binding/init
and join; a preview test-session label alone was not counted as native evidence.

| Final control | Observed result |
| --- | --- |
| Claude → idle Codex | Native input at 20:15:37.528 UTC; synthetic reply at 20:15:40.017. |
| Codex → idle Claude | Plugin input at 20:15:37.611 UTC; synthetic reply at 20:15:39.112. |
| Inform no wake | Both transcript hashes/row counts unchanged for at least 23 seconds; inform pending. |
| Next owner notice | Fixed count/sender notice, no peer body; Codex developer notice was 128 bytes. |
| Offered versus read | Both sender receipts became `offered/hook_notice` before explicit body read. |
| Explicit inbox read | Body first appeared in native tool output; both remote receipts became delivered/body-returned. |
| Claude pending revoke | Local target terminal `revoked/not_sent` before native admission. |
| Codex queued revoke | Native queued target removed; local terminal `revoked/dequeued`. |
| Post-owner observation | Neither revoked body nor ACK appeared; Codex queue stayed empty and transcript hashes stable. |
| Remote terminal receipts | Stale after simultaneous two-direction revoke; local fences were observed separately. |

Read-only metadata showed both route permits revoked and all already accepted
relay mailbox slots acknowledged. Three terminal controls on each side remained
in sender custody without reaching relay storage. No receiver budget rejection
was observed in this run. Closed reverse routes were a plausible explanation for
`forbidden`; direct HTTP capture and causal isolation were not performed. This
differs from the control-budget finding in the [Codex-only run](../m4-codex/fix-20261022/README.md).

A diagnostic artifact let a model call inbox despite a no-tools request. It did
not prove adherence to that request. The final fixture separated reply-only
notice and explicit inbox turns. Previously accepted coauthor could become
`in_doubt` after revoke; this did not erase earlier observed model replies.

The archived evidence validator passed; all assigned containers were removed.
This short recheck did not qualify arbitrary origins, hardened `ask`, Internet
native adapters, crash recovery, per-tool revoke, or exactly-once effects.
