# Changelog

Notable user-facing changes will be recorded here, following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versioned releases follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.1-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.1-preview) — 2026-10-23

Prepared for release; publication requires the owner's approval.

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
