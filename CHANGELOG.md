# Changelog

Notable user-facing changes will be recorded here, following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versioned releases follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

## [0.2.0-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.2.0-preview) — 2026-11-06

This release is about convenience: after installation the owner types no
slash commands in the normal path and never pastes a link twice; errors say
what happened and what to do; the daemon writes an operator log; links live a
day; one command upgrades everything; and `peers` shows whether the other
daemon is alive.

### Added

- `peerturn upgrade [--check]`: looks up the latest release, verifies the
  publisher signature with cosign (same trust as the installer), installs it,
  reruns `peerturn setup` for the hosts on PATH and restarts the daemon. The
  profile, peers and links are kept. Already open sessions keep the previous
  plugin until `/reload-plugins`; new sessions use the new one. The daemon
  looks up releases once a day and `status`/`doctor` say when an upgrade is
  available (`PEERTURN_NO_UPDATE_CHECK=1` disables the lookup).
- `peerturn doctor` without `--json` prints a checklist: relay, binding, prompt
  hook, certificate, every link with its presence and messages waiting at the
  relay, deliveries in doubt, and a "Do next" list. `status` carries the same
  facts: `certificate_expires_at`, `owner_invitation_hook`, `in_doubt` message
  IDs, `latest_version`, `warnings` and a one-line `summary`.
- `peers` reports `link_expires_at`, `presence` (`online`, `offline` or
  `unknown` for peers older than 0.2.0), `peer_version`, `last_seen_at` and
  `waiting_on_relay`. Linked daemons exchange an encrypted heartbeat every ten
  minutes; older peers skip it as one unknown mailbox slot.
- A daemon log at `<state dir>/logs/daemon.log` (4 MiB, three files kept) with
  codes, identifiers and stages: registrations, bindings, link renewals, lost
  sequences, rejected packets, failed owner commands, update lookups. It never
  holds bodies, links, grants, nonces, tokens or keys.
- `peerturn invite list` and `peerturn invite revoke ID`; `--lifetime` (for
  example `10m`, `24h`) and `--reuse` on `connect` and `invite create`;
  `peerturn help <command>` and `--help` on every subcommand.

### Changed

- Every error names its cause and the next step. `invalid_arguments` quotes the
  command's usage. `needs_rebind` says the helper rebinds by itself.
- The Claude plugin helper survives a daemon restart: it registers again and,
  for a session the owner bound before, proves the binding itself through the
  native socket. No `/mcp` reconnect and no `peerturn session bind --current`
  in the normal path; a first binding still happens through `peerturn connect`.
  The mod restarts a stopped helper with backoff and waits briefly for a
  starting helper before reading a pasted link, so a link pasted right after a
  restart is not lost. A link pasted while the proof is pending is kept.
- Invitations live up to 24 hours (single use, revocable) instead of ten
  minutes. `peerturn connect --reuse` prints the latest unused link again
  instead of minting another. A relay from before 0.2.0 caps invitations at ten minutes:
  the daemon notices the refusal, mints a ten-minute link instead and reports
  `relay_max_lifetime`; peers before 0.2.0 accept ten-minute links only, so
  use `--lifetime 10m` for them. A relay upgraded to 0.2.0 allows a day
  (`self_service.invite_lifetime_seconds`, default 86400).
- `peerturn setup` prints five lines; the full activation contract is behind
  `--verbose`.

### Upgrading

- Run `peerturn upgrade` on both sides. 0.2.0 talks to 0.1.9: links, renewals
  and lost-sequence floors are unchanged; heartbeats and day-long links need
  0.2.0 on the peer and, for the latter, on the relay.
- Sessions that were bound before keep their binding across the restart; the
  helper proves it again by itself.

## [0.1.9-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.9-preview) — 2026-11-04

### Fixed

- A profile no longer stops working a day after `peerturn init`. The session
  certificate lasted 24 hours and was never renewed, so every invitation and
  join then failed with `expired`, and a restarted daemon refused to start.
  The daemon now reissues it once it lapses (at start or during maintenance),
  or shortly before an invitation or join when no grant is live, keeping the
  root, device, session ID and keys under the next session epoch. A
  certificate now lasts a year, so this happens once a year.
- A link no longer dies after a day. Each side renews the grant it gave the
  other while the link is in use, once less than half of its 30 days remain,
  and sends the renewal to the peer; packets sealed under the previous grant
  stay valid until it expires. Revoking either grant ends the link.
- A message no longer expires after an hour. It now waits for an offline peer
  for nearly seven days.
- A lost message no longer blocks the channel. When an own message expires
  without reaching the peer, the sender tells the peer, which stops waiting
  for it and admits later messages; previously they stayed parked behind the
  gap (`sequence_gap`), or the sender needed a new invitation (`needs_rebind`).
- A new invitation from an already known peer now relinks the pair instead of
  failing with `conflict`: after the peer moved to another Claude session, after
  either side renewed its certificate, or while the old link is still active.
  The old link's grants are revoked locally, so its unread messages leave the
  inbox and its unsent packets are dropped. Both sides need 0.1.9.

