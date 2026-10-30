# Open-ended runtime version support

Approved policy, 25 October 2026; original design inspected PeerTurn `c61b6f5`.
Step 1 implements local policy, admission, diagnostics and model-free contract
checks. Step 2 supplies main-only qualification/release automation; live acceptance
of that automation must come from its GitHub runs, never an offline fixture.
Historical lab evidence below retains its original scope and verdicts.

## Owner decisions (25 October 2026)

1. Minimum Claude Code **2.1.306**, Codex CLI **0.163.0**, with no upper bound.
2. Newer unverified versions get **full existing functionality immediately**,
   including automatic invitations and owner-boundary notices. Status remains
   `newer_unverified`. Ask is a closed, separately qualified feature and stays
   **verified-only**. Contract failures close only dependent capabilities with
   `runtime_contract_changed` and an actionable recovery step.
3. Automatically qualify new versions in scheduled/manual GitHub Actions runs
   **from main only**, using a dedicated small-budget LiteLLM repository secret.
   Never expose the key or run paid qualification in pull-request workflows.
4. A green qualification automatically updates packaged verified records and
   publishes a signed release; a failure opens an issue for a patch. Step 1
   performed no publication or paid live run. Known-bad versions reach
   installations through PeerTurn updates; no network manifest in this phase.

The owner explicitly accepted undetectable semantic changes: for example, changed
origin-kind or subagent-turn-ID meaning could make a non-owner link appear to be
an owner paste despite structurally valid fields. Startup tests cannot prove
those semantics. Qualification is bounded evidence; it never supplies authority.

## 1. Evidence and contract inventory

