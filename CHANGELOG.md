# Changelog

Notable user-facing changes will be recorded here, following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versioned releases follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

Prepared notes for **0.1.4-preview**; its tag, signed assets and publication are
still pending.

### Fixed

- Align installer, guidance and relay-page release pins, plus native plugin
  versions, for the 0.1.4-preview candidate.
- Claude hooks use literal native API calls accepted by the module loader,
  fixing the first-run loader rejection in the published 0.1.2-preview package.
- Cold Claude MCP can discover tools before daemon startup or binding, return
  `needs_rebind`, then recover on the same connection after the first proven
  binding. Recovery is model-free; an already pinned epoch cannot follow a
  replacement session.
- An acknowledged idle invitation polls claim GET without repeating publication
  POST and exhausting the default eight-nonce budget. Confirmation is transient,
  bound to exact retained publication material, and set only after validated
  receipt and required enrollment save. A new claim requires fresh publication
  revalidation before grants or peer state; failed retries back off without
  clearing diagnostics or stopping other peers' Fetch/ACK.

### Added

- Claude-only `peerturn connect`: initialize or reuse the selected profile,
  start or reuse its daemon, prove one native binding and return one invitation.
  Existing identities, storage policy and binding epochs are preserved;
  uncertain bind submission remains `in_doubt` without blind replay. Codex keeps
  its documented shared-daemon setup; an environment thread ID is not authority
  for a CLI connect fallback.
- Operator opt-in relay `self_service`, default **closed**, with atomic signed
  own-mailbox publication, TTL ≤600 seconds and durable per-key quotas
  (4 active / 20 daily / 128 lifetime invitations; 100 inviter keys by default).
  Discovery is only a compatibility hint. Signer-derived mailbox receipts,
  author/admin invitation revoke and durable admin inviter block preserve
  admission fences; no general public mailbox signup is added.

### Upgrade and qualification limits

- Relay SQLite schema v2 migrates v1 transactionally on open, including in closed
  mode. Back up consistently before an operator-authorized upgrade and retain a
  compatible binary. No automatic downgrade, quota reset or live migration is
  claimed. See [operator policy](docs/operators/self-service.md).
- Earlier source full CI passed; this candidate still requires its own release
  gates. Claude Code 2.1.308 native checks exercised
  fresh nonadmin link creation, current MCP recovery, binding reuse and 25 seconds
  of idle waiting. A trust-warning ordering deviation means this is functional
  evidence, not full procedural acceptance.
- Signed-package installation, Haiku, two-host acceptance, owner-pasted join and
  model reply for this candidate remain pending. Earlier pinned lab results keep
  their original scope; they do not qualify these new signed assets.

## 0.1.3-preview — tag only, not published

The immutable `v0.1.3-preview` tag points to `73db586`. Validation failed before
release creation or asset publication because the relay invitation page still
pinned v0.1.2-preview. The tag is retained unchanged; no signed release or
installed acceptance is claimed. The next candidate is 0.1.4-preview;
v0.1.2-preview remains the last published release.

## [0.1.2-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.2-preview) — 2026-10-29

### Changed

- Claude Code >=2.1.306 and Codex CLI >=0.163.0 are accepted without an upper
  version bound. Newer versions retain existing functionality and report
  `newer_unverified`; ask remains separately qualified and verified-only.
- Setup, status and doctor distinguish the selected executable from the actual
  running runtime, verification evidence and available capabilities.
- Bounded, model-free contract checks report `runtime_contract_changed` with
  recovery instructions. Unknown/below-minimum versions fail before setup or
  adapter effects; contract failures do not restrict the owner's normal turn.

### Added

- Six-hour, main-only runtime discovery and disposable Linux/macOS qualification,
  with packaged evidence promotion, protected-main bot PR fallback and signed
  patch-release dispatch. Offline tests do not establish a successful live run.
- A 72-hour upstream-age gate before verified promotion, main-only Environment
  secrets and pinned upstream identities. Qualification code can access its
  dedicated provider key; only server-side quotas bound spend and exposure.
  See [operation](docs/operators/runtime-qualification.md).

### Security

- The daemon verifies Claude caller ancestry using kernel process credentials
  before exposing an already-confirmed native binding epoch.
- Verification records are platform/profile-specific; newer builds do not
  inherit an older runtime's ask qualification.
- Qualification, CI and releases do not restore shared Actions caches. Release
  jobs download clean modules and verify them before building signed artifacts.
- Ambiguous native submissions remain `in_doubt`, without automatic replay.
  Known-bad information arrives through PeerTurn updates, not a network manifest.

