# PeerTurn Claude plugin

This pre-alpha plugin accepts **Claude Code 2.1.306 and later**, without an upper
bound. The central Go runtime policy distinguishes verified builds from newer,
unverified builds; both receive all existing capabilities after native checks.
The dormant ask responder retains its verified-build restriction. Its dependency-free
JavaScript mod handles the runtime API and UI. The Go helper owns binding,
authorization, custody, framing and receipts. Installing the plugin does not
authorize peers or automatically attach other sessions.

Configure `peerturn_binary` as the absolute path to the installed executable.
The repository includes a local marketplace at `.claude-plugin/marketplace.json`;
the plugin source is `adapters/claude`. Installation and enabling require the
owner's explicit instruction. Native activation must be checked after reload or
resume; a successful plugin install alone is not delivery evidence.

The mod starts `peerturn adapter claude --native-session UUID --runtime-version
DETECTED_VERSION --provider gateway|anthropic` using an argv array. It forwards
the actual session version; the helper applies the central policy before effects.
The provider value only
records whether `ANTHROPIC_BASE_URL` is present; it is not a provider qualification.
The helper inherits its own native child socket environment. Stdout supplies one
bounded NDJSON `ready` object containing a strict `http://127.0.0.1:PORT` URL and
a 64-character lowercase hexadecimal capability. Before readiness, only bounded
allowlisted stderr codes reach `ui.status`; raw diagnostics are never displayed.
See [first-run failures and MCP verification](../../docs/onboarding.md).

All loopback calls use `Authorization: Bearer CAPABILITY`:

| Route | Request / response |
| --- | --- |
| `POST /confirm` | `{nonce,session}` confirms the consumed native bind challenge. |
| `GET /poll` | `{type:"live",message_id,attempt_id,text}` or `{type:"status",inform_pending}`. |
| `POST /authorize` | `{message_id,attempt_id}`; only `{authorized:true}` permits submit. |
| `POST /release` | `{message_id,attempt_id}` releases authorization before a native call. |
| `POST /result` | `{message_id,attempt_id,result}` preserves the native submit result. |
| `POST /owner-boundary` | `{session,invitation_links}`; optional links array: at most 32, each at most 2048 bytes; returns `{notice_id,context}` with count/sender labels only. |
| `POST /boundary-attached` | `{session,notice_id}` confirms host attachment of that notice. |
| `POST /close` | Best-effort `{}` on native session end. |
| `POST /contract` | `{contract_id}` records an allowlisted contract failure and closes only dependent capabilities. |

`<peerturn-bind>64-lowercase-hex</peerturn-bind>` is consumed by `session.receive`
before the model queue. The helper verifies the fresh nonce and exact native
instance; kernel socket peer UID/PID/start must match the helper's Claude parent.
Text or sender names do not grant authority. Clear/resume invalidates
the old binding and starts a fresh helper at the next prompt boundary.

The Go-framed coauthor text stays outside Claude's native queue until a trusted
main `turn.complete` establishes idle. Unknown state after load/reload holds
admission. The mod then reauthorizes and checks state after awaited calls before
submitting once via `$.prompt.submit({text})`, preserving plugin provenance.
There is no atomic conditional-submit API, so a new owner turn racing that call
can still cause native queue admission. There is no `asUser:true`, Esc
simulation, forced queue removal or automatic retry. Counts continue polling
while a coauthor message is pending. A drop is reported as returned; an exception is
reported as `{error:"submit_failed",in_doubt:true}`. An uncertain receipt stops
admission until reconciliation. Native acceptance is distinct from turn completion.

`coauthor` grants normal agent capabilities and is strong trust. Revocation after
native queue admission cannot guarantee removal or stop each tool dispatch.
The mod enriches `prompt.submit.context` only for host-provided `composer` and
`bridge` origin kinds. It obtains typed inform context from the helper; plugin,
peer, SDK, slash-command and unknown origins never consume it. This context is a
fixed-format count and verified sender labels with `peer_inbox` / `peerturn inbox`
instructions; it contains no peer bodies. Eligible human composer input also
hands extracted invitation capabilities to private same-UID IPC, bound to the
verified native epoch; the helper receives no complete owner prompt text.
The same strict parser/normalization as CLI join applies. The daemon retains
≤8 pending capabilities/session in memory for ≤10 minutes, clearing on use,
expiry, unbind/rebind, stop or restart. Pending secrets are absent from disk,
logs, status, receipts and audit; post-join sealed recovery state is separate.
MCP `peer_join({})` accepts no text; an optional nonsecret `handle` selects an
entry shown with only relay host and age. No inviter label precedes descriptor
authentication. The owner's paste remains in provider/native transcripts.
See [the 23 October owner decision and typed failures](../../docs/onboarding.md#owner-invitation-handoff--decision-of-23-october-2026).
The classic `UserPromptSubmit`
command invokes `peerturn adapter claude owner-boundary` as a no-op fallback
until a separately verified owner permit exists. Inform notifications show
counts only and never call prompt submit.
Owner enrichment waits at most one second for the helper. The notice becomes
`offered` only after `next(prompt)` succeeds and the mod confirms attachment.
Failed/late calls leave it pending. Bodies become `delivered` only after successful
CLI/MCP output and its acknowledgment. Both are `adapter_accepted` evidence with
distinct outcomes, never proof of sampling or a completed turn. Poll errors back
off without `/close`; unknown submit outcomes require owner reconciliation via
`peerturn delivery resolve MESSAGE_ID --sent|--not-sent`. Replaced helpers and
helpers idle without authenticated requests exit.
Owner permissions, model, effort and tools are not modified by this plugin.

`inspectRuntimeContract($)` inspects callable APIs, the version object and native
UUID without starting a helper, submitting a prompt or sending a native frame.
Lifecycle semantics start as `not_observed`; malformed events close coauthor.
Missing submit capability leaves owner invitations and notices available; a missing
toast leaves coauthor available. Contract failures report `runtime_contract_changed`
with an update / doctor / rebind action and appear in daemon status after helper
reporting. Hooks always continue owner work. Shape checks cannot qualify origin,
nonce-consumption or lifecycle semantics of a future runtime.

Targeted fixtures: `node --test adapters/claude/tests/*.test.mjs`.
Validate the actual runtime with `claude plugin validate adapters/claude
--json`. Mock fixtures do not prove live binding, lifecycle or delivery.