### Upgrading

- Links made with 0.1.8 or earlier end at the first invitation or join after
  the upgrade, which renews the day-long certificate to a year. Connect each
  peer once more with a new invitation; both sides need 0.1.9.

### Changed

- The Claude plugin stays silent in sessions that never bound PeerTurn: no
  status line in every Claude session. `peerturn connect` and `peerturn doctor`
  still report setup failures where they run.

## [0.1.8-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.8-preview) — 2026-11-04

### Fixed

- `peerturn setup claude` no longer refuses MCP registration because Claude Code
  wrote an empty `remote-settings.json` (`{}`) for an account without remote
  policy. A non-empty or non-regular managed or remote settings file still
  stops setup before changes.

## [0.1.7-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.7-preview) — 2026-11-04

### Fixed

- Upgrading an existing installation works: `peerturn setup claude|codex`
  replaces PeerTurn's own older marketplace (an older package directory under
  `~/.local/share/peerturn`, or the public repository at other contents), then
  reinstalls the plugin; for Claude it also replaces its own default MCP entry.
  Previously setup stopped with `marketplace_conflict`, and Claude refused
  `/plugin marketplace add` over the old source. Foreign sources still conflict,
  and a Claude plugin the owner disabled reports `plugin_disabled` unchanged.
  The older Codex MCP entry stays an owner step (`mcp_upgrade_required`),
  because `codex mcp get` hides its approval policy.
- `peerturn connect` no longer reuses a daemon left running from another
  PeerTurn version; it explains how to restart it. `daemon status` reports
  `peerturn_version`.

## [0.1.6-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.6-preview) — 2026-11-03

### Added

- Native plugin-command installation: the repository is a Claude Code and Codex
  marketplace (`/plugin marketplace add temirlankasen/peerturn@v0.1.6-preview`,
  `codex plugin marketplace add temirlankasen/peerturn@v0.1.6-preview`).
  `peerturn setup` adopts that marketplace only when its files equal the
  verified release, and otherwise reports `marketplace_conflict` without changes.

### Fixed

- Codex installs the Codex plugin from the repository marketplace instead of
  the Claude plugin.
- An installed Claude plugin whose executable is not configured yet keeps the
  owner's prompt ordinary instead of failing every prompt with an unset option.
- The Codex plugin added from the repository uses only the verified release
  helper in its default location; it never searches `PATH`.

## [0.1.5-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.5-preview) — 2026-11-03

### Fixed

- Claude sessions that are not bound to PeerTurn no longer show repeated
  `binding_lost` / `needs_rebind` status after every Bash call.
- A slow native session lookup no longer restarts a live, bound Claude helper
  and rotates its binding epoch; concurrent hooks share one startup.
- `peerturn mcp --current` renews a changed binding epoch of the same native
  session from fresh native proof and a matching hello, instead of staying on
  `needs_rebind` until a manual `/mcp` reconnect. A different session or a
  stale hello is still refused, and ambiguous effects are never replayed.
- `peer_reply` / `peerturn reply` accept a parent message already delivered to
  the native session, instead of returning `not_found`.
- Bare `peerturn grants` lists grants; help shows `grants [list]` and
  `grants revoke ID`.

### Changed

- Skill guidance: when peers share a repository, prefer a pushed branch, commit
  SHA or merge request reference over pasting whole files.

## [0.1.4-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.4-preview) — 2026-10-30

Published at `2026-10-30T13:13:24Z`; release validation and publication succeeded.
External verification passed for the installer's exact workflow/tag attestation,
the checksum signature using cosign 3.1.3 with the exact identity and issuer,
and the installer plus `darwin_arm64` archive digests. These checks authenticate
those artifacts; they do not establish completed installation or native delivery.

### Fixed

- Align installer, guidance and relay-page release pins, plus native plugin
  versions, for 0.1.4-preview.
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
- Earlier source full CI passed. Claude Code 2.1.308 native checks exercised
  fresh nonadmin link creation, current MCP recovery, binding reuse and 25 seconds
  of idle waiting. A trust-warning ordering deviation means this is functional
  evidence, not full procedural acceptance.
- Signed-package installation, Haiku, two-host acceptance, owner-pasted join and
  model reply for this release remain pending. Two fresh Haiku-requested
  installation attempts are waiting at normal workspace trust dialogs; model
  selection, preflight and installation are not yet proven. Earlier pinned lab
  results retain their original scope and do not qualify the new installed package.

## 0.1.3-preview — tag only, not published

The immutable `v0.1.3-preview` tag points to `73db586`. Validation failed before
release creation or asset publication because the relay invitation page still
pinned v0.1.2-preview. The tag is retained unchanged; no signed release or
installed acceptance is claimed. At the time of that failure, v0.1.2-preview
was the last published release; v0.1.4-preview followed.

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