## [0.1.1-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.1-preview) — 2026-10-23

Published on 24 October 2026; the original release preparation date is retained above.

### Added

- Automatic stdio MCP registration during `peerturn setup claude|codex`, with explicit activation checks and owner-guided upgrades that preserve existing MCP entries and policy.
- Owner-prompt invitation handoff: `peer_join({})` takes no secret; several pending invitations require an owner-selected nonsecret handle.
- Private runtime pointer and lazy MCP runtime resolution: discovery can precede daemon startup, without restarting the Codex MCP session.
- Typed startup states and actionable first-run errors instead of misleading readiness or generic daemon failures.
- Public macOS 27.0 arm64 live evidence for pinned Claude Code 2.1.306 / Codex CLI 0.163.0: setup, owner-pasted link join, coauthor, no-wake inform, signed ask refusal and effective revoke. The full six-scenario run retains its failed reverse-join verdict for an already connected pair.

### Changed

- Installer downloads use a stall-based limit (below 1 KiB/s for 60 seconds), with a one-hour total ceiling for progressing downloads.
- Relay defaults accommodate clients sharing a NAT: 32 connections per IP, 600 requests per minute and a burst of 120; a separate per-source execution cap preserves fairness. These defaults do not establish broader Internet/NAT or performance qualification.

### Fixed

- Setup rejects unsupported or auto-updated host versions before side effects, resolves canonical executable/package paths and refuses conflicting or uncertain MCP configurations.
- Claude MCP initialize accepts host informational metadata; Codex binds tool calls by host-owned `_meta.threadId`, with provenance checks on every call.
- Grant catalogs report `effective_revoked`, including remote revocation, without creating read-side receipts.

### Security

- Codex internal subagents, including `/review`, cannot record owner invitations or acknowledge owner-bound inform notices; only the verified bound root user turn can do so.
- Invitation capabilities stay in bounded, expiring daemon memory; status exposes only safe handles/hints. The owner's original paste still reaches the native transcript and model provider.
- MCP pins its runtime after the capability hello and refuses runtime replacement with `needs_rebind`; unsafe pointers fail closed without socket scanning or automatic mutation retries.
- `coauthor` remains strong trust with normal receiving-agent capabilities and possible influence on later owner turns. Ask receivers remain closed on macOS and Codex; only the separately qualified Linux Claude profile can answer.

## [0.1.0-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.0-preview)

### Added

- Initial Go CLI skeleton with help and version output.
- M1a pure protocol core: HPKE/JWS, pinned identity chains, recipient-issued grants, encrypted invitation objects, and envelope/replay decisions with published vectors and bounded fuzz targets.
- M2 durable ciphertext relay: `peerturn relay --config`, signed HTTP mailbox/invitation/permit/ACK APIs, notify-only WebSocket streams, SQLite WAL custody, quotas, Docker packaging and operator documentation.
- Approved design and M0-M7 acceptance plan for session-scoped agent collaboration.
- Preserved prior-art research and pinned Claude/Codex and tool laboratory evidence.
- Initial development gates, repository automation, and community documentation.
- Durable encrypted client state, private local daemon, CLI/MCP and exact current-session coauthor adapters for Claude Code 2.1.306 and Codex CLI 0.163.0 shared daemon.
- Faithful English specification with Russian originals, sanitized public prior-art and lab summaries, and a publication marker checker.
- Static invite instructions, full pasted-text stdin joins, verified pinned Linux/macOS installer, packaged plugins and `peerturn setup claude|codex` with explicit host activation steps.

### Fixed

- Bind relay request signatures and notification tickets to the configured public authority; check verified endpoint admission before durable replay writes.
- Pin the Docker builder to Go 1.27.3 and assert its toolchain version during builds, including the `GO-2026-6809` x509 fix.
- Isolate relay source, tenant and replay budgets; reserve physical storage for owner control, bound metadata retention, and restrict notification streams to readers.

### Decisions

- Project name: PeerTurn. Original code license: Apache-2.0.
- Develop an early `coauthor`-only preview in parallel with M0 feasibility work.
- Keep `inform` and `ask` disabled until the deterministic peer-turn guard is proven.

This preview is published. Client onboarding and experimental coauthor adapters are implemented; ask and the complete peer-turn guard remain unavailable. Local installation evidence does not establish public OIDC release verification, Internet/NAT, clean VPS or performance qualification. Go 1.27.3 is required; see [M2 evidence](docs/operators/m2-evidence.md) and the [installation lab](research/lab/results/install-by-link/README.md).
