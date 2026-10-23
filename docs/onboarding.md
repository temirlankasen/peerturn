# First-run setup and honest failures

PeerTurn remains pre-alpha. Native exchange is qualified only on Linux with
the pinned runtimes; live macOS exchange is not yet qualified. Installing or
registering components does not establish native binding or message delivery.

## Runtime preflight

`peerturn setup claude|codex` queries the selected PATH executable with
`--version` before any plugin or MCP installation command. The qualified
versions remain Claude Code **2.1.306** and Codex CLI **0.163.0**.
A mismatch, missing executable, failed probe or unrecognized/oversized output
returns typed `unsupported_version` and exit 6. Its bounded message identifies
the qualified and detected versions (`unknown` when detection failed), and asks
the owner to select the qualified executable on PATH and rerun setup.
Setup resolves PATH symlinks once and uses that same executable for the version
probe and every later setup command, even if an updater retargets the symlink.
An already running native session
still needs its own reload/resume and runtime/binding checks.

## Helper startup status

Claude's helper retains its existing ready frame and stderr diagnostic channel.
Before readiness, the mod recognizes only complete `peerturn: CODE` lines from
an allowlist, within a 4096-character buffer. It displays the code through
`ui.status`, never arbitrary stderr, error messages, tokens, paths or peer text.
After readiness, stderr cannot replace the lifecycle status.

| UI code | Next step |
| --- | --- |
| `runtime_unsupported` | Select Claude 2.1.306, then resume/reload the selected session. |
| `daemon_unavailable` | Start/check the daemon under the same private runtime environment. |
| `needs_rebind` | Check the native helper, reload/resume the same session and bind from its tool child. |
| `permission_denied` | Check private local runtime permissions and local authorization. |
| `peer_identity_failed` | Check the local IPC peer identity; do not weaken its checks. |
| `auth_failed` | Check local credentials and authority without exporting tokens. |
| `adapter_unavailable` | Startup failed without a recognized safe reason; inspect local configuration. |

An iterator ending after readiness still reports `adapter_stopped`. None of
these failures changes the owner's tools, model, effort or permission mode.

## MCP decision and verification

