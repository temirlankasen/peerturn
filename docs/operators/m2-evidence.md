# M2 relay implementation evidence

Run date: 2026-10-21. Hardening base: `d4bdeeb`, following the initial implementation
based on M1a commit `131f66c4e30f9f4c94ce5544bf3a5b1b4874f4ef`.
Current host: Go 1.27.3, macOS arm64. No public relay deployment or agent delivery.

All Go checks, Docker builds and container smoke probes used
`GOCACHE=/tmp/peerturn-go/cache`, `GOMODCACHE=/tmp/peerturn-go/mod`,
`GOLANGCI_LINT_CACHE=/tmp/peerturn-go/lint-m2`. Docker additionally used
`BUILDX_CONFIG=/tmp/peerturn-go/buildx-m2` because the default Docker buildx cache
was outside the sandbox's writable roots.

## Current toolchain and authority checks

The required toolchain is Go 1.27.3. The initial 1.27.2 results below are historical.
An independent `make ci` on base commit `d4bdeeb` passed with exit 0, including
`govulncheck` with no vulnerabilities found. This baseline check does not qualify
subsequent local changes.

The official [Docker Registry manifest](https://registry-1.docker.io/v2/library/golang/manifests/1.27.3)
returned OCI index digest
`sha256:f24164268fc1e741bbdc0066dbadcf1c8426c5a8489f0729c02c3905a53032fd`.
The downloaded index bytes matched that SHA-256, and both Linux amd64 and arm64
image configs reported `GOLANG_VERSION=1.27.3` and `GOTOOLCHAIN=local`.
The Dockerfile pins this digest and requires `go env GOVERSION` to equal
`go1.27.3` before compilation. The [Go release history](https://go.dev/doc/devel/release#go1.27.3)
confirms the x509 security fix.

The authority regression initially failed with exit 1: direct TLS and trusted-proxy
requests signed for a foreign authority consumed durable nonces; missing authority
and public proxy CIDRs were also accepted. After the fix,
`go test -race ./internal/relay/auth ./internal/relay/config` passed with exit 0.
The pinned golangci-lint 2.15.0 targeted auth/config run also passed with exit 0,
reporting 0 issues.
Fixtures cover the ticket transport lane, required canonical authority, private
proxy boundaries, new resource defaults/bounds, and signature verification before
endpoint admission before durable replay. Forbidden, rate-limited and failed
admissions do not consume nonces.

The integrated hardening uses durable replay for mutations and a bounded,
expiry-only process-local cache for GET. Read signatures can be accepted again
after restart; GET cannot ACK/delete custody. Source concurrency, body deadlines,
separate source/key rates, anonymous bootstrap admission, per-key/per-mailbox poll
and stream caps, per-mailbox wakes and the pre-TLS connection cap are documented
in the [operator guide](relay.md). This describes implemented controls; final
integrated gate and acceptance results must be recorded separately.

Store regressions reproduced seven availability/access failures before changes
(exit 1), then `go test -race ./internal/relay/store -count=1` passed (exit 0).
Coverage includes the 4 MiB physical budget, exhausting custody and normal nonces
while preserving signed owner-control capacity, bounded mailbox/tenant/writer
metadata, minimum TTL, ACKed dedup grace cleanup, independent expiry maintenance,
reader-only tickets, and exact ciphertext retry receipts after revoke.
The integrated `go test -race ./internal/relay/...` passed with exit 0 for
all four relay packages. Full final CI and the final image build remain separate
checks; their completed results follow below. No schema or migration changed.

Review follow-ups additionally tested small physical budgets with tiny packets
and idle connections before TLS. Combined row/packet quotas now use hard fractions
of custody capacity without a minimum-byte floor, and socket admission applies
source fairness before TLS. The 1 MiB and 4 MiB tenant-isolation fixtures passed.

| Final hardening check | Result |
| --- | --- |
| Integrator `make ci` | Exit 0: fmt/tidy, build, vet, all race tests, lint 0 issues, no vulnerabilities, actionlint and goreleaser checks. |
| Frozen-clock rate/backpressure fixture, `-race -count=300` after final admission changes | Exit 0, 32.108 s; no timing-dependent failures. |
| Final `docker build` | Exit 0, Linux arm64; exact Go 1.27.3 assertion reused the successful pinned-builder cache layer. |
| Copied container binary: `go version -m` | Exit 0; `go1.27.3`, `CGO_ENABLED=0`, Linux arm64. |
| Final container TLS smoke | Exit 0: verified certificate/hostname, discovery, auth rejection, public health 404, UID 65532, read-only rootfs, graceful SIGTERM exit 0. |
| Integrator content/link/line checks | 52 files, 7 links, no errors; every changed file at most 400 lines. |

Two earlier hardening CI attempts stopped at lint findings; mechanical corrections
preceded the successful final gate. No check was bypassed. Final local OCI index
digest: `sha256:0140ec51be72b2a28d73a7449956179cc291959cb5e04c72ba90c9f4de6daba5`.
This image is local, unsigned and unpublished; it does not establish a Linux amd64
run, public self-host qualification or agent delivery.

Detailed startup/security-event logging and live TLS certificate reload remain
deferred; they require a separate operator interface. Current certificates are
loaded once at startup. Internet/NAT, clean VPS and soak qualification remain open.

## Historical initial implementation run

The following checks and image digest describe the earlier Go 1.27.2 run.
They are retained as history, not as the current toolchain or final image evidence.

| Check | Exit/result |
| --- | --- |
| Initial `go test ./internal/relay` | 1, missing relay/auth implementation: test-first red. |
| Targeted auth/config/store race tests and lint | 0 after implementation and relevant corrections. |
| `make ci` (one full run) | **2**, stopped at the vulnerability target. |
| Formatting and `go mod tidy -diff` within CI | 0. |
| `make build`, `make vet`, `make test` within CI | 0; all packages passed `-race`. |
| `make lint` within CI | 0, zero issues. |
| Vulnerability scanner within CI | Tool exit 3, `go run` exit 1: `GO-2026-6809`, stdlib `crypto/x509` at Go 1.27.2, fixed 1.27.3. No other imported-module findings reported. |
| `make workflow-check release-check` | 0; run separately because CI stopped before these targets. |
| Final `docker build -f packaging/docker/Dockerfile ...` | 0. |
| Final read-only/non-root container smoke | 0; verified TLS certificate, discovery, auth rejection, public health 404, graceful SIGTERM exit 0. |
| Documentation links/400-line bounds and `git diff --check` | 0. |

That initial run retained Go 1.27.2 and failed the vulnerability gate. The later
Go 1.27.3 pin resolves that toolchain finding; no vulnerability check was suppressed.

Historical local OCI manifest-list digest:
`sha256:0f645953aecb2f493147fd2e5849be289a1bfa9a9b95299392088902f6d65436`.
This is a local build, not a signed or published release.

## Initial acceptance coverage and remaining qualification

| Scenario | Local evidence | Qualification boundary |
| --- | --- | --- |
| LAB-E2E | `TestCiphertextOnlyStorageWithPositiveControl`: client HPKE/JWS roundtrip through HTTP mailbox, scan all data-dir files including SQLite/WAL/SHM, injected plaintext control detected. | No heap snapshot, backup scan or production logging qualification; relay cannot prove arbitrary caller bytes are encrypted. |
| LAB-CUSTODY / LAB-CRASH | `TestAbruptRestartCustody`: SIGKILL after committed ciphertext, reopen, same seq, ACK and retry preserve original dedup receipt. Store tests cover restart, nonces and tickets. | No native adapter/model submit or every internal syscall fault point. |
| LAB-RECEIPTS | HTTP dedup, wrong-digest 409, expired existing ID returns original receipt; SQLite page-full and closed storage return 503 with no uncommitted receipt/seq advance. | SQLite FULL is a deterministic storage-full fixture, not a host filesystem ENOSPC mount test. No model/input/execution claims. |
| LAB-REVOKE | Historical writer WSS closure measured 5.43 ms / 6.15 ms (<5 s); current hardening instead restricts streams and tickets to readers and rejects writer subscriptions. Terminal epoch remains available for route revoke. | Old stream timings do not qualify the changed reader-only lane. Route revoke does not revoke E2E grants or undo effects. |
| LAB-NET | HTTPS httptest, long-poll wake, notify-only WSS, fetch after reconnect; Linux container with real TLS verification and SIGTERM. | Public Internet, separate NATs, proxy deployment, packet loss/reorder and 24-hour offline clients remain unqualified. |
| LAB-LIMITS | Item/byte/metadata quotas, 1 MiB fetch pages, issued-range ACK, rate/stream/poll/request backpressure, independent durable nonce budget, no silent eviction. | No 100-session/20-msg-per-second or RSS/24-hour soak qualification. |
| LAB-SELFHOST | Built and ran scratch static binary as UID 65532, read-only rootfs, persistent volume, mounted TLS, dropped caps/no-new-privileges; docs/example config. | No clean public VPS, Linux amd64 run, signed release or daily backup/restore drill. |

## Historical initial independent review

One independent `gpt-6-astra xhigh` pass covered all tracked/untracked implementation,
schema, auth, transport, packaging and operator changes. Critical findings: zero.
Three actionable findings were corrected with regression fixtures:

1. Permanent metadata and replay entries shared a cap, blocking owner control.
   Reproduced as signed HTTP GET 503, fixed with a separate bounded nonce pool;
   signed Fetch/ACK/revoke stay available at the permanent quota.
2. Committed encrypted claim had no retrieval path. Existing GET invite now exposes
   optional canonical `claim_packet` only after commit, without voucher/plaintext.
3. The highest JSON-safe live permit epoch exhausted the revoke range. PUT reserves
   that epoch for terminal DELETE; boundary fixture reproduced red before the fix.

The separate initial `govulncheck` toolchain finding was resolved by Go 1.27.3.
Online consistent backup is the standard operator SQLite backup API (`sqlite3 .backup`); the relay has
no backup HTTP endpoint and cannot independently detect rollback of its whole disk.
At that initial revision, dedup tombstones remained under the metadata cap and
required a retention design before exceeding that lifetime budget.
