# Codex adapter (minimum 0.163.0)

The shared app-server adapter accepts Codex CLI >=0.163.0, without an upper
bound. Newer releases retain full existing capabilities with `newer_unverified`;
ask remains closed. The verified baseline and original evidence remain 0.163.0.
See the [approved runtime policy](../../docs/spec/versions.md). Read-only startup
and doctor contract probes never submit model input or queue mutation canaries;
`runtime_contract_changed` disables only dependent capabilities with recovery advice.

Use a built, locally trusted PeerTurn binary. Copy the executable to
`plugin/bin/peerturn` before enabling the plugin and approving its trust screen;
the helper is then inside the content Codex hashes for plugin trust. Use a regular
file and a regular `bin` directory, rather than symlinks. Replacing the executable
changes the trusted plugin content. The hook has no PATH or environment override.
The live lab did not redisplay a trust dialog after reinstalling an updated
helper and resuming the same thread; automatic trust invalidation is not claimed.
The local marketplace is this directory (`.agents/plugins/marketplace.json`),
with plugin root `plugin/`. Add the local marketplace through Codex's plugin
interface, enable PeerTurn, and accept its actual trust screen. Plugin activation
may require a normal session restart. Do not bypass hook trust: that flag selects
embedded TUI in the pinned build. This local packaging is not a signed installer;
publisher signatures and a signed installation manifest remain unqualified.

Run the skill's `init --codex-current` in a tool child of the chosen TUI. Use
separate explicitly selected state/runtime profiles for separate native sessions;
the M2.5 daemon still owns one session per instance. A shared cwd never chooses
one. Do not bind an old preview identity to new native authority.

The adapter verifies the shared socket, version, loaded thread and thread/read,
then queues FIFO via `thread/queue/add`, preserving a stable client message ID
and returned item ID. It never steers a busy turn. A socket replacement or unloaded
thread requires rebind. Queue acceptance is not model consumption or completion.
RPCs use short-lived connections and an explicit method allowlist. Server drop
is recovered by the next connection with the original socket identity/version;
queue submissions with ambiguous outcomes are never automatically retried.
The client identifies as `peerturn`; Codex 0.163.0 may update global client
originator metadata during initialization.

The `UserPromptSubmit` hook accepts the pinned key set, rejects unknown keys and
ignores every input with an `agent_id` or `agent_type` field, including `null`.
Only an exact root-session binding can request an inform notice. Canonical
PeerTurn queue frames report the peer start to the daemon without consuming FYI.
Input is bounded at 1 MiB to cover control-escaped maximum coauthor frames plus
the outer hook JSON. Unsupported future schemas are ignored without a callback,
so they cannot qualify the peer-start detection boundary until revalidated.
Other integrations may submit indistinguishable root user input: the hook API
has no general owner-origin proof, so that residual boundary remains unverified.

Owner decision, **23 October 2026**: an eligible owner prompt also hands the
capability, parsed/normalized with CLI join's strict rules, to existing private
same-UID IPC. It is bound to the exact root thread selected on MCP calls by
host-owned `_meta.threadId`. The daemon holds ≤8 pending capabilities/session,
memory-only for ≤10 minutes, clearing on use, expiry, unbind/rebind, stop/restart.
MCP `peer_join({})` accepts no invitation text; optional nonsecret `handle`
selects an entry shown only with relay host and age. No inviter label precedes
descriptor authentication. Pending secrets never enter disk, logs, status,
receipts or audit; post-join sealed recovery state is separate. Owner pastes
still enter provider/native transcripts. See [handoff and typed failures](../../docs/onboarding.md#owner-invitation-handoff--decision-of-23-october-2026).

Codex records hook `additionalContext` as developer context and spills oversized
context into plaintext temp files. The helper therefore returns only a fixed
notice with a bounded count and validated fingerprint labels, at most eight
items; peer bodies are read separately through `peer_inbox` or `peerturn inbox
--json` as untrusted tool data. The whole hook response stays below 512 bytes,
and the plugin explicitly sets a 256-token context limit. The source contract is
`openai/codex` snapshot `c419fd01` (`core/src/context/hook_additional_context.rs`,
`hooks/src/output_spill.rs`); the observed 0.163.0 input keys are recorded in
`research/lab/results/m4-codex/raw/63-join-observe.json`. The snapshot is supporting
source evidence, not a claim that its entire HEAD matches the release binary.

The daemon prepares a short-lived notice offer without marking messages offered.
After writing the notice successfully, the helper acknowledges it using the same
20-second helper deadline (22 seconds at the host). Each active-root proof has
a nine-second cap: metadata two seconds, legacy replay eight seconds within it.
The pinned server can replay the entire rollout even for one metadata-only turn;
extremely long histories still fail closed. A failed or late write gets no acknowledgement; a missing
acknowledgement can produce another notice. This acknowledgement proves only the
helper's successful output write, not Codex ingestion or model consumption. Hook
failure returns empty context and never blocks an owner turn.

The native read-only fork mechanism is implemented separately with typed gates.
Stock read-only permits reading local signer/admission secrets and may inherit
effectful integrations. The product therefore reports `fork_responder=false`,
`protected_path_policy_failed`; sandbox preflight and injected host-independent
tests are separate evidence. It does not answer asks until full isolation and
provider budget enforcement can be qualified. See [M4 evidence](../../research/lab/results/m4-codex/README.md).
