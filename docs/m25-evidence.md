# M2.5 local acceptance

The installed preview connects explicit test sessions through encrypted client
custody. No native agent input, wake, turn completion, restricted responder,
Internet/NAT qualification or release is claimed. Operational instructions are
in [m25.md](m25.md); the historical specification and laboratory results remain
unchanged.

## Tests and failure behavior

All tests, builds and linters used Go 1.27.3 and:

```sh
GOCACHE=/tmp/peerturn-go/cache
GOMODCACHE=/tmp/peerturn-go/mod
GOLANGCI_LINT_CACHE=/tmp/peerturn-go/lint-m25
```

The initial daemon test failed before implementation. Further red regressions
covered missing init session installation, the real descriptor exceeding the
old small relay control-body bound, and an unsynced sequence journal reaching
outbox custody. The latter now fences all subsequent mutations until reopen.

| Evidence | Covered boundary |
| --- | --- |
| [Two-daemon test](../internal/daemon/e2e_test.go) | Real local TLS relay and two isolated runtime/state directories: init, encrypted invitation/claim, directional grants, signed hello, both message directions, bound reply and receiver receipts. |
| Same test | Active WSS notification evidence, relay restart, connection drop, offline enqueue, daemon reopen, receipt recovery and one admission per message. |
| Same test | Recipient grant and relay-permit revoke reject the next send; relay SQLite/WAL/SHM scan omits body, session labels, scope and full URL. A separate real SQLite plaintext control verifies the scanner detects the canary. |
| [Reservation tests](../internal/daemon/recovery_test.go) | Reopen before/after queue commit and after custody commit retain the exact packet and sequence; a failed journal write cannot produce later effects. |
| [Offline ordering](../internal/daemon/ordering_test.go) | A 96-packet backlog exceeds the 64-sequence replay window without losing sequence continuity. Budget refusals remain authenticated refusals, not unseen sequence gaps. |
| [Maintenance](../internal/daemon/maintenance_test.go) | Never-relayed TTL expiry requires explicit fresh binding; inbound receipt controls remain usable, and store retention minimizes expired plaintext. |
| [Security regressions](../internal/daemon/security_regressions_test.go) | Expired return grant cannot block local revoke; ask/inform are refused on ingress; owner status/stop work; MCP capabilities cannot call owner controls. |
| [Onboarding recovery](../internal/daemon/onboarding_recovery_test.go) | Missing grants restore idempotently; revoked state stays revoked; bad/expired invitations do not stop existing peer custody; an unenrolled claimant can finish later. |
| [Paging](../internal/daemon/inbox_page_test.go), [store pages](../internal/store/transport_page_test.go) | Bounded inbox pages preserve all messages and stable timestamp/ID cursors. |
| [Configuration](../internal/daemon/config_test.go) | Private owned TOML, strict schema, bounds, no symlink/identity replacement, atomic identical retry and fail-closed configuration lookup. |

`internal/relayclient` tests additionally cover all signed endpoints, unsigned advertised-key consistency/TLS and
redirect boundaries, sanitized failures, immutable retry and reconnect/frontier
callbacks. `internal/localipc` tests cover uid checks, modes, independent flock,
stale sockets, malformed frames, disconnect cancellation, output bounds and
holding the lock through shutdown. `internal/mcp` and CLI tests cover strict
arguments, stdin, the six tools, binding, error exits and rejected authority
arguments.

The store schema and migration files are unchanged. M1b remains the SQL and
transaction authority; HPKE, signatures, certificates, grants and invitation
proofs reuse M1a. The already pinned `golang.org/x/sys v0.49.0` becomes a direct
dependency for Unix peer credentials and flock; no package version was added or
upgraded.

## Independent review

One independent `gpt-6-astra xhigh` pass found no Critical issue and identified
R1–R8 in the initial integration: return-grant-dependent revoke, offline ordering,
poisoned onboarding, missing grant recovery, unsent expiry gaps, incoming preview
scope, missing retention ticks and blank owner control binding. Each has an
implementation fix and the regressions above. A scoped review of the remaining
configuration/paging surfaces found no Critical or High issue.

The client now keeps per-channel pending sequence metadata in its sealed journal,
uses local-only authoritative revoke when notifications cannot be signed, and
reports `needs_rebind` rather than inventing a sequence-skip protocol. These are
local preview acceptance results, not an independent cryptographic audit.

## Security review corrections

The review of `e4eab55` added red/green regressions for mailbox progress with
revoked/expired return grants, routing and identity collisions in both onboarding
directions, relay-status retry, process-scoped MCP capabilities and state-directory
ownership. [Mailbox regressions](../internal/daemon/mailbox_review_test.go) require
frontier progress and admission from another peer. The
[three-daemon case](../internal/daemon/revoke_flow_review_test.go) checks exact
post-revoke errors and another peer's `receiver_stored` through a real TLS relay.

The independent correction review found one additional Medium: parked signed
revoke promotion could leave the cached peer flag active. Durable outbound
receipt reconciliation now restores that flag after promotion and after a
stale-journal restart; [the regression](../internal/daemon/parked_revoke_test.go)
failed before the fix and passes afterward. Relay HTTP responses never supply
that authority. The reviewer found no Critical blocker; this final correction
was verified by the integrator's regression and gate.

IPC tests use genuine other-uid subprocess credentials when root; this nonroot
macOS run checks both ends of real socket peers directly, without skipped tests.
The root branch is implemented but was not executed on this host. The relay
key option is documented as WebPKI plus unsigned advertised-value consistency,
not proof of signing-key possession.

## Gate results

Targeted package race tests, CLI/MCP tests, formatting and module maintenance
passed. The first full `make ci` passed build, vet and race tests, then returned
2 for two lint style findings; both were corrected. The final `make ci`
returned **0**: fmt-check, tidy-check, build, vet, race tests, lint (0 issues),
vulnerability scan (none found), actionlint and release-configuration check all
passed. The final local log is `/tmp/peerturn-go/m25-ci.log`; the initial lint
failure is retained in `m25-ci-initial.log` in that same directory. The PR draft
is `/tmp/peerturn-go/PR-m25.md`.

After the security corrections, final `make ci` again returned **0**,
including all race tests, lint (0 issues) and vulnerability scanning (none found).
The correction log is `/tmp/peerturn-go/m25-fix-ci.log`, with earlier ordering
fixture and lint failures retained separately. Per-finding changes, red/green
evidence, independent review and explicit low-priority deferrals are recorded in
`/tmp/peerturn-go/PR-m25-fix.md`.

All changed source/test/doc files are at most 400 lines. Local Markdown links,
whitespace and the AGENTS.md byte limit are checked separately. No commit, push,
deployment, publication or database migration was performed.

## Limits retained

File-permissions storage does not protect against same-uid compromise. Native
binding, adapters and key renewal remain later qualification; expired authority
does not acquire a replacement identity. The installed-preview profile hash is
not a publisher release signature. A config-write failure can leave initialized
state without a published config; preserve the keys and use the explicit state
directory rather than deleting or replacing identity. An expired inbox cursor
returns an empty page; restart inspection without the cursor. Both cases fail
closed and are documented for operators.
