# M1b security review fixes

This records the follow-up to the review of `203c2c3`, applied without commit or
push on `feat/m1b-store` at `0183e6a`, Go 1.27.3. Historical evidence remains in
[M1b evidence](store-m1b-evidence.md); the updated contract is [here](store-m1b.md).
The original review and sanitized PoC sources/log were read in full.

The owner explicitly allowed changes inside the approved logical model. The
unreleased initial `001.sql` was revised: scoped receipt keys, control counters,
inbox mailbox reference, bounded revoke reserve, aggregate audit count and
sealed key check. No shipped schema upgrade is promised. Old pre-fix databases
fail closed; they must not be reopened with a silently generated replacement key.

## Regressions

| Finding | Red observation | Corrected acceptance |
| --- | --- | --- |
| H1 | Far-future: budget_exceeded; scope: scope_denied; full control queue: backpressure; none ACKed | Attributable terminal custody; scope rejection consumes only an authenticated in-window sender sequence; honest second grant continues |
| H2/L3 | Delivered/rejected/in_doubt bodies retained; finished rows never pruned | TTL clears peer bodies/packets/bytes; horizon GC restores 8192 receipt-row/64 MiB data capacity; floors preserve replay protection |
| M1 | Orphan RecordReceipt accepted | Actual-row namespace and exact reverse-recipient checks; colliding inbound/outbound IDs conflict; namespaced AAD rejects swaps |
| M2 | 40 expired/control admissions accepted in one second | At most five admissions under baseline burst; remainder are bounded rejections with ACK progression |
| M3/L1 | Current-epoch revoke retry increments again; real late result returns wrong fence | Single epoch transition, per-message receipts/notifications/audits, responder cancellation, exact Revoked fence |
| L2/L7 | Missing/relative keys accepted, mismatched key opened DB, temp_store=0 | Explicit atomic create, existing-only open, key check, strict path/owner/perms checks, temp_store=2 |
| L4/L5/L8 | Unknown invite revoke accepted; expiry/revoke evidence could regress | Terminal receipts are monotonic; explicit native reconciliation; invite create/consume/revoke and delivery-result audits |
| Retry after refusal | Repeating ScopeDenied returns replay and loses ACK result | Recent slot preserves rejected reason; compacted frontier proves prior transport custody without readmission |

Red commands exited 1 on the expected behavioral assertions. Key, GC,
receipt/revoke/delivery/invite subsets passed with `-race` (exit 0).
The root store/envelope integration suite passed (exit 0); follow-up retry tests
also showed red 1 before green 0. No test skips or suppression comments were added.

All checks use:

```sh
GOCACHE=/tmp/peerturn-go/cache GOMODCACHE=/tmp/peerturn-go/mod \
GOLANGCI_LINT_CACHE=/tmp/peerturn-go/lint-m1b make ci
```

## Final verification

- Final `make ci`: exit 0, Go 1.27.3. All build/vet/race tests passed,
  golangci-lint reported 0 issues, govulncheck found no vulnerabilities,
  actionlint and GoReleaser check passed. Log: `/tmp/peerturn-go/m1b-fix-ci.log`.
- An earlier full-gate attempt exited 2 at lint (12 issues), after build/vet/race
  passed. Lint findings were corrected; its log is retained separately as
  `/tmp/peerturn-go/m1b-fix-ci-attempt1.log`. The final gate follows those changes.
- One independent `gpt-6-astra xhigh` security review found two further Medium
  refusal defects: paired out-of-order rejects accumulated sparse records and
  rejected promotions retained stale Pending. Both received red 1 → green 0
  regressions; final source review reports no open Critical/High/Medium findings.
- Diff/content/link/400-line checks: exit 0. No commits or pushes were made.
- Decisions remain in the store contract and these evidence notes.

## Explicit residuals

- This is local storage acceptance, without transport/native-adapter delivery,
  model-input, crash-on-real-filesystem, backup rollback or exactly-once claims.
- Unresolved native attempts and sparse consumed sequences remain until explicit
  reconciliation or gap resolution; their peer bodies are cleared at TTL.
- Rejection attribution is a bounded aggregate, not an exhaustive attack log.
- Owner revoke needs its trusted signer and terminal-control disk reserve; no
  grant-state change is reported if the atomic transaction cannot commit.
- L9 nonce-key lifecycle/rotation remains informational future work; OS keychain
  integration and protection against same-uid compromise are outside this store.
