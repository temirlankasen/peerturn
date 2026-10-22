# M6 Claude responder qualification

Owner decision, 22 October 2026: remove the monetary funding prerequisite.
Receiver-owned deadline, output, concurrency, hourly and daily quotas control
v0.1 asks. Spend per ask depends on these limits and provider behaviour;
`--max-budget-usd 1` is a native estimate, with no provider-billed ceiling promise.
Codex and macOS responder admission remain unavailable independently.

The [M6b lab](../research/lab/results/m6-ask/m6b/README.md) separates product
acceptance from native enforcement diagnostics. [Earlier M6 evidence](../research/lab/results/m6-ask/README.md)
retains its original closed verdict. A native-only fixture does not prove daemon
authority isolation, signed reply custody or a qualified product `ask`.

## Trusted launch inputs

Only a daemon-derived project directory, executable, home, native UUID and exact
authority roots enter `ResponderOptions`. Peer text is a bounded JSON string on
stdin. It cannot supply arguments, settings, environment entries or native IDs.
The helper records its actual project directory through the verified native
binding; a shared directory by itself grants no authority.

`AcquireResponderSnapshot` reads the exact bound UUID's local JSONL journal. It
requires the pinned native version, a complete newline-terminated journal, a
unique latest completed assistant UUID, an assistant `end_turn`,
explicit requested model and effort, and a recorded native system prompt.
Per-turn effort overrides, missing metadata, duplicate UUIDs, ambiguous files and torn records
fail with `snapshot_unavailable`. The snapshot retains a hash and size of the
original prefix. Later owner appends are allowed; a changed original prefix is
rejected. Exported display labels cannot replace the private qualification.

The runner resumes a new native UUID at that exact leaf with
`--resume-session-at`, explicitly preserves model/effort, and enables the native
system-prompt snapshot. Before peer input, and again before returning an answer,
it verifies the child journal's system prompt and model/effort against the
original snapshot. It never rewrites or trims the original transcript. Older
mixed-model history, unsupported native records and unavailable child evidence
can conservatively make the profile unavailable.

The parser binds the exact native session, existing transcript and unique last
completed leaf. Every nonempty `parentUuid` must refer to an earlier unique UUID.
In ordinary TUI transcripts, `last-prompt` identifies pre-generation input; the
resume target is the later completed assistant. Trailing `turn_duration` metadata
does not add model context. The parser does not reconstruct every historical branch or claim atomic
capture of every runtime memory slot. Later owner appends do not change the
captured prefix; torn or ambiguous boundaries are refused.

## Managed policy gate

The native helper fingerprints its authenticated direct parent's Linux executable,
selected settings sources and effective allowlisted provider environment. The
daemon launches that exact binary and compares its existing environment with the
parent fingerprint; it never imports a credential from the helper. Binary and
settings fingerprints are checked again before peer input and final output.
Requested model/effort come from the parent snapshot; both init and real assistant
events must match the requested/effective model respectively.

The binary digest is cached by pathname, device, inode, size, modification time,
change time and mode. Each use opens and stats the selected image; a changed
identity or timestamp requires a fresh bounded SHA-256 read. The metadata is
checked again after hashing, and concurrent checks serialize only the same
pathname. This reduces repeated binary reads without claiming an immutable
filesystem. Settings and provider fingerprints remain freshly checked.

This is a conservative custom-gateway profile. It supports default
`user,project,local` or explicit empty settings sources. Selected settings are
fingerprinted and qualified as provider-neutral, then execution-bearing settings
are excluded from the fork. The maintenance-only `DISABLE_AUTOUPDATER=1|true` is
allowed; settings-derived provider environment, `apiKeyHelper`, OAuth,
unknown provider settings/flags, custom source lists, proxy overrides and explicit
`--settings` are refused rather than guessed. The same provider configuration is
retained; hooks/plugins/MCP permissions are deliberately replaced by the restricted
profile. There is no independent provider identity attestation or billing proof.

