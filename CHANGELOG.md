# Changelog

Notable user-facing changes will be recorded here, following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versioned releases will follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once a public release exists.

## [0.1.0-preview] - Unreleased

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

No releases have been published. Client onboarding and experimental coauthor adapters are implemented; ask and the complete peer-turn guard remain unavailable. Local installation evidence does not establish public OIDC release verification, Internet/NAT, clean VPS or performance qualification. Go 1.27.3 is required; see [M2 evidence](docs/operators/m2-evidence.md) and the [installation lab](research/lab/results/install-by-link/README.md).
