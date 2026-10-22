# M6 Claude ask

**Owner decision, 22 October 2026:** monetary funding is no longer an admission
prerequisite. M6 uses receiver-owned quotas, a restricted Claude launch profile,
OS-keystore isolation, exact parent/configuration checks and durable reply custody.
Spend per ask is bounded only by these limits and the provider's own behaviour;
there is no billed-dollar ceiling. This supersedes the earlier monetary proposal
in [design §8.2](spec/design.md#82-context-framing-concurrency-and-cost).

The [M5 evidence](m5-ask.md) remains historical evidence. The new
[M6 lab](../research/lab/results/m6-ask/README.md) preserves the earlier closed
preparation and diagnostics. [M6b evidence](../research/lab/results/m6-ask/m6b/README.md)
records the new product acceptance separately; a diagnostic fork is not a signed reply.
The [M6c regression evidence](../research/lab/results/m6-ask/m6c/README.md)
records root, queue recovery and transcript-retention rechecks.

The qualified product profile is Claude Code 2.1.306 on Linux ARM64 with the
tested custom HTTPS gateway and a freshly verified Secret Service keystore.
An actual native peer received a useful signed answer from context present only
in the receiver's live conversation. The fork's requested/effective model,
effort and exact three-tool provider inventory were retained; the complete parent
transcript stayed byte-identical. This does not qualify other providers or OSes.

## Keys and admission

Follow [wrapping-key operation](m6-keystore.md) to rotate a stopped file-backed
store with `peerturn keys isolate --state-dir /absolute/private/state --json`.
File mode stays available with `authority_secrets_isolated=false`; asks are
refused with `authority_file_key`. A migrated store requires its OS service on
every open and a fresh continuity check before each attempted peer admission.
A stale `seal.key` is refused. macOS Keychain command transfer has mock coverage;
native macOS host qualification is absent, so asks return `macos_unqualified`.
Only Linux with a verified Secret Service backend can qualify this profile.

Only the native Claude UUID and project recorded after its existing challenge
handshake can select a parent. A cwd alone never binds a session. Codex can send
an explicit ask but cannot receive one in a responder: its native runtime
interface still exposes only binding and coauthor queue operations.

The pinned Claude 2.1.306 profile removes every tool except Read/Grep/Glob, uses
restricted mode, scopes Read permission to the bound project, rejects permission
prompts, and supplies strict empty MCP configuration. User/project/local setting
sources and hooks are disabled; the three pinned builtin plugins are explicitly
disabled as well. Any nonempty managed settings/MCP document, unreadable policy
source, macOS MDM uncertainty or remote-managed-capable authentication refuses
startup. Unknown init tools, MCP servers, plugins, skills or hook events refuse
peer input. Fresh native provider-request shapes must also report exactly the
three file tools; the explicit deny list excludes hidden scheduling, agent,
skill and tool-discovery tools that the pinned CLI omits from init inventory.

The read boundary is the authenticated parent's canonical cwd. HOME, an ancestor
of HOME, filesystem/system roots and credential directories are refused as
`project_root_unqualified`; local status reports that reason. Credential-store,
shell-history, keyring, `.env*` and PeerTurn-state deny rules apply at every depth
inside an otherwise qualified project. These do not remove secrets already in
inherited conversation context or establish protection from arbitrary hardlinks.

The native protocol emits init only after receiving input. A fixed trusted local
probe therefore runs first in the same fork; its validated init and native
system/model/effort and actual provider tool inventory precede the single peer-frame write. That write is
serialized with revoke and binding checks. The probe is an extra model turn and
can incur cost. It is never represented as the question reaching the model.

Snapshots request a native prefix with `--resume-session-at`.
Missing, ambiguous, malformed or incomplete journal evidence closes admission.
The prefix, requested/effective model, effort and system snapshot are checked
again in the fork. A child UUID is selected and sealed before process start.
After the child exits, its journal and UUID-specific runtime artifacts are
removed on success, failure and cancellation; a cleanup failure suppresses the
answer. The pinned print-mode fork did not update `history.jsonl` or
`.claude.json`. Restart cleanup uses sealed child metadata and refuses to race
a still-running child; an unverified cleanup closes asks while preserving owner
and coauthor operations. Native parent history remains runtime-controlled and
may contain plaintext private context; no history is attached to a reply.
The exact native config directory is sealed with the snapshot. Legacy cleanup
metadata lacking that directory requires local reconciliation; the daemon never
substitutes its current HOME/config selection as proof. GC retains unresolved
cleanup metadata, and a successful cleanup explicitly clears it.
The selected leaf is the latest completed assistant and the last context-bearing
UUID in the captured prefix. The pinned TUI's trailing `turn_duration` metadata
is ignored for leaf selection. Its `last-prompt` points to pre-generation input,
so it cannot substitute for a completed assistant. Duplicate record UUIDs,
duplicate session files, dangling ancestry, mixed sessions and nonterminal leaves
are refused. The exact bound session ID
selects the journal; no newest-session search or cwd-only selection is allowed.
Parent executable and provider settings are checked separately; model labels do
not establish provider continuity. See the [profile](m6-claude-profile.md).

## Budgets and reply custody

The sealed journal retains receiver policy and per-peer reservations: default
120-second deadline, 16-KiB final answer, two active forks per daemon, one per
parent/conversation, six asks per peer per rolling hour, twelve per peer per UTC
day, and 24 asks per UTC day across the daemon. The peer counter uses the exact
grant-bound identity/session, independent of conversation and budget IDs. Set
`ask_per_day = 24` in the owner's private `config.toml`
(allowed range 1..4096), then restart the daemon. Changing the limit preserves
usage; absent configuration keeps the sealed policy. Failed/ambiguous launches
consume reservations. Clock rollback and fresh peer/conversation/message IDs do
not reset the daily count. Native output and tool calls are bounded separately.
These limits do not qualify a monetary ceiling; the startup probe can incur cost.

Upgrading a legacy hourly-only ledger cannot reconstruct expired reservations.
It conservatively consumes the default daily allowance for its last recorded UTC
day; the owner may raise the limit, or wait for the next UTC day. Malformed sealed
ask accounting closes ask admission while ordinary owner/coauthor work continues.
Completed reservations remain through their UTC day so hourly cleanup cannot
reset a peer's daily share. Missing per-peer history from an older ledger is
conservatively counted against each peer until the next UTC day.

Custody and unavailable refusals spend no session model-turn budget. A durable
launch reservation charges once in the sealed quota ledger. Cheap permission,
quota, busy and reverse-lane checks precede keystore, executable and journal
qualification, which runs outside both daemon locks. At most one new worker
starts per polling pass. Before spawn, the DB repeats permission, session-turn,
busy and six-packet control-headroom checks atomically with `responder_runs`.
Headroom is a precheck, not capacity held against later unrelated traffic.

Terminal failure/cancellation custody commits independently of reverse-notice
insertion. A full control queue or lost return authority can prevent a notice,
but cannot leave the run busy. Restart resolves incomplete and legacy uncertain
asks as `failed`/`responder_failed`, discards uncommitted answers and releases
both DB and quota capacity without refunding usage. A crash during preflight
also closes its paid pending input. No old question is automatically replayed.
`UncertainDeliveries` excludes responder routes: uncertain live coauthor effects
retain their separate fence. No schema migration or new SQL table is introduced.

The product path records distinct `responder_started`, `model_input`,
`turn_completed`, redaction and `reply` evidence. Only native assistant evidence
for the peer turn supports model input. The daemon seals/signs the filtered
answer with the original conversation, parent, budget and incremented hop; reply
outbox custody, run settlement and its reciprocal receipt share one transaction.
Every stage and final commit recheck grant epoch, lease, binding, expiry and
revocation. Failed or cancelled output is discarded.
Failure receipts sent to the peer expose only `unavailable`, `busy` or `failed`;
bounded detailed diagnostics stay in local receipts/status.

Outbound redaction is defence in depth. It catches literal known values, exact
whole-value Base64/Base32/hex encodings at aligned boundaries, Go's URL/JSON/HTML
escaping variants, and selected credential patterns. Shifted encodings, wrappers,
mixed hex case, chunking, ciphers, alternative escapes and paraphrases can evade
it. It does not isolate authority or promise confidentiality between peers whose
messages already occur in the parent's full context.

## Live evidence boundaries

The final product runs record all authenticated reverse-channel stages, useful
receiver-only context and an unchanged live parent. The real model refused the
injection attempts without calling tools. A separate deterministic native gateway
forced 14 actual tool results with the same restricted profile: project Read
succeeded; state/keystore/outside/symlink Read and outside searches were denied;
Bash, Write, MCP and agent/scheduling/discovery tools were unavailable. This
diagnostic proves native enforcement and uses no real model or provider.

The final product runs passed the redaction policy stage but the model refused
to repeat the synthetic token, so they do not prove a literal replacement.
An earlier product calibration recorded an actual redaction label with the same
unchanged redactor. Both observations and the earlier failed profile attempts are
preserved separately. Live Codex returned signed `responder_unavailable`; its
final-binary status remained closed. No macOS host qualification was performed.
