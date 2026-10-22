# M1a review-fix evidence

Scope: uncommitted changes on `feat/m1a-core`, starting at `97df05da9c23caf5c81f562971e6d010eb414fd0`, 2026-10-21. The integrator owns commit and merge. No push, service deployment, database migration, or native agent admission is part of this work.

The supplied security review reported M-1 invite formatting leakage, M-2 ambiguous gap/replay outcomes, L-1 through L-7 hardening, and T-1 through T-5 test weaknesses. The updated `main` specification was read through `git show main:docs/spec/{security,design,requirements}.md`; the old in-session guard is superseded for v1 by the fork-responder model. Historical specification and lab evidence were not rewritten.

## Regressions and test-first evidence

| Boundary | Red result | Result after implementation |
| --- | --- | --- |
| Invite formatting | `go test` exit 1: real fixture decimal/hex/base64 secret output and first-contact `needs_verify` | Race tests exit 0; nested/unexported formatting, errors, text/JSON slog and serialization covered. |
| Sequence classification | `go test ./internal/envelope -run '^TestPureReplayDecision$' -count=1` exit 1: gap returned `replay` | Exit 0: future sequence returns `sequence_gap`; old/reused sequence remains `replay`. |
| Pending custody API | Focused tests exit 1 before the new metadata API existed | Exit 0: verified metadata, no plaintext/dispatch, bounded pending window, conflict/replay, and gap-then-fill. |
| Future continuation | `go test ./internal/envelope -run '^TestGapPreserves' -count=1` exit 1: missing future parent returned `scope_denied` | Exit 0: park first, rerun conversation validation when the gap fills. |
| Scope policy | `go test ./internal/grant -run '^TestScopeAllows' -count=1` exit 1: `inform/inform` returned `guard_unavailable` | Exit 0: recipient-approved ceilings and receiver routes; body cannot escalate authority. |
| Routing API | Focused tests exit 1 before typed routing existed | Exit 0: live/responder/inbox, duplicates have no dispatch route, root conversation reuse denied. |
| Lost ACK | Focused tests exit 1 before `ClaimRef` existed | Exit 0: authenticated retained-result lookup after expiry, with no new consumption permission. |
| Certificate/rotation lifetime and Ed25519 keys | Focused tests exit 1: overlong authority and identity-key forgery accepted | Race tests exit 0: maximum lifetimes, all small-order encodings and noncanonical field encodings rejected. |
| HPKE domains | Focused tests exit 1 for six cross-context opens and independent stdlib info checks | Race tests exit 0 with separate envelope/descriptor/claim `info`. |
| JWS malformed part allocation | Focused test exit 1: approximately 1.65 MB allocated per dotted compact rejection | Exit 0 with part count checked before split; regression ceiling 1024 bytes. |

Test interfaces were those requested in the review: Link, OpenClaim/ClaimRef/Consume, JWS/HPKE and identity APIs, grant validation, and envelope Open/replay/routing. No runtime mock is used as evidence of native execution.

T-1 now sends each mutated routing field to HPKE open directly. Unsupported version/suite/features also retain their public early-decoder rejection; supported mutations set expected transport values to the changed header and must reach `decryption_failed`. T-2 changes exactly one byte of each original signed payload field without remarshal, preserving the original protected header and signature. Receiver tests hand-sign input to bypass sender validation. Negative tests assert stable codes, including grant/certificate window containment, future creation, recipient key mismatch `needs_verify`, sender pin mismatch, and recipient key epoch mismatch under valid AAD.

## Reproduction

Toolchain: Go 1.27.2; tool versions are pinned in the [Makefile](../Makefile). The Makefile defines the test, build and lint commands. Local cache paths used in this environment:

```sh
export GOCACHE=/tmp/peerturn-go/cache
export GOMODCACHE=/tmp/peerturn-go/mod
export GOLANGCI_LINT_CACHE=/tmp/peerturn-go/lint
make ci
```

Short fuzz command, repeated for the targets listed below:

```sh
go test ./internal/envelope -run '^$' -fuzz '^FuzzOpen$' -fuzztime=5s
```

| Package | Changed-boundary fuzz targets | Exit |
| --- | --- | --- |
| crypto | `FuzzVerify` | 0 |
| identity | `FuzzCertificateDecoders`, `FuzzVerifyChain` | 0 |
| envelope | `FuzzOpen` (valid admission and valid gap seeds) | 0 |
| protocol | `FuzzDecodeMessage` | 0 |
| grant | `FuzzGrantDecode`, `FuzzGrantVerify` | 0 |
| invite | `FuzzParseLink`, `FuzzDecodePacket`, `FuzzDecodeClaimPacket`, `FuzzDecodeSignedContent`, `FuzzOpenDescriptor`, `FuzzOpenClaim`, `FuzzSignedDescriptorPayload`, `FuzzSignedClaimPayload`, `FuzzSignedProofPayload` | 0 |

An independent `gpt-6-astra xhigh` security pass reviewed the finished production diff and new untracked files: no critical, high or medium findings, and no blockers. It confirmed the requested M-1/M-2, L-1–L-7 and T-1–T-5 boundaries without repeating successful author tests. Remaining low item from the original review: `Descriptor.EnrollmentVoucher` is still an exported string, so callers must not log whole descriptors. Voucher representation/redaction is separate from the requested `ik`/`ek` fix and remains follow-up work; generic Link formatting is covered here.

Final `make ci`: **exit 0**. Formatting, tidy, build, vet, race tests for all ten packages, golangci-lint (0 issues), govulncheck (0 reachable vulnerabilities), actionlint, and GoReleaser configuration checks passed. Govulncheck additionally reports one advisory in a required module outside imported/reachable packages. The initial CI attempt exited 2 at lint's exhaustive switch check; three already-rejected control kinds were made explicit in the routing switch before the successful final run. No suppression was added.

The content check found 53 changed files, no file above 400 lines, and 53 valid local Markdown links; `git diff --check` exited 0. These are repository checks, not evidence of a live deployment or delivery.

## Integration responsibilities

M1a contains pure decisions. M1b must serialize retained invite results, pending ciphertext/snapshots, dedup, inbox and budgets atomically, expire pending ciphertext, enforce aggregate disk/channel quotas, and look up incoming conversation IDs even for root messages. A time-independent claim reference is only a retained-result lookup key; a cache miss still needs current-time validation. Current device/session authority comes from caller-selected chains and pins; no additional revocation registry is implemented here.

A responder routing decision does not prove a qualified sandbox, secret isolation, snapshot, budget reservation, redaction or native execution. Those remain adapter/storage acceptance work. `coauthor` remains strong trust with the receiving agent's normal capabilities; peer text may influence later owner turns. No exactly-once effect, FS or PCS claim is made.
