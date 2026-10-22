# M1b local custody evidence

This is local store acceptance, not proof of a working agent bridge, relay,
provider billing cap, native model input, or exactly-once effects. Commands ran
in an isolated checkout with Go 1.27.2; no commit/push was made.
Schema and residuals: [store contract](store-m1b.md).

## Test-first evidence

| Slice | Red exit/result | Result after implementation |
| --- | --- | --- |
| Durable encrypted open | 1, missing store/sealer API | 0, key reopen/AAD isolation/private permissions and SQLite reopen |
| Invite authoritative consume | 1, missing/invalid initial fixture before implementation refinement | 0, 64 distinct claims, exactly one winner, stored retry result after expiry |
| Inbox/outbox lifecycle | 1, missing custody/expiry APIs | 0, duplicate/conflict, sender_stored, gap/expiry/ACK and intent recovery |
| Conflicting native result | 1, a changed result was accepted | 0, exact retry accepted, different result conflicts |
| Expired first-seen packet | 1, expired error left both frontiers empty | 0, authenticated empty tombstone then live next sequence |
| Gap receipt/control reserve | 1, data budget blocked control | 0, authenticated kind/lane, no body/dispatch/receipt response |
| Duplicate mailbox metadata flood | 1, record after 65,536 still accepted | 0, independent retained custody cap and typed backpressure |
| Outgoing root → incoming reply | 1, scope_denied without outbound parent | 0, exact-binding bidirectional chain lookup after reopen |
| Revoke after native queue acceptance | 1, inbox remained adapter_accepted | 0, queued item quarantined and attempt fenced |
| Invalid transport tombstone | 1, missing RejectMailbox API | 0, explicit rejected custody without consuming unverified sender sequence |

Additional table-driven and concurrency coverage includes 64 duplicate inbox
commits, independent handles, lost-response retry, same ID/fresh ciphertext,
mailbox holes/conflicts/lookahead, root collision, wrong identity/binding,
alternating conversations, control parent substitution, quota/control reserve,
monotonic terminal receipts and cancellation of a transaction context.

Crash-window fixtures abort after inbox write, decision, receipt and before
commit; after invite claim; after revoke epoch/quarantine/lease steps; after
native result persistence. No partial custody ACK/evidence or second invite
winner survives rollback. A committed intent with a lost native result recovers
to in_doubt and never returns Start=true on retry. These are transaction abort
simulations, not native runtime SIGKILL qualification.

Migration evidence covers fresh schema, idempotent reopen, persisted custody,
WAL/FULL/foreign_keys/busy_timeout/fullfsync PRAGMAs, future schema refusal,
and a conflicting initial table proving migration/ledger rollback together.

At-rest tests scan DB, WAL and SHM for unique dummy canaries in inbox text,
outgoing payload, invite result, native result and receipt native IDs. A separate
SQLite file intentionally stores a plaintext canary and is detected. Checkpoint
scans remain clean. No real message, invite secret or private key is a fixture.

## Independent review

One `gpt-6-astra xhigh` pass found no critical findings, two high and two medium:
unbounded duplicate mailbox metadata, absent first-seen expired tombstone,
parked control response/starvation, and absent outgoing conversation lookup.
Each has a red regression followed by a correction. No independent product
security audit or live integration approval is claimed.

## Gate evidence

- Targeted race store/envelope checks: exit 0.
- Outgoing chain/binding/control-parent targeted race suite: exit 0.
- CGO_ENABLED=0 store tests: exit 0.
- Pinned linter: exit 0, zero issues in the final full gate.
- govulncheck: exit 0, zero reachable/imported-package vulnerabilities; one
  required-module advisory is outside imported packages/call graph.
- Local content/link/table check: exit 0, all 16 tables documented, local links
  resolve, no >400-line file/suppression/TODO/skip; git diff --check exit 0.
- Final full make ci: exit 0 (run once on the finished code diff):
  fmt-check, tidy-check, CGO_ENABLED=0 build, vet, all race tests, pinned lint,
  vulnerability scan, actionlint and GoReleaser config check all passed.

The historical private PR handoff and local sandbox bookkeeping are archived
outside public history. The verification results above remain the evidence for
this milestone; local handoff-file creation is not a delivery claim.
