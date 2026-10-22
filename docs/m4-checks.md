# M4 verification record

2026-10-21, Go 1.27.3, worktree `feat/m4-codex` on base `6dd78cc`.
All test/lint/build commands used
`GOCACHE=/tmp/peerturn-go/cache`, `GOMODCACHE=/tmp/peerturn-go/mod`,
`GOLANGCI_LINT_CACHE=/tmp/peerturn-go/lint-m4`.

| Check | Exit / observation |
| --- | --- |
| Initial targeted fixtures | 1, undefined native adapter/store/hook APIs before implementation |
| Adapter `go test -race ./internal/adapters/codex` | 0; transport/RPC/framing, runner/sandbox/redaction/process controls |
| Adapter `go vet` and pinned golangci | 0; no issues |
| Daemon/store targeted integration and atomic notification fixtures | 0; FIFO, local vs peer receipt IDs, inform once/no wake, revoke, ambiguity, stale binding, failed-init keys, transaction rollback |
| Full `make ci` | 2: new hook fixture Unix socket path too long; all other packages in full race run passed |
| Corrected `go test -race ./cmd/peerturn` | 0 after shortening test name to keep temporary socket path within native limits |
| fmt-check / tidy-check / build / vet in full gate | 0 |
| `make lint vuln workflow-check release-check` | 0; no lint issues or known vulnerabilities; actionlint and GoReleaser config valid |
| Evidence content/links/AST/line limits/synthetic-secret check | 0, 116 evidence files; teardown recorded |
| Skill quick validator | 0 with existing cached PyYAML; initial default interpreter lacked yaml |
| Changed-file ≤400 lines / documentation links / diff whitespace | 0 |

The one independent security/access review found no critical issue. Its findings
were addressed: native IDs stay local; owner revoke works after binding loss;
route filtering precedes paging; individual maximum native frames fit the context
budget; hook capability requires observed activation; result and signed notice
outbox commit atomically. A lost native acknowledgment still requires explicit
reconciliation and never causes automatic native resubmission.

[Live evidence](../research/lab/results/m4-codex/README.md) pins tested binaries
and separates native fork feasibility from product ask qualification. Final
post-review changes were not run through the full live lab again. Product ask,
authority-secret isolation, provider budget and automatic signed responder reply
remain unavailable. The unfinished acceptance remains documented in the capability matrix.

No commits or pushes were made. Project decisions and limitations remain in
[M4 operation](m4.md).

## Review fixes, 2026-10-22

The historical record above predates review `48f773b`. The owner subsequently
required notice-only inform, strict subagent exclusion, native dequeue on revoke,
per-item failure isolation, bounded atomic offers, true read stages, socket
reconnect and helper/path hardening. Ask preflight remains deferred and closed.

Regression-first daemon/store/hook/socket tests failed with exit 1 before their
fixes, then passed with race detection (exit 0). Targeted pinned package lint
passed after test-fixture corrections. Independent `gpt-6-astra xhigh` review
found no critical or new high issues; three medium regressions were fixed:
private native IDs in offered peer receipts, reply/reread after inbox delivery,
and partial batch cleanup changing sequence counters before all reads succeed.

The first full CI attempt exited 2 on the old test treating `delivered` as an
unknown stage. The next reached lint and exited 2 on three test-only issues.
The late batch-save ghost retry was reproduced (exit 1) and fixed (race exit 0).
Its repeated full gate exposed the old one-second TTL fixture crossing a wall
clock second before custody; a full-second margin fixed the fixture (race count=3,
exit 0). Final `make ci` exited **0**: build, vet, race tests, pinned lint
(0 issues), vulnerability scan (none), actionlint and GoReleaser validation passed.
The log is `/tmp/peerturn-go/m4-fix-ci-final3.log`. [New final-binary live evidence](../research/lab/results/m4-codex/fix-20261022/README.md)
is separate from the earlier lab. No commit/push or DB migration is part of this fix.

Final Linux ARM64 `313cfc22…950eb9` passed the live native matrix, including
genuine subagent stdin and exact queue deletion; containers x1-x3 were removed.
Remote revoke notification remains limited by a separately recorded control
admission budget failure. Content checks exited
0: 292 evidence files (JSON/AST/links/limits/secret markers), 164 new raw records
and final scenario assertions. All changed source/test/doc files remain <=400 lines.

The integration review identified a second remote receipt failure: mutual revoke
closes relay routes before terminal receipts arrive. The daemon now terminalizes
those locally revoked peers' rejected notifications with an audit event, while
independent peers keep native delivery and receipt flushing. See [M4 operation](m4.md)
for the Codex reconciliation limits and supported `grants revoke` path; these
corrections have unit/three-daemon regression coverage, not a new live lab claim.