Both hosts support plugin MCP declarations: see the official
[Claude manifest reference](https://code.claude.com/docs/en/plugins-reference#mcpservers)
and [OpenAI MCP manifest reference](https://developers.openai.com/plugins/deploy/submission-errors#mcp-manifest-errors).
That packaging support does not establish current-session provenance.
The local `research/lab/results/macos-live-2` host captures in the main checkout
show Claude messaging environment present, but no `CODEX_THREAD_ID` or
`XDG_RUNTIME_DIR` in the host-started Codex MCP process.

The chosen fallback is explicit registration by `setup` after the observed
plugin content matches the verified inventory. It runs the host CLI with an
argv array, the absolute installed package executable and exactly `mcp --current`:

```text
claude mcp add --transport stdio --scope user peerturn -- /ABSOLUTE/peerturn mcp --current
codex mcp add peerturn -- /ABSOLUTE/peerturn mcp --current
```

This uses the documented [Claude stdio registration](https://code.claude.com/docs/en/mcp#option-3-add-a-local-stdio-server)
and [Codex MCP CLI](https://developers.openai.com/codex/mcp#configure-with-the-cli).
No repository `.mcp.json`, native UUID, token, test label, shell wrapper or
captured environment is written. Existing host CLI conflict/errors fail setup;
inspect the named entry with `<host> mcp get peerturn` before retrying.
Setup never interprets human-readable missing-entry diagnostics. Codex uses
`mcp list --json` to establish absence, then `mcp get peerturn --json` for an
existing entry's detailed settings. Claude 2.1.306 has no JSON get/list export:
setup reads its bounded native user configuration, checks local/project scopes
from the current directory through its ancestors and corroborates the result
with `claude mcp get peerturn`'s exit status (0 present, 1 absent). Exit 1 alone
never permits add. Malformed, oversized or ambiguous configuration, unsupported
policy locations and failed queries return typed `mcp_registration_failed`.
Exact user stdio entries on either host are skipped without writes. Codex also
accepts the documented name-only `env_vars = ["XDG_RUNTIME_DIR"]` forwarding.
Different command/argv, tool restrictions, disabled status, other environment or timeout settings
are conflicts, preserved without writes. Claude project/local entries and
managed/remote/MDM policy presence are conservatively refused; inspect the
effective scopes manually while preserving owner policy. Setup does not remove
policy files or rewrite an existing entry to make it match.
The [pinned get implementation](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/cli/src/mcp_cmd.rs#L955)
does not expose every approval setting; skipping an existing match preserves
those settings too. Native query diagnostics are discarded to avoid exposing
private host configuration.

After installing a newer verified PeerTurn version, the existing MCP entry can
still point to the previous version's executable. If only that path differs,
both paths use the installer's `~/.local/share/peerturn/<version>/peerturn`
layout and the existing version is older, setup returns typed
`mcp_upgrade_required` with an explicit owner step. It does not execute the
old binary, remove the entry or rewrite it. A different command layout or
additional configuration differences remain `mcp_registration_failed`.
Inspect `<host> mcp get peerturn` and preserve owner tool, approval and
environment policy before the owner replaces the entry:

```text
codex mcp remove peerturn
peerturn setup codex

claude mcp remove peerturn --scope user
peerturn setup claude
```

Use only the selected host's pair of commands. Rerun setup with the same
verified `--path`/`--sha256` options if those were supplied originally.
Restore Linux name-only `XDG_RUNTIME_DIR` forwarding when needed, then
reconnect MCP in the same selected native session. This configuration match
does not establish publisher authenticity, activation or native binding.
The plugins themselves remain free of MCP declarations so plugin-only installs
cannot be mistaken for the explicit registration and verification sequence.

Registration supplies no authority and never falls back to an explicit preview.
The MCP handshake accepts the informational
[Implementation fields](https://modelcontextprotocol.io/specification/2025-11-25/schema#implementation)
including description, website URL and icons, plus unknown informational
client/capability fields. It bounds initialize parameters to 16 KiB, validates
known field types and rejects duplicate keys. It never fetches icons or URLs,
acts on client capability claims or treats metadata as instructions. Tool
arguments and authority-bearing data retain strict decoding.

### Claude host proof

Claude 2.1.306's captured MCP environment includes
`CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN` and
`XDG_RUNTIME_DIR`; the fixture stores the initialize request only, with no
credentials. `currentMCPBinding` calls `BindCurrentEvidence`: the daemon issues
a nonce, the MCP process authenticates to the private native messaging socket,
and the native helper confirms that nonce for the selected session. The
resolved binding epoch must match the subsequent daemon capability handshake.
A partial messaging environment, failed challenge or changed/missing epoch
fails closed before protocol output. If both messaging variables are absent,
discovery can initialize without authority, but Claude tool calls fail closed:
only the qualified Codex profile can use deferred binding. No UUID is taken
from initialize metadata.

The regression fixture exercises challenge, captured initialize, tool listing
and `peer_status`, including replaced/missing epochs. This verifies the code
path with synthetic local sockets; a fresh native host rerun is still required.

### Codex host proof

The exact source tag `rust-v0.163.0` resolves to commit
`83086188ae0614bd882818ff1aa23f3194962012`. This release adds
`params._meta.threadId` from the host's `sess.thread_id` to each MCP tool call:
[call construction](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/core/src/mcp_tool_call.rs#L521)
and [metadata insertion](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/core/src/mcp_tool_call.rs#L1394).
This field is outside model-supplied `arguments`. Optional
`x-codex-turn-metadata.thread_id` must agree with it. The common `sessionId`
may identify a root session for child threads and is never the selector.
The host-owned stdio envelope provides thread provenance within the existing
trusted same-UID native-host boundary; it is not protection against a malicious
local process impersonating the host.

Codex's [initialize request](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/codex-mcp/src/rmcp_client.rs#L1112)
has no thread. Host `mcp --current` therefore permits initialize and tool listing
without opening a daemon capability. A qualified Codex tool call must supply
host metadata before the proxy opens the existing authenticated daemon
handshake with that exact native thread. The daemon checks its stored live
Codex binding; a test label, neighbouring thread or model argument cannot
substitute for it. The proxy pins the thread and checks provenance again on
every tool call. Missing, malformed, conflicting or changed evidence fails
closed. Missing/malformed proof returns typed `binding_error`; conflicting
proof returns `binding_mismatch`, both with state `needs_rebind`. Tool-child
mode also requires matching host metadata on every tool call against its
inherited thread. Client name/version only select the supported parsing profile and
cannot authorize attachment.

MCP metadata establishes a native thread, not human intent. `peer_join` uses
only the pending capability recorded by that session's owner-prompt hook and
rejects delivered peer invitations; possession of a thread ID does not bypass
that requirement. No per-session UUID
or token is written to host configuration, and no neighbouring session is
discovered or attached.

### Owner invitation handoff — decision of 23 October 2026

The owner approved this change after the mac4 owner-origin recovery gap and
mac5 runtime-path and model-transcription failures. Those historical runs remain
unchanged; this decision and local fixtures do not qualify a fresh live macOS
exchange. The owner pastes the invitation into the selected session normally.
Its owner-prompt hook extracts and normalizes the capability with the same
strict invitation parser used by the CLI, then sends it over existing private
same-UID IPC. Claude binds it to the verified native binding epoch; Codex binds
it to the exact root thread later selected by host-owned `_meta.threadId`.

Call MCP **`peer_join` with `{}`**. It accepts no `text`, URL or fragment
argument, so the model never has to transcribe the invitation. The daemon holds
at most **eight** pending capabilities per session in memory for at most
**10 minutes**. An attempted join consumes its selected entry even on failure;
expiry, unbind/rebind, daemon stop and restart also clear pending entries. A new
distinct paste evicts the oldest entry and clears its bytes and timer; duplicates
do not extend expiry. Claude records eligible composer pastes before waiting on
operation serialization, so relay I/O cannot discard the paste.
An ambiguous submit still requires `in_doubt` reconciliation, never blind retry.
PeerTurn does not write pending capabilities or pasted prose to disk, logs,
status, receipts or audit. This pending store is separate from the existing
sealed recovery state retained after join has begun. The owner's paste still
appears in the native transcript and model-provider context.

With multiple pending invitations, `ambiguous_owner_invitation` returns only
nonsecret `invitations` entries containing `handle`, `relay_host`, `routing_hint`
and `age_seconds`; the hint is `/i/` plus six public routing-ID characters, never
fragment data. `peer_status.owner_invitations` exposes the same view. After the
owner identifies the intended invitation, call `peer_join` with its optional
`handle`. A handle is a random opaque selector, not a capability or authority
proof. No inviter name/label is trusted or displayed before the descriptor has
been authenticated. `no_owner_invitation` means there is no pending owner paste;
`owner_invitation_expired` requires a fresh paste. Hooks do not retain malformed
capabilities, so a malformed paste can leave no pending invitation. Direct CLI
parsing returns `malformed_invitation` and asks for the original unchanged;
never repair fragment values or reconstruct the link.

For Codex, recover in this order:

1. Keep the PeerTurn daemon running with the selected thread's existing native
   binding and the same private runtime environment as Codex. Resolve a separate
   `needs_rebind` or version failure first; a missing prompt alone does not
   require a new PeerTurn identity or native thread.
2. Open `/hooks`, review PeerTurn's `UserPromptSubmit` hook and explicitly
   enable/trust it. Reload/resume that same thread if required by the host.
3. After those steps, the owner must paste the full invitation again in that
   same native session. Call `peer_join` with `{}` within **10 minutes** of it.
   An agent's repetition of text from an earlier prompt is not a new owner paste.

A paste before daemon startup, an unsuccessful hook call, expired pending entry
or a daemon restart requires another owner paste after readiness. Hooks never
replay invitations from transcripts. MCP thread metadata, successful status and
hook trust alone do not establish a received owner paste. Same-UID native host
and hook IPC remain trusted local components, not independent human-origin
authentication. Missing provenance never becomes owner authority.
An invitation found in delivered peer content remains separately denied as
`peer_invitation_denied`.

For Codex, the daemon also requires native root-thread metadata and an active
`id == rootTurnId == hook.turn_id` from `thread/turns/list` with `itemsView: notLoaded`.
Internal Review can forward child events into root history, so membership alone
does not prove owner origin. Unknown or inherited root IDs fail closed, including
unqualified continuation chains; paste again in a fresh root user turn.
This has source/test evidence; a live `/review` qualification is still pending.
The pinned legacy server replays its rollout even for this one-turn metadata
page. Each proof is capped at 9 seconds (metadata 2 seconds, replay 8 seconds,
both inside that cap); offer and ACK share a 20-second helper budget under a
22-second host hook timeout. Extremely long histories can still fail closed;
use CLI join if the owner hook cannot finish rather than repeating the paste.

The source's [stdio environment allowlist](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/rmcp-client/src/utils.rs#L16)
includes `TMPDIR`. Explicit `XDG_RUNTIME_DIR` always takes precedence. Without
it, CLI/MCP can use the daemon's `runtime.path` pointer in the selected private state
directory, before macOS falls back to canonical `$TMPDIR/peerturn-<uid>`.
The daemon writes the pointer atomically and removes it on a clean stop. The
pointer is bounded, no-follow, same-UID and private-permission checked, as are
its pointed runtime directory and socket. A hostile or malformed pointer fails
closed without fallback or socket scanning. There is no shared `/tmp` fallback.
MCP resolves lazily until its first successful hello, then pins that directory.
A later different selection returns `needs_rebind` without contacting it; bye
uses the pinned directory. Before hello, late daemon startup can still recover.
The pointer only locates IPC; exact native binding still supplies authority.
`env_vars` cannot manufacture a current thread.

For `unsafe_runtime` after a deleted temporary directory, restart `peerturn daemon`
with the intended runtime environment. The daemon computes its own directory
from explicit `XDG_RUNTIME_DIR`, then the platform default (macOS `TMPDIR`), and
republishes `runtime.path` only after listening and taking the state lock.
Clients keep rejecting stale targets until that publication. A malformed pointer,
unsafe permissions or symlinks still require correcting the local path/permissions;
do not bypass checks or scan for another socket. Then paste the invitation again.

On Linux, name-only `XDG_RUNTIME_DIR` forwarding remains an alternative to the
private-state pointer; the variable is absent from Codex's default allowlist.
The pinned 0.163.0
[add command](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/cli/src/mcp_cmd.rs#L128)
accepts only `--env KEY=VALUE` and does not expose a name-forwarding flag;
`run_add` explicitly saves an empty `env_vars`. Setup therefore cannot register
name-only forwarding through that CLI and never captures the current value.
After setup, manually add this line to the existing `[mcp_servers.peerturn]`
table in the Codex user config, preserving its command, argv and owner policy:

```toml
env_vars = ["XDG_RUNTIME_DIR"]
```

Start/resume Codex from the same runtime environment as the daemon, with that
variable set, then reconnect MCP. Without forwarding (or if the host variable
is unset), Linux needs a valid private-state runtime pointer; otherwise it
returns `runtime_unavailable` on tool calls. Codex initialize/tool discovery
does not require a runtime; calls resolve again until the first hello. There is no Linux
`TMPDIR` fallback. Rerunning setup accepts this exact
name-only forwarding and skips the matching entry without writes. Additional
forwarded names or saved environment values remain conflicts.
The same-UID, no-follow and private-directory/socket checks still apply.

After setup, perform these steps in the **same selected native session**:

1. Activate the plugin through normal hook trust and reload/resume. Initialize
   the native profile and keep its daemon running under the same private runtime
   environment. For Claude, run `peerturn session bind --current` from its tool child.
2. Open `/mcp` and reconnect `peerturn` after binding is ready. Reload/resume that
   same session if the host requires it; do not create a replacement session.
3. Confirm `peer_join` is listed. Call `peer_status` with `{}` and compare its
   `session` to `peerturn status --current --json` run from that session's tool child.
   Require `binding: native_claude` or `binding: codex_shared_daemon` as appropriate,
   and `capabilities.attach_existing: true`.
4. Tool listing alone is not binding evidence: Codex binds only on its first
   proven tool call. If native proof is unavailable, report the typed failure
   and stop agent joining. The owner may use the existing
   [interactive current-session alternative](install.md#human-stdin-and-private-file-alternative):
   run `peerturn join --current --link-stdin` in that selected session's tool
   child, and supply the invitation through stdin. If interactive stdin is
   unavailable, only the owner may prepare the private file described there
   and give its path to the agent. Never embed the invitation in a tool command,
   save a token/UUID in config or substitute a test label.

The Codex tools/call fixture is derived from the exact release source, not a
live wire capture. The previous macOS captures prove startup environment only.
This fix and its synthetic regression tests do not qualify a fresh host MCP
run or live macOS exchange. No live agents or relay exchange were run here.
