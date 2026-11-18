# Changelog

Notable user-facing changes will be recorded here, following [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versioned releases follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

This release is about a live conversation between two people's agents: a
reply now reaches the other session the way a new message does, and nothing
old silently stops delivery.

### Changed

- A reply under a `coauthor` grant wakes the receiving session like a new
  `coauthor` message. Before, `peer_reply` only stored the reply in the inbox,
  so the other side saw it on its owner's next prompt or `peer_inbox`. The
  receiver's turn budget counts these replies, and a reply chain still ends
  after 8 hops. `inform`, and a reply under an `ask` grant, still do not wake.
  Codex receives such a reply in its live queue as well. The receiver decides:
  a newer receiver wakes on a reply from an older sender, while an older
  receiver keeps storing replies without waking until it is upgraded.

### Fixed

- An uncertain delivery left on a revoked link paused every later delivery
  into the session, with no word in `status` that delivery had stopped. It now
  stays on record for `peerturn delivery resolve` but no longer pauses other
  links. An uncertain delivery on a live link still pauses delivery, and
  `status` now says so: "Delivery into this session is paused".
- After an upgrade from a release that did not remember the bound session in
  the plugin, the daemon stayed down after a reboot until someone ran a
  `peerturn` command in that session. `peerturn setup` now tells the plugin
  which session is bound (option `peerturn_bound_session`), so that session
  alone brings its helper back. Other sessions still start nothing. The hint
  only allows a launch: the daemon checks the binding and a fresh native proof
  as before, and a session it refuses never relaunches from the hint.

## [0.2.1-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.2.1-preview) — 2026-11-16

This release is about cost in multi-session work: PeerTurn no longer runs in
Claude Code sessions that do not use it, and comes back by itself after a
reboot.

### Changed

- The Claude plugin starts its helper only in a session that uses PeerTurn:
  one bound before, or one where the owner runs a `peerturn` command, pastes
  an invitation link or a PeerTurn MCP tool is called. Before, every open
  session kept a helper process polling the daemon. Other sessions now keep
  only the idle stdio MCP proxy. A session the daemon refuses is forgotten.
- A helper waiting for the daemon slows its probes from 200 ms to 2 s. Before,
  after a reboot every open session checked the daemon and its native socket
  five times a second, without end.
- The plugin no longer has the classic `UserPromptSubmit` command. It did
  nothing, yet started a shell and a PeerTurn process on every prompt of every
  session.

### Security

- Build with Go 1.27.4, which fixes `GO-2026-6859`: a header race in
  `net/http` `MaxBytesReader` could crash an HTTP/1 server. The relay and the
  helper's loopback endpoint use that server. The Docker builder is pinned to
  the official 1.27.4 image by digest.

### Fixed

- After a reboot the daemon stayed down until someone ran `peerturn connect`.
  A helper now asks once to start the existing profile's daemon, so resuming
  the bound session brings PeerTurn back. No profile is ever created this way.
- A runtime directory removed by a crash or temporary-directory cleanup made
  clients reject the daemon pointer as unsafe, so the helper gave up without
  starting the daemon. A missing directory now reads as an offline daemon.
- The plugin recognises `command peerturn`, `env NAME=value peerturn` and a
  quoted path to the executable. Before, such a `connect` in a new session
  started no helper and failed after 20 seconds with `needs_rebind`.

### Upgrading

- Run `peerturn upgrade`, then `/reload-plugins` or restart open Claude Code
  sessions: a running session keeps the old plugin until then.
- The plugin learns which session is bound when the binding is next
  confirmed. In the bound session, run `peerturn status` or call `peer_status`
  once after upgrading; from then on it starts with the session.

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

Releases 0.1.0-preview through 0.1.4-preview are in the [0.1 changelog](docs/changelog-0.1.md).