`CheckResponderProfile` checks managed configuration before executing the pinned
Claude binary. On Linux it checks `managed-settings.json`, `managed-mcp.json`, all
JSON entries in `managed-settings.d/`, and the Claude `remote-settings.json` cache.
Any nonempty managed document is refused as `managed_policy_present`. Invalid,
unreadable or nonregular sources are `managed_policy_unverified`.

The current qualification accepts only explicitly supplied custom HTTPS
`ANTHROPIC_BASE_URL` plus API-key/token authentication. OAuth, direct Anthropic,
WSL and other unqualified managed-policy sources are refused. macOS remains
`macos_unqualified`: checking a few plist paths cannot establish absence
of the native managed-preferences domain. No host model or real Keychain probe
was run. The documented policy locations and the custom-endpoint exclusion for
server delivery are described in the official [managed settings](https://code.claude.com/docs/en/managed-settings)
and [server-managed settings](https://code.claude.com/docs/en/server-managed-settings)
references. These references are not a substitute for a qualified OS probe.

## Restricted native process

The fixed arguments include `--restricted`, `--permission-mode default`,
`--permission-prompts none`, `--tools Read,Grep,Glob`, empty setting sources,
disabled slash commands, `--no-chrome`, and strict empty MCP configuration.
Private `0600` settings enable `disableAllHooks` and explicitly disable the three
pinned built-in plugins as well as inherited user/project/local configuration.
Unknown extra plugins or native tools cause a failure; they are never silently
accepted as harmless.

The pinned CLI can add tools to its actual provider request that are absent from
`init.tools`. The fixed native deny list also excludes `AskUserQuestion`,
`ListAgents`, `ReportFindings`, `ScheduleWakeup`, `Skill`,
`DeferredToolPlaceholder` and `ToolSearch`. Init inventory alone is insufficient.
The child also fixes `ENABLE_TOOL_SEARCH=false`: a deny on ToolSearch alone can
leave a deferred placeholder in the provider request, as the official
[MCP tool-search documentation](https://code.claude.com/docs/en/mcp#configure-tool-search)
describes. This changes only the fork's tool profile.

The fork sets `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`,
`DISABLE_AUTOUPDATER=1`, `DISABLE_TELEMETRY=1` and `DISABLE_ERROR_REPORTING=1`.
These names were checked against the pinned 2.1.306 binary. They disable native
background traffic and update paths; they are not a network sandbox or a
restriction on the configured model-provider request.

The read boundary is the bound parent's current working directory, resolved to
its canonical existing directory. `/`, the home directory and its ancestors,
top-level system directories (including `/tmp`, `/var`, `/Users` and `/Volumes`),
their symlink aliases, and sensitive system/credential subtrees are refused as
`project_root_unqualified`. A specific temporary project directory can qualify;
the temporary-directory root cannot. The owner must bind a specific project.

Read permission is allowed only for that qualified root. Explicit deny rules
cover declared and resolved daemon state/config/runtime roots, native runtime
configuration, protected paths, `/proc` and file-descriptor paths. Root-relative
rules also deny credential directories at every depth: SSH, cloud, Kubernetes,
Docker, Codex/Claude, GitHub, GnuPG, browser profiles, local keyrings and PeerTurn
data/config/cache. They deny `.netrc`, `.git-credentials`, package-manager auth,
shell histories, `.env*`, `.git/config` and common private-key file names.
The native restricted mode independently confines file tools to working
directories. The live lab confirms that removing bare `Read` permission matters:
the previous profile read an outside file and its symlink alias. A scoped Read
allow with automatic permission denial blocked outside Read/Grep/Glob on 2.1.306.

Path rules are not inode isolation. A pre-existing in-project hardlink may expose
the bytes of a readable file. The mandatory authority subset therefore requires
a rotated non-file wrapping key and sealed state; redaction cannot replace that
boundary. Project files and the full inherited native context may contain other
secrets or stale instructions. The filesystem remains live during analysis.

## Initialization and receipts

The native `init` event is emitted only after user input. The SDK initialization
control response has no complete tool/MCP/plugin inventory. The runner therefore
first submits one fixed local startup probe in the same fork, verifies `init`,
and waits for that trusted probe to complete. This adds a bounded model turn and
cost; it adds no peer text to the owner's live conversation.

Before submitting peer data, native inventory must be exactly Read/Grep/Glob,
with zero MCP servers, plugins and skills. Hook or unknown system events fail
closed. Known `thinking_tokens` and `api_retry` notices are discarded. Each
repeated per-turn `init` must preserve the same fork UUID and inventory.
The fresh child `api-request-shape.tools` must independently contain exactly
the three unique file tools. Missing or additional tools fail closed before peer
stdin. The last exact trusted probe separates fresh shapes from inherited owner
history because the native fork rewrites inherited session IDs. Every fresh shape
is checked again before returning output. Native denies enforce execution;
observing a `tool_use` event and cancelling afterward is not a pre-effect fence.
`AdmitPeerInput` must serialize the actual bounded stdin write with the daemon's
fresh grant/epoch/expiry checks and revocation, not just return a prior approval.

A fresh local UUID is selected before native process creation and durably
recorded by `PrepareFork`. It is passed with `--session-id`; init must report
that exact UUID. The pinned native CLI was checked to support this with
`--resume --fork-session`. The local UUID reservation is custody evidence;
`responder_started` still requires valid native init.

After the process finishes, the runner removes its fork journal on success,
failure, cancellation and invalid/missing init, after any final journal checks.
Cleanup also covers UUID-specific project artifacts, debug logs, todos, tasks,
session environment and file-history directories. An uninspectable or
unremovable artifact suppresses the answer as `fork_cleanup_failed`.
`CleanupResponderArtifacts` supports idempotent recovery once the corresponding
child has stopped. `ResponderForkActive` checks same-user Linux process arguments
for the exact reserved `--session-id`; an active process or an unverified check
must close new asks instead of deleting a still-written transcript. This is a
recovery observation, not a process lock. Native 2.1.306 probing found only the fork journal changed;
neither `history.jsonl` nor `.claude.json` contained the fork UUID. Shared owner
metadata and the parent transcript are never rewritten by cleanup.

This cleanup is not secure erasure or crash-atomic deletion. A daemon crash can
leave an orphan process and plaintext artifacts until it has stopped and
recovery cleanup succeeds; recovery must not race a still-writing child. A
same-user process or filesystem backup can retain copies independently.

`responder_started` requires the verified native init. `model_input` requires a
real assistant event for the peer turn, matching the qualified effective model;
synthetic native API-error messages do not qualify. Completion requires a
successful final result for the same child UUID. Observer failure, cancellation,
changed snapshot, malformed output, timeout or budget failure returns no answer.
The daemon separately owns durable reservations, signed receipts and reply
commit authorization. A shell exit, fork UUID or native-only context canary is
not signed-answer acceptance evidence.

## Output policy

The answer and redaction input are capped at 16 KiB. Native event buffering has
separate bounded per-record and total limits, and only the complete final answer
can leave the runner. Tool/debug output and partial answers are never forwarded.
Native turn/dollar cap results are typed `budget_exceeded`.

Redaction matches known values and the exact whole-value encodings generated by
`outbound.SecretEncodings`, plus bounded credential-pattern heuristics. Coverage
includes those exact Go JSON/HTML escapes and aligned standard hex/Base64/Base32
forms. It does not prove detection of wrappers, arbitrary byte shifts, mixed-case
or separator hex, alternate entity spellings, arbitrary re-encoding or deliberate
semantic disclosure. Full parent native history is retained; the temporary
fork copy is cleaned after process completion. Redaction is defense in
depth, not DLP or permission to expose authority material to the fork.

Validation evidence and platform boundaries are recorded in the
[M6b lab](../research/lab/results/m6-ask/m6b/README.md). Earlier native diagnostics
remain in the parent directory and retain their original closed/failure verdicts.
