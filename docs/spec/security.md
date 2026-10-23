# Fork responder and live delivery security

2026-10-21, owner's decision after M0/M0b. V1: `coauthor` is unrestricted live session; `ask` is separate runtime-restricted fork; `inform` is storage/UI and next owner turn without wake. Live permission mode, model/effort/persona/context do not change. This is a design contract: the Go CLI skeleton did not implement these mechanisms. The [Russian original](ru/security.md) preserves the historical text.

Publication note: private paths are replaced by stable archive identifiers; original line references are retained. See the [historical citation key](../README.md#historical-citations).

## 1. Threats, evidence, and trusted components

| Attacker / failure | Protected property | Mechanism / residual risk |
|---|---|---|
| MITM / malicious relay | Body, grant, key confidentiality/authenticity | TLS + pinned HPKE/JWS; relay sees routing/traffic metadata and may delay/delete ciphertext |
| Stolen invite | No lasting or machine-wide access | 10 min TTL, one use, exact recipient-issued grant; first holder may beat legitimate colleague |
| Pending owner invitation / runtime pointer | No model transcription or wrong-runtime authority | Memory-only capability handoff ≤8/session, ≤10 min, exact epoch/thread; private same-UID IPC and validated runtime pointer; same-UID compromise remains trusted-boundary failure |
| Admitted malicious peer | Scope/body never elevate authority | Verified scope dispatcher: ask never enters live, inform never wakes, coauthor means strong trust |
| Prompt injection in question, source, inherited context | Text cannot remove responder restrictions | Claude runtime tool removal / Codex OS sandbox, fixed launch profile; model may still lie, over-refuse, disclose secrets |
| Protected-file reads / history secrets | Do not expose signer/admission authority or expected secrets | Mandatory pre-spawn authority isolation; Claude path deny + outbound policy. Codex stock RO does not hide files, separate profile required; other secrets/history remain residual |
| Stale binding/snapshot, concurrent fork | No wrong-session input/reply or merging different grants | Exact parent epoch/instance, snapshot handle, separate run/output/budget; shared cwd is not authority |
| Crash/helper death/sandbox unavailable | No unrestricted fallback or false reply | Admission preflight, runtime boundary, supervisor, whole-answer buffer, typed receipt; ambiguous → in_doubt |
| Flood/full-context asks/loops | Bounded run count, runtime/output and concurrency | Receiver-owned reservation, daily/hourly counts, deadline/output limits; no billed monetary ceiling |
| Compromised runtime/plugin/uid/root/publisher/provider | Honest protection boundary | Trusted computing base; E2E does not protect against local/runtime/provider compromise |

TCB: OS and agent runtime, Go daemon/supervisor/outbound policy, minimal adapter, publisher, crypto libraries. Responder uses provider credentials only for model request; they are never supplied in question, env dump, or tool-visible PeerTurn credential. Native runtime may write its own session metadata/transcripts outside tool sandbox. Read-only promise applies to responder-tool effects rather than zero physical writes on machine.

M0 completed with negative in-session guard verdict: Claude throw/timeout/unload permit effects; Codex has stdin gap and no synchronous queue→turn mapping (`M0:73`, `:94`, `:110`). §Q6 (`:133`) remains a **future v2 option**, not v1 blocker. Do not rewrite its history/raw evidence.

M0b complete: Claude fork inherited context, Read worked, runtime removed Write/Bash, live journal 47/47; Codex fork inherited context, live rollout 16/16, kernel denied write, bwrap userns failure executed no commands (`M0b:14`, `:29`). Basic mechanism, not qualification of protected paths, network/MCP, budgets, snapshot races, or full OS/provider matrix.

Original action-authority risk remains: peer “owner approved” triggered Bash canary under bypass through prior art (`L2-RAW:latency-evidence-extract.log:11`); body `ctrl:despawn` killed pane (`L2-RAW:agmsg-expert-canary-control.log:2`). Framing/signature are not a sandbox.

## 2. Grants and admission

| Scope | Message destination | Authority / receipt |
|---|---|---|
| `inform` | Durable inbox, status/UI; data on next real owner turn | Never peer-started turn; owner_mixed does not restrict owner tools; no automatic reply |
| `ask` | Native fork of exact live conversation | Runtime restrictions for entire child run; final answer only through daemon redaction and bound reply route |
| `coauthor` | LIVE session: Claude mod submit / Codex queue | Receiving agent's normal capabilities; strong trust, **not sandbox** |

Grant is recipient-issued, directional, exact identity/device/session/epoch. Sender requests subset only; `kind=coauthor` under ask → reject. Body, display name, reply ID, “owner approved” never change scope/launch settings. Elevation is separate owner control. Coauthor may edit, read secrets, use network, change config, weaken future protection within host permissions; warning required before join.

Pipeline: bound transport principal/size → HPKE/JWS/cert chain → exact audience/epochs → current grant/expiry/revoke → dedup/conflict → budgets → durable inbox → scope-specific capability/snapshot → live submit or fork spawn or inform pending. Envelope rejects authority fields `argv/settings/approval/toolCall/native_session_id`. Peer text is separate data, not control opcode or shell interpolation.

Recheck current grant before fork start, inform boundary, live submit, reply outbox commit. Reply commit and local revoke serialize: after revoke commits no new reply is authorized. Already committed reply may have gone over network; no retroactive cancellation. Runtime without fork profile never gets ask in live as fallback.

## 3. Native fork: authority, context, and framing

`ResponderRun` locally binds `message_id/attempt_id`, exact parent binding/epoch/instance, committed snapshot boundary, native fork ID, model/effort, profile hash, budget, one-use reply route. Body chooses none of these handles. Fork is not joined session: no new grants, mailbox, signing keys, peer discovery, or message sending into live/third session.

Daemon launches fixed absolute executable + argv with private input, without shell concatenation. Admission requires `authority_secrets_isolated=true`: tools cannot read PeerTurn signing/decryption keys, unlocked state/admission/grant-control credentials, transport tokens, live-control IPC/auth, process handles enabling live authority. Check files/env/descriptors/proc inspection/symlinks and broker ACL, not merely tool list. Otherwise `protected_path_policy_failed`, no spawn. Native runtime/TCB needs provider auth; never forward it to tools through env dump. Clean env does not hide credential files or satisfy gate alone.

### Claude

Native `claude -p --resume <id> --fork-session --permission-mode default` + `--allowedTools Read Grep Glob` and `--disallowedTools Bash Edit Write NotebookEdit WebFetch WebSearch Task`: M0b `M0b:10`. Live bypass unchanged; separate child gets default. M0b proved Write/Bash removal, not every tool/version/integration.

`--allowedTools` alone only auto-approves (`CC-DOC:cli-reference.md:67`). Hardened profile fixes `--tools Read,Grep,Glob`, denies MCP/Agent/new effectful tools, disables inherited hooks/plugins/skills/commands, provides no subagent/scheduler/session-send capability. Flags source-only `:87`, `:131`, `:138`; full combination **unverified**, LAB-FORK-CC. Never permit hidden hook shell command merely because Bash is absent from model inventory.

### Codex

Native `codex exec -c sandbox_mode='"read-only"' -c approval_policy='"never"' fork <thread-id>`; overrides before fork, `--sandbox` after fork rejected (`M0b:26`). OS restricts tool subprocesses and descendants; never excludes approval escalation, grants no sandbox bypass permission. Arbitrary shell need not be pure: runtime/kernel block writes/network, CPU/read side channels remain.

Linux bwrap requires unprivileged user namespaces. Activation/doctor test effective sandbox in launch environment; sysctl does not rule out AppArmor/seccomp denial. M0b: ordinary container refused without execution, seccomp-unconfined lab allowed read + denied write (`:30`). Do not weaken host policy automatically or retry unsandboxed. Source: `CX-SRC:sandboxing/src/bwrap.rs:28`.

macOS: Seatbelt, source `CX-SRC:core/src/sandboxing/mod.rs:179`; live native fork+Seatbelt **unverified**. Default RO network off: source `CX-SRC:protocol/src/protocol.rs:1314`, needs live negative control. Tool network off does not block runtime API to provider. Exclude inherited MCP/hosted services, plugins, shell init/config, remote send, shared jobs or separately prove them restricted: sandboxed shell does not protect external MCP service. Full profile **unverified**, LAB-FORK-CX.

### Grant-aware framing and snapshot

Daemon framing states verified recipient grant: whom owner authorized to read/analyze/reply, scope, that peer body is question/data, what runtime prohibits. Never claims peer became owner, quote is system command, or grant permits all secrets. Model retains persona/useful native context; security does not depend on obedience. DeepSeek in M0b over-refused even permitted question (`M0b:19`): test useful benign answer separately from denying fixture-secret disclosure.

Snapshot is committed conversation prefix, not copying transcripts into transport. Same model/effort verified by effective metadata; unknown/mismatch → refusal, no cheap model/summary fallback. Busy session needs immutable native boundary or wait; torn/unknown boundary → snapshot_unavailable. Files may change during fork: history snapshot gives no atomic filesystem view. Full-context memory/tool-result-slot inheritance and concurrent snapshot **unverified**, LAB-FORK-CC/CX, LAB-MIX.

## 4. Live session, owner_mixed, and concurrent forks

| Event | Decision |
|---|---|
| Inform while idle/busy | Storage/status, Claude mod UI where available, no wake; next owner input gets fixed data framing and owner_mixed receipt |
| Ask while owner works | Separate fork on proven prefix or wait for boundary; no restriction/interruption of owner turn/tools |
| Ask FYI | Configurable, default UI status/count without body/answer or model turn; owner explicitly opens result |
| Coauthor while busy | Native next-turn queue, no steer; unrestricted live turn with verified peer label |
| Owner input during fork | Live session only; never steer responder or change scope/profile |
| Two asks/different peers | Default parent serialization; permitted parallelism uses separate fork IDs/snapshots/budgets/output, no shared mutable responder |
| Owner reads inbox/result/peer advice | Owner_mixed unrestricted; peer text may influence future actions, no semantic taint-tracking promise |
| Esc/parent epoch change/disconnect | Close new admissions, cancel corresponding responders, discard late output; owner tools continue |

Gemini/agy/Copilot boundary: inform only at owner boundary, `wake_idle=false/fork_responder=false`, ask → responder_unavailable. Coauthor boundary possible with explicit unrestricted capability; do not replace ask with mixed owner turn. Sources: `GEM-SRC:docs/hooks/reference.md:114`, `R3-N:ext-antigravity-hooks.txt:490`, `R3-N:ext-copilot-hooks.md:404`, `:407`. End-to-end **unverified**.

Default one fork/parent, two/daemon, one outstanding ask/conversation. Increased owner policy never mixes output/grants. Concurrent forks may finish out of FIFO order; reply always bound by message/attempt. Fork merges no history/edits into live. Child transcripts/result caches local with private permissions/retention, never peer inbox attachment.

## 5. Fail-closed responder without blocking owner

| Failure | Behavior |
|---|---|
| Unknown build/profile/model/effort/snapshot | Ask does not start; responder_failed with responder_unavailable/model_mismatch/snapshot_unavailable |
| bwrap/userns/Seatbelt unavailable or effective restrictions unproven | sandbox_unavailable, fork_responder=false, no unrestricted retry |
| Claude denied tools/path policy not applied, unknown integration | protected_path_policy_failed or responder_unavailable; no peer answer |
| Helper/daemon crash, runtime timeout, nonzero exit | Supervisor/recovery cancels child group; no whole validated final answer → no reply, typed receipt |
| Redactor timeout/error/invalid encoding/oversize output | redaction_failed or budget_exceeded; no raw/partial fallback |
| Grant expires/revoked before reply commit | Cancel run/discard output, revoked/expired receipt; already read context cannot be erased |
| Crash between spawn/commit/send | in_doubt, reconcile by run/attempt/reply ID; no blind resubmit/duplicate paid fork |

If daemon dies, peer sees pending/in_doubt meanwhile; receipt publishes after durable recovery, no instant receipt from dead process promised. Child gets no signing/transport credentials and cannot send answer itself. Native fork restrictions survive supervisor failure; orphan cancellation and resource cleanup require LAB-FAIL qualification. Answer must await positive completion, outbound policy, fresh grant even if process could still have read data.

No live permission changes or global onFailure:block. V1 does not depend on hook origin→tool fence. In-session guard, complete dispatch coverage, M0 §Q6 required-guard API remain v2 research; only separate owner decision returns them to scope.

## 6. Protected paths and read boundary

Protected roots: PeerTurn keys/state/admission/IPC, host/provider credentials/token stores, SSH/cloud credentials, private configs/integration executables, owner-configured paths, project .env/secrets. **Authority subset** (PeerTurn keys, grant/admission/control credentials/state, transport/live-control IPC) must be isolated on both runtimes before ask regardless of broader read policy. Leaked signer key enables forged identity/grants; cannot accept as encoded-exfil residual or compensate with redaction. Ordinary source stays accessible. Redactor never collects credentials from disk; receives only already known core secrets/patterns.

Claude gets private `--settings` with `permissions.deny` `Read(...)` rules; example and absolute `//` syntax: design §8.1. Docs: deny priority `CC-DOC:permissions.md:58`, path/tool matching `:352`, root `:362`, symlink handling `:459`. **Unverified** on resumed fork: effective merge, exact syntax for every protected root, Grep content/Glob metadata, home aliases, symlinks/hardlinks/TOCTOU. Do not replace Read deny with invented `Glob(path)` deny. If mandatory Claude rules do not work, ask stays disabled until fixed.

Codex stock RO denies writes but reads uid-accessible files including credentials; network off does not prevent answer exfiltration (`M0b:37`). This **fails mandatory authority isolation** while keys/state/IPC remain accessible to same uid. Need qualified runtime read-deny/isolated file view, credential broker, or service account without these data/handles; native fork/provider auth compatibility **unverified**, LAB-FORK-PATHS. Do not invent native deny flag. Until proof, `authority_secrets_isolated=false` → ask unavailable. After gate, broad reads of other files may remain residual (`protected_paths_enforced=false`); no confidentiality sandbox/DLP promise.

On both runtimes, denying new reads does not hide a user secret already in full inherited context. Prompt injection may request paraphrase/encoding: residual, outbound filter not universal DLP. **Known core exposure of active authority secret** is separate: ask unavailable until revoke/rotation/rebind, not “acceptable context residual” or reliance on redaction. No universal history scan for unknown secrets promised. Do not trim owner context to work around refusal. Absence of write canary proves neither confidentiality nor answer quality.

## 7. E2E, invites, rotation, and revoke

Crypto profile and exact wire: design §4–7. Before model delivery: HPKE integrity + JWS Ed25519 + pinned cert chain + scope/audience/epoch. Sender-controlled display fields separate from identity. Key mismatch never fixed by automatic repeat TOFU; `needs_verify`. Root rotation requires old/new cross-sign and pin update; old-root loss/compromise requires new human verification. Self-signed “new key is me” through relay is insufficient proof.

Session encryption keys rotate every 24 h/on compromise; session/device chain signs new public keys, old keys retained at most permitted message TTL+grace to decrypt backlog. Do not delete old key before expected-message custody/reconciliation completes; deleted-key ciphertext cannot be recovered. Session epoch changes on continuity loss, grant epoch on revoke/change; not interchangeable. Private-key backup needs explicit owner export and separate encrypted artifact; still no forward secrecy promise.

Invitation secret never sent as query bearer. Invite proof-key signature, encrypted descriptor, signed claim use standardized primitives only; single-use mechanism is application-level. Relay atomic claim and inviter's **local authoritative** consume transaction prevent two grants under relay replay. Retalk confirmed sequential one use; concurrent race still **unverified** (`L2-RAW:rt-one-use-second-watch.log:2`), LAB-INVITE. Forwarded URL lets first holder claim own key; trusted link-transfer channel establishes human fingerprint, with out-of-band comparison for higher assurance.

Owner decision, **2026-10-23**, after mac4/mac5: eligible owner hooks parse/normalize the owner's explicit paste and pass the extracted capability over existing private same-UID IPC, bound to Claude's native epoch or Codex's exact host-owned `_meta.threadId` match. MCP `peer_join({})` accepts no invitation text; its optional random nonsecret `handle` selects a pending entry. `routing_hint` contains only `/i/` and six public routing-ID characters, never fragment data. Only `{handle, relay_host, routing_hint, age_seconds}` appears in `owner_invitations` status or ambiguous-selection errors; no inviter label before descriptor authentication. Missing, multiple or expired entries produce typed refusal and owner repaste/selection. Hooks discard malformed candidates; direct CLI parsing returns `malformed_invitation` with an unchanged-paste instruction, never reconstruction or relaxed parsing.

Threat delta: the daemon now holds up to eight pending capabilities per session in process memory for at most ten minutes; same-UID native host/hook IPC is trusted, not independent human-origin authentication. Use, expiry, unbind/rebind, stop and restart clear entries. Pending secrets/prose are never persisted, logged, put in receipts/audit/status or replayed from transcripts. Post-join sealed onboarding recovery state remains separate. The owner paste still reaches provider/native transcripts. Coauthor remains strong trust and peer text may influence later owner turns. Historical lab outcomes are unchanged; a new live run is still required.

The daemon's runtime pointer is nonsecret discovery data in its private state directory, written atomically and removed on clean stop. CLI/MCP consult it only without explicit `XDG_RUNTIME_DIR`, before macOS TMPDIR fallback. Bounded no-follow reads, owner/private-permission checks and validation of the pointed directory/socket's UID/private permissions are mandatory. A hostile pointer fails closed without scanning or fallback. It never replaces native-session proof or authorizes another session.

Revoke:

1. Owner `grant revoke` atomically raises authoritative epoch, closes new live/fork/inform admissions and reply commits, quarantines pending inbox, cancels responder runs; audit/receipt outbox committed together. Already admitted unrestricted coauthor has no per-tool fence: no effect rollback or promise to stop every dispatch.
2. Adapter removes unconsumed coauthor queue entries if API allows. Strict dequeue revoke after native acceptance unproven: `in_doubt`/`already_admitted`, best-effort cancellation, no per-tool revoke promise. Active fork cancelled, late answer unauthorized. Inform already in owner context cannot be erased; owner turn/jobs never stopped.
3. Online relay route-permit revoke and stream close target≤5 s; local receiver publishes effective status≤1 s after commit; that commit already prohibits new admissions/reply commits. Already admitted coauthor unaffected. Transport auth does not replace admission/reply checks. L2 saw post-revoke stream event through caching (`L2-RAW:rc-observer-revoke-connected.log:5`, `:7`, `:9`).
4. Default authority: receiving session on its local daemon. If another owner device is policy authority, recipient-signed grant pins its public key and policy epoch beforehand; receiver uses signed leases≤30 s without peer/relay refresh. After remote authoritative commit old lease may still allow admissions/reply commits until expiry, at most 30 s; `effective` only after receiver ACK or all old leases expire. Signed issued_at/expires_at become monotonic deadline with conservative clock-uncertainty subtraction; delayed delivery gives no fresh 30 s from receipt, clock rollback never extends lease. Unknown clock bound prohibits new admissions/reply commits. When remote revoke **has not reached authority yet**, `revoke_requested`, no finite SLA; enqueueing command is not effective revoke.
5. Invite revoke prohibits new join without revoking issued grants. Confirmed distinction: Bob kept writing after invite revoke (`L2-RAW:retalk-revoked-invite-contact.log:2`). Session disconnect and device/root revoke separate; cached policies removed by epoch.

## 8. Limits, outbound, and audit

Baseline: plaintext≤64 KiB/ciphertext≤96 KiB, UTF-8; 10 accepted messages/min/grant, burst 5; ≤20 automatic coauthor turns + responder runs/10 min/session, hop≤8. Inform spends no model turns. One active fork/parent, two/daemon, one outstanding ask/conversation; fair per-peer queues/control lane. Receiver never lets peer reset limits with new conversation/message ID. Overflow→backpressure/TTL without context truncation.

**Owner decision, 22 October 2026, v0.1:** monetary qualification is removed as an admission prerequisite, superseding the previous unverified $1/run and $10/day proposal. Reserve before spawn: deadline 120 s, ≤8 tool calls, output≤16 KiB, one active run per parent/conversation, two per daemon, six asks per peer per rolling hour, and owner-configurable 24 asks per UTC day per daemon by default. Reservations, including failures and ambiguous launches, count durably; new IDs and restarts do not reset usage. Full native context and the startup probe are billable; cache savings are not guaranteed. Spend per ask is bounded only by these limits and provider behaviour, with no billed-dollar ceiling. No model/effort/context reduction to meet a quota. Kill/retry never undo provider billing.

`ask` reply never automatically creates new ask; receipts/hello/heartbeats/typing never wake model or get model reply. Child responses inherit verified budget_id/parent chain. Group fan-out billed/limited per recipient. Local private-local-bridge precedent: pair bucket and hops `BRIDGE:source-D:15`; exact new values are design baseline, LAB-LIMITS.

Outbound: explicit text-only send/reply; ask only whole final answer, no reasoning/tool output/debug events. Go core checks fresh grant/reply route/size/rate, deterministic secret patterns and already known core credential values, emits `[REDACTED:<class>]` and local count. Reply commit serializes with revoke. Error/timeout/invalid output → typed failure, no raw fallback. Reads nothing automatically from `@path`, URL, image reference, transcript, `.env`. Dangerous payload may be rejected with clear reason, not silently summarized to change meaning. Arbitrary encoded secrets, patternless personal data, other network tools, coauthor consequences outside this redaction; read-only `ask` may still disclose context/file data in answer. Encoded/paraphrased secrets and redactor mistakes are residual; LAB-FORK-REDACT must show detection controls and limits.

Audit JSONL/local DB: actor fingerprints, session/turn IDs, grant ID/epoch, action, policy decision/reason, message ID/ciphertext digest, receipt stage/evidence, version/config hash, monotonic event seq. No body, raw tool input, token, full invite URL, env dump, full user path. Ciphertext digest preferred to plaintext hash to avoid guessing short messages from logs. Append-only permissions and signed checkpoints detect some corruption, but same-uid compromise may forge/delete journal; not forensic immutable audit. Retention 30 d, opt-in encrypted debug with TTL and warning about plaintext in native host transcript.

## 9. Supply chain and relay hardening

- Signed manifest and hashes verified **before execution** of installer/helper/plugin. Invite-pinned release cannot choose arbitrary publisher or mutable HEAD. Bootstrap page is data, not command. First-use publisher trust is separate from peer identity; the installer pins the keyless CI workflow/tag identity and OIDC issuer, verified using trusted Sigstore roots.
- Go modules/toolchain pinned, SBOM, vulnerability checks, reproducible artifacts. New shell/parser/SQLite/signature dependencies get M1 review; no random package from matching npm name. Signature does not replace code review.
- Relay non-root, bounded JSON/headers/bodies/timeouts, quotas default on, per-request auth, WSS revalidation/revoke, separate admin/private metrics. No outbound user-URL fetch; helper bootstrap fetch only trusted-publisher HTTPS, redirect allowlist, prohibit loopback/metadata/private destinations except explicitly selected local-test profile.
- Invite HTML without analytics/third-party resources, no-referrer/no-store; fragment never enters telemetry. One-use Monitor URL notify ticket redacted from access logs, no content read, TTL≤60 s before upgrade. Native sockets/tokens never become relay credentials.
- Update: suspend peer admission, verify signed compatibility, atomic binary/config swap, probe scope-specific live/boundary/responder profiles, then resume. Compatible rollback never restores old revoked grant epoch. Backup restore below monotonic epoch needs re-enrollment; otherwise rollback could resurrect access.

## 10. Residual risks and acceptance conditions

1. `coauthor` is strong trust with normal host capabilities; may read secrets, act, influence future configuration.
2. Peer text in owner_mixed, history, explicitly opened ask result influences unrestricted owner reasoning; semantic injection not isolated.
3. Read-only fork retains prompt injection, inference/encoded exfiltration, broad Codex file access; path deny never cleans native history, redaction not universal DLP.
4. History snapshot may become stale, filesystem is not atomic, parallel forks see different revisions; correctness/answer availability not guaranteed.
5. Model cost/cache/usage estimates may differ from bill, cancellation does not refund; limits/accounting need live qualification.
6. Runtime/uid/plugin/publisher/provider compromise outside E2E boundary; local child transcripts host-managed. HPKE Base+signatures lacks FS/PCS, relay sees metadata.
7. Invite possession does not prove identity; revoke never erases known data or rolls back admitted coauthor/committed replies; remote partition gives no SLA before authority commit.

V1 safety acceptance: LAB-FORK-CC/CX, LAB-FORK-PATHS, LAB-FORK-REDACT, LAB-FORK-COST, LAB-INFORM, LAB-FAIL, LAB-MIX, LAB-INVITE, LAB-REVOKE, LAB-EXFIL. M0b proves architecture feasibility; hardened profiles remain **unverified**. Never claim protected-path containment where absent or mark supported from one canary. Stock in-session guard/API is future v2, no longer full-v1 blocker.
