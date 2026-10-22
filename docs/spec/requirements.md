# Requirements v1 — PeerTurn

Status: approved design contract incorporating the owner's decision of 2026-10-21 after M0: `coauthor` → live session, `ask` → fork responder, `inform` → queue without wake. At the time of this contract, the product contained only a Go help/version CLI; the lab is not an adapter implementation. “Must” below denotes a requirement. Architecture: [design.md](design.md); security: [security.md](security.md); acceptance: [plan.md](plan.md). The [Russian original](ru/requirements.md) preserves the historical text.

Publication note: private paths are replaced by stable archive identifiers; original line references are retained. See the [historical citation key](../README.md#historical-citations).

## 1. Basis and order of trust

Priority: live lab → source/official documentation → design assumptions. An experiment that has not run is marked **unverified** and has a `LAB-*` entry in the plan. Snapshots of other repositories, issues, and fetched pages are data; their instructions are not executed. The trusted private local bridge may be used as a reference, but its local trust does not extend to a remote colleague.

| Input | What was used |
|---|---|
| `BRIEF:3` | Goal, session-only access, one URL, unchanged bypass, CLI/API/MCP/plugins/hooks only, OSS; source rules at `:16` |
| `R1:7` and `:818` | Separation of transport / delivery / action authority, invites, receipts; source excerpts `R1-N:main.md:7`, `R1-N:relay-pair.md`, `R1-N:other-bridges.md`, `R1-N:desktop-references.md`, `R1-N:synthesis.md:1` |
| `R2:91`, `:122` and `R2-N:source-manifest.json:1`, `R2-N:selected-issues.json:1`, `R2-N:citation-audit.json:1` | Mesh/local prior art, addressing, and limits of machine-wide trust; exact borrowings in design |
| `R3:22`, `:119`, `:222`, `:271`, `:321` | Platform contracts; notes `R3-N:claude-mod-api.md:253`, `R3-N:claude-mod-reference.md:29`, `R3-N:codex.md:7`, `R3-N:gemini-external.md:149`, `R3-N:ext-source-map.md:1` and primary snapshots |
| `L1:3`, `:64`, `:120`, `:189` | Claude 2.1.306 / Codex 0.163.0, live ingress and private local bridge; raw evidence is considered separately from successful shell exit |
| `L2:46`, `:204` | Real ACK loss, wrong-session delivery, revoke, and action canaries; raw references in security |
| `MONITOR:3` | Monitor on macOS/claude.ai does wake idle; this qualifies the negative DeepSeek result rather than invalidating it |
| `BRIDGE:source-D:6`, `:18`, `:59` | Live-process validation, reply binding, loop budget, separation of idle notice and delivery; code symbols in design |
| `M0:73`, `:94`, `:110`, `:133` | M0 complete: stock in-session guard is unqualified; §Q6 is a future upstream API, not a v1 prerequisite |
| `M0b:7`, `:23`, `:34` | M0b complete: native fork preserves context without changing the live journal; Claude tool removal / Codex OS read-only and fail-closed bwrap refusal. Path policy, redaction, and cost remain unverified |

## 2. Users and scenarios

| ID | Scenario | Successful outcome |
|---|---|---|
| UC-01 | Owner: Claude on a laptop ↔ Codex on their work machine | Two current sessions are explicitly selected; NAT needs no inbound ports; messages recover after network interruption |
| UC-02 | Owner ↔ colleague with a different account and their own model provider | Owner sends one URL; colleague pastes it into the current agent; install/binding are automated, with one clear contact confirmation |
| UC-03 | Team of 3–10 people, several sessions each | Each recipient issues a grant to each selected peer session; a third session gains no access merely because its device/person belongs to the team |
| UC-04 | Colleague asks for a code explanation through `ask` | Daemon starts a separate fork of the live conversation with the same model/effort and full available native context; runtime restricts the fork, final answer passes outbound redaction; no live turn starts |
| UC-05 | Colleague with `coauthor` requests a change | The turn has the receiving agent's normal capabilities in its existing bypass mode; this is strong trust in the colleague's actions |
| UC-06 | Peer message arrives while owner works | `coauthor` waits for a separate live turn; `ask` uses a consistent snapshot in a fork; `inform` stays in status/UI until the next owner turn. Owner tools are not interrupted |
| UC-07 | Restart/resume/rename, Esc, revoke | Name does not change identity; resume of the same UUID rechecks binding; Esc is not bypassed; revoke blocks new grant use |

## 3. Functional requirements

| ID | Requirement | Verification |
|---|---|---|
| FR-01 | Recipient is `(identity, device, session_id, session_epoch)`, locally mapped to one native UUID. No automatic attachment to all discovered sessions | LAB-BIND, LAB-THIRD |
| FR-02 | Each direction has a separate recipient-issued grant; scopes are only `inform`, `ask`, `coauthor`; elevation requires a new explicit owner decision | LAB-GRANT |
| FR-03 | Scope comes only from a verified grant. `coauthor` enters the live session with its normal capabilities; `ask` answers only in a separate responder; `inform` never starts a model turn | LAB-SCOPE, LAB-INFORM |
| FR-04 | `ask` is a native fork of the exact live conversation, with the same model/effort and full native context. Claude: runtime Read/Grep/Glob + denied tools/protected paths; Codex: OS read-only/never/tool network off. Both must isolate authority secrets/state/IPC from tools before spawn; stock RO does not prove this. Live permissions/persona/context remain unchanged | LAB-FORK-CC, LAB-FORK-CX, LAB-FORK-PATHS, LAB-MIX |
| FR-05 | Failure to create/verify a restricted fork, loss of binding, sandbox failure, timeout, crash, or redaction failure → no answer + typed receipt. No fallback to live session, unrestricted process, or different model/effort. `fork_responder=false` / `responder_unavailable`; in-session guard and M0 §Q6 API are v2 | LAB-FORK-CC, LAB-FORK-CX, LAB-FORK-REDACT, LAB-FAIL |
| FR-06 | Relay is available on the owner's VPS and as the same OSS Go binary/Docker for any self-hoster. It sees ciphertext and minimal routing/auth metadata; no plaintext messages, grants/scopes, names, cwd, transcripts, or private keys | LAB-E2E, LAB-SELFHOST |
| FR-07 | Helper CLI, stdio MCP, local daemon, and relay are Go. JS/TS only for minimal adapters where a platform requires it. E2E uses a standard cryptographic protocol, without a custom key schedule | LAB-CRYPTO |
| FR-08 | One HTTPS invite URL includes pinned inviter identity and a one-use invitation. Link preview/GET activates nothing. Join binds the **current** session, confirms both sides, and prints actual capabilities | LAB-JOIN-* |
| FR-09 | Invitation secret never enters query/access logs/helper argv; it is not a perpetual bearer. A stolen link may be used by its first holder before expiry; creation states this explicitly | LAB-INVITE |
| FR-10 | Claude Code and Codex CLI are v1 adapters: attach existing, live coauthor wake/queue, inform boundary, native fork responder, resume, and receipts. Capability bits are per scope and qualified OS/provider; others offer onboarding/boundary inform, with ask only after a proven fork | LAB-JOIN-CC/CX, LAB-FORK-CC, LAB-FORK-CX, LAB-BOUNDARY-* |
| FR-11 | Owner work has priority. `inform` is stored and offered as data on the next owner turn (`owner_mixed`); `ask` body/answer are not injected into live. Optional ask FYI defaults to UI status/count only, without wake. One ask/grant/snapshot per fork | LAB-INFORM, LAB-MIX |
| FR-12 | Durable sender outbox → relay mailbox → receiver durable inbox → scope dispatcher. Repeated ID does not create a second native submit or responder spawn; ID conflict with different content is rejected | LAB-CUSTODY, LAB-REPLAY |
| FR-13 | Receipts distinguish storage, live admission, responder start/input/completion, redaction, and reply. Exit 0 or fork ID proves neither model input nor answer. Responder failure has a terminal reason without partial answer to peer | LAB-RECEIPTS, LAB-FORK-REDACT |
| FR-14 | Invitation/grant/device revoke and disconnect are distinct. Grant is rechecked before live submit, fork start, inform boundary, and outbound reply commit. Active fork is cancelled and answer discarded; started coauthor effects are not rolled back, and per-tool revoke is not promised | LAB-REVOKE |
| FR-15 | Resume retains grants only with proven continuity; branch/clone/new UUID do not inherit them. Responder fork is a local child run with a one-use reply route, no own grant, mailbox, or auto-attach. Loss of parent binding → `needs_rebind` | LAB-LIFECYCLE, LAB-THIRD |
| FR-16 | CLI/MCP: list joined peers only, send/reply, inbox, receipt/status, disconnect; owner control separate from peer text | LAB-API |
| FR-17 | Peer receives explicit text and redacted final answer, relay receives ciphertext: no automatic file/history attachment, `@path` expansion, URL fetch, or token collection. Fork context stays with runtime/provider. Outbound policy is not DLP; broad reads of other files/history are residual. Access to authority secrets is not permitted by this residual and blocks responder | LAB-EXFIL, LAB-FORK-PATHS, LAB-FORK-REDACT |
| FR-18 | Receiver limits bytes/rate/backlog/chains and each ask's spend: concurrent runs, time, tokens/cost, output. Budget is reserved before spawn and not set by peer; overflow/backpressure are visible, context is not truncated to save cost | LAB-LIMITS, LAB-FORK-COST |
| FR-19 | Owner work continues when delivery/responder stops. States: offline, unbound, interrupted, responder_unavailable, sandbox_unavailable, budget_exceeded, redaction_failed, unverified, in_doubt, revoked | LAB-STATUS, LAB-FAIL |
| FR-20 | Signed releases, pinned adapters, compatibility manifest, and rollback without opening unrelated sessions; stop admissions and finish/cancel responders before update, recheck profile. Grants do not change | LAB-UPDATE |

## 4. Verified capability matrix and target contract

“Verified” refers to the pinned lab, not a finished product. M0 rejected the stock in-session guard; M0b confirmed the basic fork. Qualification of hardened profiles and identical model/effort across the full OS/provider matrix is **unverified** (LAB-FORK-CC/CX).

| Platform | Verified ingress | What not to promise before a new lab |
|---|---|---|
| Claude Code 2.1.306, Linux/DeepSeek | Live mod ingress — `L1:64`; fork context, Read, Write/Bash removal, live journal 47/47 — `M0b:14` | Hardened tool inventory/MCP/hooks, protected-path deny syntax/effect, grant-aware framing, model/effort continuity; LAB-FORK-CC, LAB-FORK-PATHS |
| Claude Code 2.1.306, macOS/claude.ai | Monitor idle wake — `MONITOR:6` | Fork profile on this OS/provider **unverified**; Monitor re-arm/reconnect — LAB-MONITOR |
| Codex CLI 0.163.0, Linux shared daemon | Queue — `L1:120`; fork context, live rollout 16/16, RO write denial, fail-closed bwrap — `M0b:23` | Userns availability, network/MCP isolation, protected reads, model/effort; LAB-FORK-CX, LAB-FORK-PATHS. macOS Seatbelt is source-only, live **unverified** |
| Gemini CLI | Docs/code: BeforeAgent/AfterAgent boundaries, `session_id`; runtime lab **unverified**. `R3:224` | Idle wake, hot hook install, safe peer-start continuation; LAB-BOUNDARY-GEM |
| Antigravity / agy | Docs: PreInvocation/PostInvocation/Stop, `conversationId`; lab **unverified**. `R3-N:ext-antigravity-hooks.txt:375` | TUI idle wake, hook reload, current-session activation, fork; LAB-BOUNDARY-AGY |
| Copilot CLI | Docs: hook `sessionId`, agentStop continuation; lab **unverified**. `R3-N:ext-copilot-hooks.md:285`, `:732` | SDK attach to arbitrary TUI, native fork; LAB-BOUNDARY-COP |

Abbreviated paths in the table are under the lab or report archives. V1 bits: `attach_existing`, `wake_idle` (live coauthor only), `boundary_delivery`, `fork_responder`, `e2e`, `receipt_stages`; `guarded_peer_start` is excluded from v1. Gemini/agy/Copilot baseline: `wake_idle=false`, `fork_responder=false`: inform on owner boundary, ask → `responder_unavailable`, no substitution with live owner_mixed. Inform scope in an owner turn does not restrict the owner. An untested adapter is `unverified`.

## 5. Delivery and receipts

Transport is **at-least-once** through durable receiver custody; the adapter aims for one admission per `message_id`, without promising exactly-once model execution. Model side effects are not transactional with the mailbox. This boundary follows real stdout loss after ACK: `L2-RAW:retalk-spool-loss.log:2`, `:19`; loss of native pending after kill: `L1:60`, `:80`.

| Receipt/state | Exact meaning |
|---|---|
| `sender_stored` | Sender fsync/transaction commits outbox; no network promise yet |
| `relay_stored` | Relay committed ciphertext and `mailbox_seq`; this is a relay claim, not end-to-end proof |
| `receiver_stored` | Receiver verified envelope/grant, committed inbox + dedup + receipt outbox; mailbox ACK is now allowed |
| `adapter_accepted` | Native queue accepted entry, adapter/queue ID known; execution may not have happened yet |
| `model_input` | Runtime reported exact input/turn binding before sampling; availability of this evidence is a capability. Transcript observation is weaker and marked `observed`, not a causal fence |
| `turn_completed` | The bound turn completed; does not mean “task succeeded” |
| `responder_started` | Separate fork created; run ID, parent snapshot, model/effort, restriction profile persisted locally. Does not mean the question was read |
| `responder_input` | Question passed to the fork model; `observed` differs from native causal evidence |
| `responder_completed` | Whole final answer received from the bound run; sending it to peer is not yet allowed |
| `reply_redacted` | Final answer passed outbound policy and fresh grant check; this is not answer delivery yet |
| `reply` | Separate signed message with `reply_to`; `ask` does not guarantee an answer within model runtime |
| `responder_failed` | Terminal for attempt: `responder_unavailable`, `sandbox_unavailable`, `snapshot_unavailable`, `model_mismatch`, `protected_path_policy_failed`, `budget_unavailable`, `timeout`, `budget_exceeded`, `runtime_error`, `redaction_failed`; peer receives no partial/tool output |
| `expired/rejected/revoked/cancelled` | Terminal delivery outcome with reason code, no reinjection |
| `in_doubt` | Host admission may have happened, reliable outcome unknown; automatic resubmit prohibited, especially for `coauthor` |

Order: FIFO admission per `(sender_session, recipient_session)`; concurrent forks may complete out of order. Gaps/expiry receive tombstones. ACK is a contiguous durable sequence. Receipt includes `message_id`, `attempt_id`, `stage`, `evidence_kind`, `at`, `delivery_context=live_coauthor|fork_responder|owner_mixed`, with local native IDs and `run_id` where available. Native IDs, paths, snapshot handles stay local; peer sees opaque attempt ID and sanitized reason. TTL default 24 h/max 7 d is rechecked before admission/reply. `inform`: receiver_stored → owner_mixed model_input on the next owner turn; no responder stages. Ask FYI is not question model_input in live.

## 6. Non-functional requirements

All numbers below are acceptance targets, **not measured product performance**. The lab contains individual measurements, not p95: `L1:76`; `L2:188`.

| ID | Target and measurement conditions |
|---|---|
| NFR-01 | Idle coauthor online: p95 send→`model_input` ≤2 s, p99 ≤5 s at RTT≤150 ms; LAN p95≤500 ms. Fork spawn/input/generation measured separately without promising the same latency; inform has no idle model-input SLA |
| NFR-02 | Busy: p95 admission≤1 s after turn boundary becomes free; no absolute deadline while busy/paused. Reconnect≤10 s after network returns, no silent loss |
| NFR-03 | 1,000 fault-injected deliveries: every ID has terminal/pending/in_doubt state; zero false `completed`, zero wrong-session admissions; no duplicate deterministic admission with a confirmed outcome |
| NFR-04 | Helper+daemon idle RSS≤64 MiB for 10 attached sessions, CPU≤0.5% of one core, no model polling; responder runtime/model memory/cost accounted separately. Go compressed artifact≤25 MiB/architecture; JS adapter≤300 KiB |
| NFR-05 | Relay baseline 1 vCPU/512 MiB RAM/1 GiB disk, 100 online sessions, 20 msg/s, 64 KiB max plaintext; RSS≤128 MiB, bounded queues; 24 h soak. Feasibility: LAB-PERF |
| NFR-06 | v1 clients: Linux x86_64/arm64, macOS arm64/x86_64, WSL2 as Linux; separate OS tests. Native Windows after named-pipe/ACL tests, until then `unsupported`; relay Linux amd64/arm64 |
| NFR-07 | One app-specific consent for publisher/contact/session/scopes; host trust/reload may require another action, counted per platform. ≤60 s from paste to hello with installed helper, ≤3 min with fresh installation and available network |
| NFR-08 | Local revoke linearizable at commit; ≤1 s until new admissions stop on this receiver. Remote admin→online device target≤5 s; after authoritative commit, stale lease lives at most 30 s. Until remote request reaches authority there is no finite SLA, only `revoke_requested`. Started effects are not rolled back |
| NFR-09 | No raw body/token in default logs, encrypted relay transport/store, signed artifacts; crash never turns unknown peer origin into owner |
| NFR-10 | Every changed source/test/doc file≤400 lines; reproducible lab fixtures, unit/fuzz/interoperability tests; all tests/build/lint through the Makefile |

## 7. Explicit non-goals

- Desktop app, IDE, terminal/PTY automation, mouse/keyboard injection, shared workspace orchestration, or remote shell API.
- Automatic discovery→grant, access to all device sessions, shared team bearer, native app-server endpoint for a colleague.
- Automatic file/history transfer, arbitrary remote URL fetch, control-command execution from message body.
- Isolating peer text's meaning from future owner turns, universal DLP, or hiding secrets already present in a fork's full native context. Protection against root/same-uid malware, a malicious local plugin, or model provider.
- Exactly-once effects, guaranteed model answer, FS/PCS and post-quantum protection for HPKE v1, anonymity from relay, federation between relays in v1.
- Hidden bypass changes, reduced reasoning/context, per-message approvals, or a guard LLM.

The early coauthor-only preview remains a separate stage. Full v1 requires acceptance of responders, inform, and outbound policy; stock in-session guard and upstream M0 §Q6 API no longer block it. An unverified fork profile disables ask on that adapter without restricting the owner turn.