Approved minima: **Claude Code 2.1.306; Codex CLI 0.163.0**. Evidence:
[M3](../../research/lab/results/m3-claude/README.md),
[M3 corrections](../../research/lab/results/m3-claude/fixes/README.md),
[M4](../../research/lab/results/m4-codex/README.md),
[M4 corrections](../../research/lab/results/m4-codex/fix-20261022/README.md),
[mixed Linux run](../../research/lab/results/int-m3-m4/README.md), and
[mac7](../../research/lab/results/macos-live-7/README.md).
These cover Linux ARM64 and macOS 27.0 arm64 preview scenarios, not every OS,
origin, provider or feature. mac7's overall verdict remains **FAIL**: its reverse
join reused an already connected pair; no fresh reverse-pair success was proven.
No cheaper evidence establishes older minima. In particular, the pinned Codex
source adds host-owned tool-call `threadId` in 0.163.0; see
[onboarding evidence](../onboarding.md#codex-host-proof).
Owner-reported 2.1.308 / offered 0.164.0 are candidates, not new qualification.

The tables inventory runtime assumptions in current code, including dormant paths.
Symbols identify consumers; links are repository-relative. Existing limitations
in [M3](../m3.md), [M4](../m4.md), [M4 checks](../m4-checks.md) and
[security](security.md) remain. Notice-only inform is not a restricted peer turn;
this proposal enables neither restricted inform nor product ask.

### Claude Code

| Contract | Property protected | Code / symbol |
| --- | --- | --- |
| `session.version()` object with `version` and `base`; CLI `--version`; actual running process versus PATH binary | Correct compatibility diagnosis; no fabricated qualified version or unnoticed updater replacement | [register.js](../../adapters/claude/hooks/register.js): `start`, `launch`; [setup_runtime.go](../../cmd/peerturn/setup_runtime.go): `checkSetupRuntime`; [setup.go](../../cmd/peerturn/setup.go): `runSetup` |
| Plugin/module installation, options, activation; `session.start/end`, clear/resume without another start; `session.id()` | Bind the selected live UUID; close old helper/epoch; installation is not activation | [hooks.json](../../adapters/claude/hooks/hooks.json); `register`, `start`; [setup_observe.go](../../cmd/peerturn/setup_observe.go): `observeSetupPlugin` |
| `process.spawn` argv and streamed stdout/stderr; direct native parent; `http.fetch` response `ok/status/text`; `clock.every/sleep/cancel`; `env.get` provider hint | Private helper capability, bounded framing, lease renewal, failure recovery; provider hint grants no authority | `launch`, `request`, `poll`, `stop`; [server.go](../../internal/claudeipc/server.go): `Serve`, `ServeHTTP` |
| `CLAUDE_CODE_MESSAGING_SOCKET/TOKEN`; auth/user newline frames; `session.receive` with `origin.kind=peer`; consumed nonce before model input | Tool/MCP child and mod prove the same live UUID, instance and epoch; token stays local | [current.go](../../internal/claudeipc/current.go): `bindCurrent`; [native_peer.go](../../internal/claudeipc/native_peer.go): `newNativeGuard`, `connectNative`; [binding.go](../../internal/claude/binding.go): `Challenge`, `Confirm`, `MCPEpoch`; `register` |
| Socket kernel UID/PID/start identity equals helper's direct Claude parent, stable inode; macOS/Linux process inspection | Prevent socket substitution and stale process reuse; same cwd never binds | `newNativeGuardFor`, `connectedNativeProcess`; [native_peer_darwin.go](../../internal/claudeipc/native_peer_darwin.go); [native_peer_linux.go](../../internal/claudeipc/native_peer_linux.go) |
| `prompt.submit` hook carries host origin: composer, bridge, plugin, peer; composer alone can hand off invitations; composer/bridge can carry fixed notices; slash input excluded | Peer/plugin/bridge text must not authorize joining; notice contains no body | `ownerBoundary`, `register`; [claude_binding.go](../../internal/daemon/claude_binding.go): `recordClaudeOwnerInvites`; classic command emits `{}` because it lacks this proof |
| `turn.start/complete` main-agent discriminator is absent `agentId`; continuation `next(e)` and generation ordering; initial busy state unknown | Hold coauthor outside native queue while owner is busy; no child completion mistaken for root idle | `register`, `poll`, `deliver` |
| `prompt.submit({text})` without `asUser`; response `drop/error` versus exact `origin.kind=plugin`, `name=peerturn`, matching text digest | Literal coauthor admission, no owner impersonation; accepted is not sampled/completed; lost result is `in_doubt` | `deliver`; `ServeHTTP`; [claude_admission.go](../../internal/daemon/claude_admission.go); [claude_delivery.go](../../internal/daemon/claude_delivery.go) |
| `prompt.submit.context` and successful continuation; UI `status/toast` | Notice ACK only after accepted boundary; UI failure never blocks owner or counts as delivery | `ownerBoundary`, `register`, `status`; [claude_inform.go](../../internal/daemon/claude_inform.go) |
| Host MCP initialize/clientInfo/capability extensions, inherited messaging env, stdio lifecycle | Discovery grants nothing; nonce epoch must match daemon lease; reconnect cannot attach another session | [initialize.go](../../internal/mcp/initialize.go); [server.go](../../internal/mcp/server.go): `initialize`; [mcp.go](../../cmd/peerturn/mcp.go): `runMCP`; [fixtures](../../internal/mcp/testdata/README.md) |
| Plugin/MCP CLI commands and JSON inventory; `.claude.json`, legacy `.config.json`, project and managed precedence; `mcp get` exit status | Preserve existing owner configuration; fail rather than overwrite an unrecognized effective policy | [setup_observe.go](../../cmd/peerturn/setup_observe.go); [setup_mcp.go](../../cmd/peerturn/setup_mcp.go): `registerSetupMCP`; [setup_mcp_claude.go](../../cmd/peerturn/setup_mcp_claude.go): `observeSetupClaudeMCP` |
| **Dormant ask:** binary/version and managed-policy probes; resume/fork argv, settings/hooks/plugins/MCP removal, tool allowlist, stream result; journal UUID ancestry, `end_turn`, model/effort/system/tool shape, version per record | Snapshot continuity and authority isolation; none is established by a version comparison | [responder_startup.go](../../internal/claude/responder_startup.go): `CheckResponderProfile`; [responder_snapshot.go](../../internal/claude/responder_snapshot.go): `AcquireResponderSnapshot`, `verifyFork`; [responder_profile.go](../../internal/claude/responder_profile.go); [responder_configuration.go](../../internal/claude/responder_configuration.go); [responder_stream.go](../../internal/claude/responder_stream.go) |

### Codex CLI

| Contract | Property protected | Code / symbol |
| --- | --- | --- |
| CLI version and shared app-server initialize `userAgent`; experimental API opt-in, `initialized` per connection; short-lived WebSocket-over-UDS connections, notification draining and refused server requests | Detect actual server, not merely PATH CLI; avoid subscriptions and unintended server-request effects | [transport_rpc.go](../../internal/adapters/codex/transport_rpc.go): `Open`, `initialize`, `supportedAgent`, `Call`, `exchange` |
| `$CODEX_HOME/app-server-control/app-server-control.sock` alias to same-UID 0700 directory/0600 socket; stable socket incarnation | Exact shared-daemon binding; no embedded/new-daemon fallback or socket scan | [probe_socket.go](../../internal/adapters/codex/probe_socket.go): `DefaultSocketPath`, `probeSocket`; [binding.go](../../internal/adapters/codex/binding.go): `validateIdentity` |
| Tool-child `CODEX_THREAD_ID`; paginated `thread/loaded/list`; `thread/read` without turns, matching ID and idle/active status | Prove that the explicitly selected thread is live in that daemon, not a neighbor or stored-only thread | `Bind`, `Validate`, `requireLoaded`, `validateThread`; [current.go](../../cmd/peerturn/current.go) |
| `UserPromptSubmit` input: `hook_event_name`, `session_id`, `turn_id`, `prompt`, `cwd`, nullable `transcript_path`, `model`, `permission_mode`; optional `agent_id/agent_type` | Bounded strict schema; reject child fields even when null; do not read transcript paths or infer authority from cwd/model | [codex.go](../../cmd/peerturn/codex.go): `codexHookInput`, `valid`, `runCodexHookContext` |
| Internal Review can omit agent fields and share root session; root `thread/read`: ID=sessionId, parent null, source allowlist, active; latest `thread/turns/list`: `id=rootTurnId=hook.turn_id`, inProgress, itemsView=notLoaded, empty items | Exclude delegated/review/continuation input from owner invitation and notice handling; membership alone is insufficient | [owner_turn.go](../../internal/adapters/codex/owner_turn.go): `ValidateOwnerTurn`, `validateOwnerThread`, `validateOwnerTurnHistory`; [native_owner_turn.go](../../internal/daemon/native_owner_turn.go): `checkOwnerTurn` |
| Host-owned `_meta.threadId` on **every** tools/call, outside model arguments; optional sessionId and nested metadata consistency; initialize name `codex-mcp-client` has no thread | Deferred binding at first call, pin even refused thread selection, reject changes; root sessionId is not a child selector | [codex.go](../../internal/mcp/codex.go): `qualifiedCodexClient`, `codexToolThread`, `sessionBinding.Call`; [onboarding](../onboarding.md#codex-host-proof) |
| `thread/queue/add`: threadId, text input/text_elements, clientUserMessageId; echoed queuedSubmission/input and item ID; next-turn busy FIFO | Literal coauthor, correlation without dedup/exactly-once claims; ambiguous acceptance is fenced | [transport.go](../../internal/adapters/codex/transport.go): `Queue`; [types.go](../../internal/adapters/codex/types.go); [native.go](../../internal/daemon/native.go) |
| `thread/queue/list` bounded data; `thread/queue/delete` item ID and boolean deleted, including unloaded thread | Availability probe and revoke/dequeue evidence; empty queue/false delete never proves not executed | [probe.go](../../internal/adapters/codex/probe.go): `Probe`; `Delete`; [native_revoke.go](../../internal/daemon/native_revoke.go) |
| Peer frame identification in hook; hook trust, timeout, `hookSpecificOutput.additionalContext` in developer context, possible output spill | Peer input cannot consume owner notices/invitations; fixed notice only; offered after stdout ACK, not model input | `runCodexHookContext`; [framing.go](../../internal/adapters/codex/framing.go): `PeerMessageID`; [hooks.json](../../adapters/codex/plugin/hooks/hooks.json); [native_inform.go](../../internal/daemon/native_inform.go) |
| MCP env allowlist: no assumed CODEX_THREAD_ID/XDG_RUNTIME_DIR; TMPDIR available in pinned source; runtime pointer, first-hello pin; `mcp add` lacks name forwarding, `list/get --json` differ | Locate private IPC without manufacturing binding or persisting env secrets; preserve owner MCP policy | [mcp.go](../../cmd/peerturn/mcp.go): `mcpRuntimeCaller`; [runtime_pointer.go](../../internal/daemon/runtime_pointer.go); [setup_mcp.go](../../cmd/peerturn/setup_mcp.go): `checkSetupMCP`; [onboarding](../onboarding.md#owner-invitation-handoff--decision-of-23-october-2026) |
| Plugin/marketplace CLI JSON layouts, bundled helper path and normal trust UI; auto-update can replace shared daemon separately | Installation integrity and activation; CLI success is not hook execution or live binding | [setup.go](../../cmd/peerturn/setup.go); [setup_observe.go](../../cmd/peerturn/setup_observe.go); [adapter README](../../adapters/codex/README.md) |
| **Dormant ask:** `exec fork`, read-only/never/ignore-user-config flags, env isolation, JSON child/turn/item/usage events, tool restrictions and sandbox behavior | Separate child, unchanged live thread, authority and budget boundaries; still unavailable | [responder_command.go](../../internal/adapters/codex/responder_command.go): `BuildResponderCommand`; [responder_output.go](../../internal/adapters/codex/responder_output.go): `parseResponderOutput`; [sandbox.go](../../internal/adapters/codex/sandbox.go) |

Both MCP clients also depend on bounded JSON-RPC framing, the seven-tool catalog,
initialize negotiation (four implemented protocol revisions), extensible
informational metadata and strict authority-bearing inputs. A new runtime version
does not automatically authorize a new MCP protocol or tool schema.

## 2. Central policy and user-visible behavior

Use `internal/runtimepolicy`, distinct from PeerTurn's own `internal/version`.
It owns version parsing/comparison, minimums, verified evidence records, known-bad
capability exceptions and required contract profiles. A decision returns detected
version/source, minimum, qualification, contract results, disabled capabilities,
policy revision and actionable diagnostics. Adapters supply facts, not decisions.

- Accept every parseable SemVer **>= X**, with no upper or major-version ceiling.
  Numeric comparison, bounded fields, no leading-zero ambiguity; prereleases use
  SemVer ordering (baseline `-rc` is below X; a later release's RC is unverified).
  Build metadata does not change ordering; modified builds do not inherit evidence.
- `verified` means an exact tested release/build and applicable OS/architecture,
  adapter revision and capability profile occur in the packaged evidence list.
  Seed it with the two baseline releases and their **limited** evidence above.
  Records pin `os`, `arch`, `adapter_revision` and `profile`; initial tuples are
  `linux-arm64` and `darwin-arm64` only, under `native-preview/v1`.
  An exact release on another tuple stays `newer_unverified` with reason
  `unverified_platform`; separately qualified ask remains Linux ARM64 only.
  `newer_unverified` means admissible but no applicable record, including gaps
  below the highest tested version. It never means a full test pass.
- Below X: `unsupported_version`, e.g. “Claude Code 2.1.305 is below minimum
  2.1.306. Update Claude Code, resume the selected session and rerun setup/bind.”
  Missing/malformed reports: `runtime_version_unknown`; select/check the runtime.
- Setup reports the selected executable's version; bound status/doctor report
  the **running** runtime. Show version qualification separately from capability
  readiness, including `not_observed`, failed contract and recovery action.
  Setup without a running session cannot claim binding or live contract success.
- Claude passes actual `session.version()` facts to the Go helper, never the old
  hard-coded constant. The bounded helper startup preflight calls the same module;
  JS contains no independent version policy. Missing preflight capability closes
  peer admission while the owner hook still continues normally.
  Parse `version` and `base` separately; an unexplained mismatch is contradictory
  runtime evidence, not permission to substitute whichever value passes policy.
- Codex parses and retains the actual server version from initialize on every
  connection; stop assigning `SupportedVersion` as the detected version. Record
  MCP client and CLI reports separately. Conflicting runtime evidence closes the
  affected binding until reconciled; newer CLI on disk cannot requalify old server.
- Remove literal admission checks from all owner-listed sites, plus Claude
  `CheckResponderProfile`, Codex `supportedAgent`, `Bind` and `validateIdentity`.
  Keep equality checks for **incarnation continuity**, e.g. a bound version cannot
  silently change mid-lease. Reload/restart invalidates cached proofs and pending
  invitations; rerun checks without migrating grants to a different native UUID.
- Snapshot record versions use the central parser/profile lookup too. Older
  records within a resumed history are not proof of the current running version.
  Removing a responder pin does not satisfy its separate isolation/snapshot gates.
  The version-bearing profile ID in [ask_prepare.go](../../internal/daemon/ask_prepare.go),
  `authorizePreparedAsk`, must identify the actual central profile, not label a
  newer runtime as 2.1.306. Existing stored evidence keeps its original meaning.

Runtime policy does not relax wire versions for envelopes, grants, certificates,
invitations or storage. Those are separate protocol/schema compatibility checks.

Acceptance is not unconditional admission of effects. A known-bad version still
passes the minimum rule but its affected capability fails `runtime_contract_changed`.
Do not raise X merely to blacklist a broken intervening release or silently infer
that every later release fixed it. A workaround must be covered by explicit tests.

## 3. Runtime self-test and limits

Run bounded, model-free structural checks for **all** versions at startup and
before the ordinary binding operation;
cache only immutable facts for the same binary/build, adapter, process/socket
incarnation and config. Revalidate live identity on each effect. Do not block the
owner waiting for diagnostics, execute prompts, read transcript bodies or fetch peer URLs.

| Check / timing | Safe method and evidence | Failure scope |
| --- | --- | --- |
| Executable/setup | Bounded `--version`, selected executable identity before/after; inspect plugin/MCP inventory and configuration read-only | Setup/configuration only; no overwrite on unknown policy |
| Claude mod start | Check required callable APIs, version object and stable UUID; helper stdout framing, loopback echo, private socket UID/PID/start/inode checks | Binding/coauthor if essential API or identity missing; UI-only failure disables UI |
| Claude lifecycle | Observe actual start/end and main turn events, validate fields/order, retain unknown busy state until proven idle | Coauthor held; owner continues; no synthetic prompt to force idle |
| Claude bind nonce (outside self-test) | Existing challenge is a native **user frame**, normally consumed by `session.receive`; ordinary requested bind may attempt it on any admissible version | Never inject it from startup/doctor merely to test compatibility. Confirmation must match nonce, socket, host UUID, instance and expiry; absence/conflict closes binding regardless of version |
| Claude read-only current resolution | Daemon pins native socket kernel UID/PID/start and inode at binding open, rechecks at confirm; takes caller PID from IPC kernel credentials and rechecks every same-UID ancestor edge/start and native incarnation before returning the already-confirmed epoch. No native frames are written | Linux/macOS only; inaccessible process metadata, lost ancestry/incarnation or missing IPC credentials fail `needs_rebind`. Other platforms cannot establish this proof. It proves process ancestry, not human origin or executable authenticity; only explicit session bind challenges |
| MCP discovery/call | Validate actual initialize, negotiated protocol, types/bounds; discovery opens no capability and needs no daemon; check Claude native ancestry and already-confirmed exact epoch before each tool call (nonce operation is separate), Codex metadata plus native exact-thread proof before every tool call; pin the first proven session/epoch/caller even if hello fails | Refuse unproven tool calls while discovery stays available; never follow a replacement session/epoch/endpoint; preserve independently proven CLI binding |
| Codex bind/reconnect | Private socket identity, initialize/initialized, bounded loaded-list and metadata-only thread/read; queue/list limit=1 | Binding failure closes all native effects; queue failure closes coauthor, not owner/local revoke |
| Codex owner hook | Validate real hook schema; reject any agent fields and peer frames; root metadata and one-turn notLoaded query, bounded by existing 9-second proof / 20-second hook budgets | Automatic invitation/notice boundary only; timeout is unavailable, not proof of compatibility |
| Optional protocol inspection | In isolated CI, generate candidate Codex JSON schemas; compare required methods/fields with fixtures | Structural evidence only; not a live owner-origin or queue-effect test |

Do not treat queue/list success as proof that queue/add/delete semantics work.
Never enqueue/delete a canary in the owner's thread. Exercise mutations only in
disposable qualification sessions; validate real response echoes before recording
acceptance. Missing/malformed response **after** submit is `in_doubt`, alongside
the contract diagnostic; never rewrite it as a safe rejection or automatically retry.
An absent hook is `not_observed`; listing/trust settings alone cannot prove coverage.
The normal Claude binding operation is **not guaranteed model-free on an unknown
runtime**. If consumption semantics change, its random nonce may reach the model
and cause an unintended turn/cost. It contains no invitation, token or peer body.
Do not require a per-build interception allowlist: that would restore the exact-pin
failure for every new release. Keep all existing binding checks; only the native
mod confirmation establishes binding, never a model echo or successful socket write.
Missing confirmation returns a typed failure without automatic challenge retry.
Preventing every such unintended turn would require a proven non-prompt native
binding API; none is established here. This residual risk needs owner acceptance.

**Not self-testable by a startup shape check:** meaning/unforgeability of Claude
origin kinds, nonce consumption before model input, root versus subagent turn
semantics, completeness/order of events, `_meta` being host-owned rather than
model-controlled, binding/routing correctness inside the host, busy/FIFO/drop/
dequeue semantics, configuration precedence, sandbox isolation or unchanged
model/effort/context. Agreement between two fields from one runtime is useful
cross-checking, not independent human authentication. Official
[app-server documentation](https://developers.openai.com/codex/app-server)
describes schema generation and experimental opt-in, not stability guarantees for
this private shared-daemon integration.

Approved handling of `newer_unverified`:

1. Admit the version and run structural checks. Preserve read-only diagnostics,
   local revoke and capabilities with sufficient current binding evidence.
   Coauthor needs the usual exact session/epoch proof and grant recheck; missing
   proof closes that lane, never falls back to cwd, transcript or another session.
2. Enable automatic owner-invitation capture and owner-boundary notices immediately
   when their ordinary structural/provenance checks pass. No semantic qualification
   allowlist or extra owner confirmation is imposed on newer versions. The owner
   accepted the undetectable semantic-regression risk described above.
3. Never extend owner authority from unknown origin. Retain Codex's independent
   hook/root metadata checks and Claude's process/socket/nonce/epoch checks even
   on verified versions. Semantic qualification can be per capability without
   declaring the whole release verified. A newer Claude release with successful
   ordinary binding and structural checks can use coauthor without first entering
   the verified list. Failed exact-session proof closes dependent functions.
4. Keep restricted inform closed; ask requires exact packaged verification plus
   its existing independent responder/provider/OS/isolation gates. Newer versions
   do not inherit ask qualification. No owner permission/model/effort changes.

This is a real tradeoff: open version acceptance cannot prove arbitrary future
host semantics. Undetectable consistent runtime regressions can still misroute
coauthor or change behavior; native host and same-UID code remain trusted.
Warnings cannot prove semantics. Observed contract failures close dependent
paths; even qualified releases have bounded, not universal, evidence. Coauthor
remains strong trust, not a sandbox, with influence on subsequent owner turns.
Full existing functionality on a newer version is the approved default, subject
to ordinary identity/provenance and actual contract checks; ask is the exception.

Typed result: `runtime_contract_changed`, with bounded fields `runtime`,
`detected_version`, `contract_id`, `phase`, `reason` (`missing`, `malformed`,
`contradictory`, `semantic_unverified`, `known_bad`), `affected_capabilities`,
`policy_revision`, `action`. Some inventory checks remain observation-only or
qualification-only; neither unobserved nor semantically untested means PASS.
No native IDs, paths, prompts, tokens or raw host
errors in public diagnostics. Preserve existing `needs_rebind` for stale identity
and transient `unavailable` for transport failure. Action: update PeerTurn, rerun
doctor and rebind the same selected session; if needed select a qualified runtime
and resume it. No automatic downgrade, account change, override or resubmission.
Carry this typed result through daemon/IPC/MCP, Claude `writeClaudeFailure` and
the mod's startup vocabulary; use exit 6 for contract/version failures, retaining
exit 7 for an ambiguous attempted effect. Do not collapse it to adapter_unavailable.
Hooks return their normal continuation/empty output on failure; diagnostic failure
must never become a global block on verified owner work.

## 4. Release detection and qualification

The runtime-watch workflow uses a scheduled GitHub Action every six hours,
off the hour (`17 */6 * * *`), plus manual dispatch from main only. Scheduling
can be delayed; it is not a safety deadline
([GitHub schedule semantics](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#schedule)).
Read npm metadata for [`@anthropic-ai/claude-code`](https://registry.npmjs.org/@anthropic-ai%2fclaude-code)
and [Codex releases](https://api.github.com/repos/openai/codex/releases).
Compare every unseen stable version, not only latest; handle pagination and record
prereleases separately for optional early checks. Deduplicate by runtime/version/
artifact digest and policy revision; use conditional requests and bounded retries.
Codex candidates come from non-draft CLI `rust-v<version>` releases; validate tag
parsing and artifact identity rather than treating unrelated repository tags as CLI.

Detection produces a candidate report/artifact; live qualification determines
verification. Use SHA-pinned Actions, bounded jobs and disposable runners without
owner profiles. Only main scheduled/dispatch jobs receive the dedicated small-budget
LiteLLM repository secret. PRs receive no key and never run paid qualification.
Separate detection read permissions from the approved update/release job. Pin candidate
artifacts to recorded upstream integrity/checksums and immutable tags/commits;
changed bytes under one version are a new candidate. Upstream metadata is data.
After all required scenarios pass, automatically add the evidence-backed verified
version and publish a signed PeerTurn release. Failure opens an issue for a patch.

Model-free CI: policy boundary/parser tests; adapter fixture replay and mutation
tests; required Codex generated-schema comparison against the candidate binary;
isolated native startup/initialize/metadata/empty-queue probes where available;
Claude API/module/config shape and no-provider nonce-consumption canaries where
the real host can run without authentication. Block provider egress, not merely
omit a prompt. Do not copy live credentials to make a check pass. If startup needs
authentication or a contract cannot be exercised, record `not_tested`, never PASS.
Fake RPC/hooks test PeerTurn, not the candidate's semantics. No schema-generation
facility is assumed for the undocumented Claude mod API.

Turn the mac7 procedure and existing
[Linux orchestrator](../../research/lab/results/int-m3-m4/run.py),
[fixtures](../../research/lab/results/int-m3-m4/fixture.py) and
[evidence validator](../../research/lab/results/int-m3-m4/check_evidence.py)
into a new reviewed runner; do not execute or edit historical research scripts.
Parameters: PeerTurn commit/package digest, both runtime versions/digests, OS/arch,
scenario set, private canonical root, provider profile and explicit cost/run budget.
The mac7 README is a procedure/evidence source, not an already reusable CI runner.

Required live scenarios, on disposable macOS and Linux ARM64 profiles:

- Fresh independent pairs for each join direction; one first paste and actual
  `peer_join({})`; selected-handle ambiguity; late daemon and real host MCP calls.
- Same-cwd neighbors, stale/socket-replaced bindings, restart/resume, thread switch,
  mismatched MCP metadata, missing hooks; no implicit attachment or owner settings change.
- Claude composer positive and bridge/plugin/peer/slash negatives; Codex genuine
  subagent, internal `/review`, continuation and queued-peer negatives. Assert no
  owner invitation or notice ACK, not merely a particular model answer.
- Coauthor idle/busy delivery each way; correlate storage, native acceptance,
  model input and synthetic action separately; dropped/lost replies stay fenced.
- Inform no wake, fixed notice versus explicit body read; ask stays unavailable;
  revoke before admission, queued deletion and ambiguous already-started outcomes.

Use isolated HOME/TMPDIR/config/state, normal hook trust, a test relay and disposable
credentials. Pin both runtime update mechanisms for reproducibility only inside
the lab. Test a candidate against the other baseline, then both newest candidates.
Capture sanitized event assertions and artifact identity; keep raw private data
separate and redact before upload. Stop only recorded run-owned processes, revoke
test grants, verify teardown and expiry. Never retry an ambiguous native submit.
Use Makefile gates on an isolated runner within the dedicated key's approved small budget.

Outcome: required scenarios pass for an explicit profile -> reviewed verified-list
entry with evidence digest/date/limitations and README/CHANGELOG “Verified with …”.
Failures -> known-bad affected contract/profile, patch and targeted reproducer,
then requalify the final patched package. Partial/infrastructure failures remain
unverified, with their cause recorded in the failure issue. No green result is
inherited across changed binaries or derived from mac7's overall FAIL.

### How an older installation learns a known-bad version

| Option | Benefit | Cost / failure behavior |
| --- | --- | --- |
| Packaged policy in a PeerTurn patch | Small, offline, existing authenticated distribution; code fix and policy travel together | An installation learns **only after updating**. No remote revocation or instantaneous new verified badge; old/offline clients retain stale knowledge |
| Signed compatibility manifest attached to releases | Old clients can learn verified/known-bad capability records without a code update; faster warning/disable | New trust/update surface, expiry/rollback/key-rotation/cache rules, network availability and freshness tradeoff; still cannot fix code or undo admitted effects |

**Approved: patch-only for this phase**, explicitly documenting the stale-client
window. Pair each qualification announcement with a small PeerTurn patch containing
the updated evidence list; users update PeerTurn to receive new knowledge. Startup
self-tests and ordinary native authority gates remain the local defense.
An unknown break cannot be retroactively detected by an offline old installation.

If later approved, a manifest must use established release signing/verification,
an installed trust root, bounded canonical data, monotonic sequence and expiry,
atomic last-good cache and rollback protection. It may describe exact runtime/
platform/adapter profiles and disable capabilities; it cannot download executable
code, change grants/minima or grant new authority. Invalid data never replaces the
cache. Persist learned denials; on expired/unavailable metadata report stale and
fall back to packaged/local evidence, never silently clear a denial. A requirement
for mandatory fresh revocation would need a separate offline-availability decision.

## 5. Separately reviewable migration

1. Add central policy and evidence data, initially without changing admission.
   Tests: minimum-1/minimum/newer, numeric 2.1.999→2.2.0, 0.163→0.164, future major,
   prerelease/build metadata, malformed/oversized versions, evidence-profile mismatch,
   known-bad precedence. Preserve exact pins in historical evidence/fixtures.
2. Add contract results, actual-version reporting and capability gating alongside
   old checks. Tests: every missing/wrong field, delayed/absent hooks, invalid origin,
   owner continuation, silent runtime replacement and sanitized diagnostics.
   Qualification status never supplies authority. No DB migration is needed;
   use packaged evidence and incarnation-scoped in-memory results.
3. Switch Claude consumers, helper preflight and setup to the central policy;
   remove all listed/adjoining pins together. Tests: newer version passes admission,
   actual version reaches daemon, newer Claude binds without verified-list entry,
   missing/wrong/expired confirmation closes binding, no model echo can confirm,
   startup/doctor emit no native user frame, composer
   versus other origins, main/child events, epoch mismatch, lost submit `in_doubt`.
4. Switch Codex consumers, RPC handshake and MCP client qualification. Tests:
   newer client/server admission, actual versions retained, every reconnect checked,
   changed socket/version fenced, missing/conflicting `_meta`, Review/rootTurnId
   negatives, no queue retry, delete ambiguity, env forwarding/config preservation.
5. Wire consistent status/doctor/setup output and dependent contract gates; document
   human recovery and unverified limitations. Test verified-but-broken and
   unverified-but-structurally-ready separately; no owner policy/model/effort change,
   typed errors survive all surfaces, no promotion of restricted capabilities,
   no installation-success-as-binding claim. Update current help/relay-page wording;
   historical evidence retains its exact pins.
6. Add release detection and reusable qualification runner, then qualify candidates
   and ship the policy/patch through the normal authorized release path. Tests:
   pagination, repeated releases, republished digest, timeout/rate limit, untrusted
   metadata, missing credentials, partial matrix, fresh reverse pairs and redaction.

Start each behavior change with its failing fixture; executor runs targeted Make
checks; the integrator runs the full `make ci` once on finished code.
Require one independent finished-diff security/release review. Step 1 changes
local code/tests/docs only; release automation follows separately. No release
version bump, schema change or historical evidence rewrite belongs to this step.

## Operation

Step 2 implements `runtime-watch.yml` and `runtime-qualify.yml`; the
[operator guide](../operators/runtime-qualification.md) defines configuration,
evidence limits and recovery. The main-only watcher runs every six hours,
dispatching at most eight unseen stable candidates above minimum. Durable
policy/artifact claims prevent duplicate paid runs; uncertain mutations require
manual recovery. Verified versions have no continuous artifact revalidation.

Model-free fixtures precede the `ubuntu-latest`/`macos-latest` live matrix, pairing
the candidate with the opposite baseline. Artifact integrity and native assertions
are required; unsupported automation or missing proof fails. The LiteLLM key is a
main-only Environment secret that full-access hosts can read from the parent;
server spend/rate/model/expiry limits bound access. The profile remains narrow.

Qualification may precede the approved 72-hour publication hold. Promotion and
release wait for npm `time[version]` or GitHub `published_at` to reach 72 hours.
Users may use newer unverified runtimes; only the verified label/patch waits.
Claude's pinned npm lock/tree is shared across OS jobs. Qualification/release
disable Go caches; release downloads and verifies clean modules before building.

Same-run/commit receipts pin OS/arch in packaged JSON and update README/CHANGELOG
“Verified with …”; protected main uses a checked bot PR with auto-merge. Reserve
`v0.1.N-preview` (at least `v0.1.2-preview`) with installer pins; no ask profile.
Sweeps recover releases after PR merge. Dispatch `release.yml` at the tag for
tag-based OIDC and validation/SBOM/provenance/draft-publish safety. Failures update
one sanitized issue. Implementation work performs no live runs or GitHub writes.
