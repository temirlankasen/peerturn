# M5 ask qualification

The M5 product verdict was **closed on Claude Code and Codex**. M5 found
concrete failures in candidate profiles; it does not qualify a safe fork responder.
`authority_secrets_isolated` and `fork_responder` remain false. Sending ask returns
`responder_unavailable` with `protected_path_policy_failed`, without a paid run or
live-session fallback. Detailed probe artifacts remain in the private research
archive; the bounded public observations are summarized below.
[M6](m6-ask.md) supersedes this closed preparation with the Claude-only
Linux ask responder and its receiver quotas.

## Results

| Candidate | Observed result | Admission verdict |
| --- | --- | --- |
| Claude Code 2.1.306, Linux | Direct/normalized/symlink reads denied; project reads work. A project hardlink discloses the protected fixture. Managed hooks still execute with private `disableAllHooks` and `--safe-mode`. | Closed: path rules and tool removal do not isolate authority or managed effects. |
| Codex 0.163.0, Linux process keyring | Possessor-only process-ring key is unreadable by tested child/sibling. Linking it to the inherited session ring discloses it. Exec/restart cannot recover a solely process-owned key. | Closed: no qualified persistent bootstrap secret. Retaining `seal.key` does not solve that problem. |
| Codex 0.163.0, outer bwrap | A fresh `/dev` allows actual fork tool execution under the inner sandbox. Protected paths are hidden; writes and tool connections fail. A project hardlink remains readable. Fresh PID/proc profiles fail in this lab. | Closed: nested sandbox feasibility is insufficient for authority isolation. |
| Codex, macOS | A separate throwaway-keychain host probe is prepared; no host Keychain/Seatbelt acceptance has been run. | Closed: ACL/deputy, process and runtime-extension boundaries remain unproven. |

These are bounded observations on pinned runtimes, not an impossibility claim
about every operating system or a future separate-user broker. No real authority
values were used as probe canaries.

## Product changes

`Daemon.Open` records a typed negative startup preflight. Status/doctor report
the storage mode, check time and stable failure reasons without paths or keys.
There is no switch that converts native feasibility into secret isolation.

The dormant Claude runner requires explicit state/config/runtime paths. Its deny
rules include supplied roots and their resolved ancestors, plus additional
daemon/default/configured roots assembled by `responderDirectories`. This fixes
the previous Linux-only guesses; it does not qualify hardlinks or managed hooks.

Both adapters redact known values in literal, exact whole-value hex/Base32/Base64
encodings at aligned boundaries, and Go's URL/JSON/HTML escaping forms, including
binary key encodings. Overlapping matches are merged.
This is a bounded set of encodings, not detection of every possible transformation.
Chunking, re-encoding, ciphers and paraphrases remain residual risks. Known-value
redaction is not a replacement for protecting signing/admission authority before
spawn. No automatic fork reply uses this policy today.

Bound Claude and Codex receivers return a signed, encrypted preflight rejection
for a valid remote ask. The local terminal receipt and reverse-channel outbox
notification commit in one store transaction. Journal recovery publishes only
notifications whose outbox commit exists; a failed transaction rolls back the
reserved sender sequence. Unbound preview sessions still reject unsupported
scopes at ingress. An unavailable return grant preserves the local rejection.
Full reciprocal control queues defer that peer's rejection for periodic retry;
they do not prevent existing ciphertext from draining or other native work from
progressing, and a failed reservation does not create a wake loop.

## Remaining acceptance work

No candidate passed Phase 1, so enabling ask, migrating key storage, adding paid
budget reservations and automatic signed fork replies are deferred. Existing
deadlines/output/concurrency limits in the dormant runners are not a durable
per-peer hourly quota or accounting qualification. The signed rejection path is
not a successful fork answer/reply path.

A subsequent implementation needs a durable bootstrap secret inaccessible to
fork tools and process inspection, launch-time negative self-checks, mandatory
extension exclusion respecting managed policy, exact snapshot/model continuity,
durable per-peer reservations and revoke-serialized reply custody. Linux keyring
alone is not durable storage; directory masking alone does not protect aliases.

Full context may contain other peers' messages and previously pasted credentials.
An ask grant must not be presented as cross-peer confidentiality. Even a future
qualified profile cannot guarantee that output heuristics recognize transformed
or previously unknown secrets.

No live ask end-to-end success or unchanged Claude M5 parent transcript is claimed.
The Linux evidence distinguishes native final output, actual tool execution and
unchanged synthetic parent rollout hashes. The macOS script requires a separate
human-run host qualification; its expected denial is not a measured result.
