# mac7 — macOS acceptance with normal-user runtime discovery

**Overall: FAIL for the requested six-scenario qualification.** Scenarios
**1, 2, 4, 5 and 6 PASS**. Scenario **3 FAIL**: the first B→A owner paste
returned `conflict/rejected` because scenario 2 had already connected that
same pair. This is the existing peer-binding rule, not evidence that the
Claude owner hook lost the invitation. A fresh B→A pair was not substituted.
The fresh C→A selected-handle join did succeed. Harness corrections below
mean this is not a pristine, minimum-step onboarding demonstration.

Live native checks: **23 October 2026, approximately 12:55–13:16 UTC**,
isolated macOS arm64 host (macOS **27.0**). Cleanup and expiry verification followed.

Raw evidence names below are archival references; only this sanitized summary is public.
Relay URLs below are placeholders, not the tested deployment address.

## Build and harness

| Item | Evidence |
| --- | --- |
| Source | `fix/onboarding-mac`, **b2fab52eded572560a0e115d90adc69a9ed72425**, extracted with `git archive`; historical research excluded. `source-identity.json`. |
| Build | Cached Go **1.27.3**, offline modules, local **Makefile build via the check runner**, exit **0**. No product changes or full product CI rerun. `build.txt`, `build.exit`, `versions.json`. |
| Binary SHA-256 | `deea6c1cf3a96bac4a7aa5ff5b0f866d971526062f733331ff8dfaaaa8b3deff` |
| Installation | Same unsigned local release-layout staging as mac6: archive/package copies, Codex plugin binary copy, inventory and explicit setup `--path` / `--sha256`. No publisher-attestation claim. `build-identity.json`. |
| Native hosts | Isolated npm Claude **2.1.306** for A/C; standalone Codex **0.163.0**, shared daemon, for B. An isolated model gateway test key, **gpt-5.4-mini** on all three; DeepSeek unused. |
| Before first TUI | Codex `check_for_update_on_startup=false`; isolated native daemon `app-server-daemon/settings.json` has `updater.autoUpdateEnabled=false`; Claude updater/nonessential traffic disabled. No Codex update cache/new daemon version observed. `startup-update-controls.json`, `cleanup-before-purge.json`. No packet-capture claim. |

`R=<private-test-root>` was **0700**. The pre-existing root was
empty. A/B/C launch environments used the following own, private directories:

| Side | HOME | TMPDIR | CODEX_HOME | npm cache | CLAUDE_CODE_TMPDIR |
| --- | --- | --- | --- | --- | --- |
| A | `$R/a/home` | `$R/a/tmp` | `$R/a/home/.codex` | `$R/a/npm-cache` | `$R/a/tmp/claude` |
| B | `$R/b/home` | `$R/b/tmp` | `$R/b/home/.codex` | `$R/b/npm-cache` | `$R/b/tmp/claude` |
| C | `$R/c/home` | `$R/c/tmp` | `$R/c/home/.codex` | `$R/c/npm-cache` | `$R/c/tmp/claude` |

**XDG_RUNTIME_DIR, XDG_CONFIG_HOME and XDG_STATE_HOME were unset.** Native
child checks also show all five required variables inside mac7 and all three
XDG variables absent: `a-native-isolation.json`, `b-native-isolation.json`,
`c-native-isolation.json`. Some model shell commands chose other subpaths
inside their own side for npm/Claude tmp; these are recorded as actual values
in those files. No claim of arbitrary MCP-child environment inheritance is made.
All three daemon pointers used macOS default private state under their isolated
HOME; targets were private canonical side TMPDIR runtimes.
`runtime-pointer-profile.json`.

Credentials were sourced privately from the authorized env file, never put in
argv or evidence. Copied mac6 paths were changed to mac7 before execution.
Initial helper argv still used some `/tmp` aliases; see H1 below. There was no
manual MCP registration, environment forwarding, copied pointer, synthetic MCP
call, join CLI fallback, identity re-initialization or relay-host access.
A/C preview labels configured initial storage only; native tool-child binding
and real host tool calls provide the native evidence.

## Scenario verdicts

| Scenario | Verdict | Evidence and limit |
| --- | --- | --- |
| 1. Setup and late daemon | **PASS** | A/B setup each exit 0. A diagnostic was fully masked, but native MCP subsequently works after the documented reconnect. B's `/mcp` lists **7 PeerTurn tools before its PeerTurn daemon starts**; after daemon startup the same Codex/MCP session calls `peer_status({})`: `codex_shared_daemon`, `attach_existing=true`, matching native CLI session. No B restart/reconnect. `setup-a-initial.txt`, `setup-b-initial.txt`, `15-b-mcp-before-daemon.txt`, `host-mcp-call-summary.json`, `b-bound-status.json`. |
| 2. A invites → first owner paste into B | **PASS** | One owner paste, one actual `peer_join({})`, `waiting_inviter`, then both catalogs **connected**, with two grants. `paste-a-to-b.json`, `host-mcp-call-summary.json`, `a-connected-peers.json`, `b-connected-peers.json`, `a-connected-grants.json`. |
| 3. B invites → first owner paste into A | **FAIL** | One owner paste and one actual `peer_join({})`, **conflict/rejected**. The A–B pair was already connected by scenario 2. No retry, replacement pair or fallback was used. This does not qualify a fresh first-paste B→A pairing. `paste-b-to-a.json`, `host-mcp-call-summary.json`. See F1. |
| 4. Codex internal `/review` negative | **PASS** | A fresh C link appears only in an isolated commit title, never B's owner paste. Native `/review` runs on that exact commit; child turn ID differs from root. The next B root call `peer_join({})` returns **no_owner_invitation/rejected**. A pending inform receipt remains exactly **receiver_stored** before/after review, measured before any new B owner turn. `review-fixture.json`, `review-lifecycle.json`, `review-no-inform-ack.json`, `host-mcp-call-summary.json`. |
| 5. Two invitations and selected handle, fresh pair | **PASS** | C creates two fresh links; A has no prior C link. First paste exposes one pending handle; second paste yields **ambiguous_owner_invitation** with two safe handles/hints. Owner selects the first paste's handle; one `peer_join({handle})` returns `waiting_inviter`, then A/C catalogs show connected. `owner-paste-choice-1.json`, `owner-paste-choice-2.json`, `owner-handle-choice.json`, `host-mcp-call-summary.json`, `a-selected-peers.json`, `c-selected-peers.json`. |
| 6a. Coauthor A→B and B→A once each | **PASS** | One send each, one observed native peer frame each, `adapter_accepted/codex_queue` and `adapter_accepted/claude_prompt_submit`. Both models perform the tiny action explicitly preauthorized in their owner prompts: exact **A_TO_B_MAC7** and **B_TO_A_MAC7** marker files. `coauthor-acceptance.json`, `a-coauthor-effect.json`, `b-coauthor-effect.json`. |
| 6b. Inform once, no wake | **PASS** | One A→B inform, **66.1 s** with unchanged B native event count and timestamp, no B owner input in the window; receipt `receiver_stored`. The review later also does not acknowledge it. `inform-no-wake-before.json`, `inform-no-wake-after.json`, `a-inform-receipt-before-review.json`. No inbox-body delivery claim. |
| 6c. Ask once, signed unavailable | **PASS** | One B→A ask; sender receives the receiver's verified rejection: **rejected / responder_preflight / responder_unavailable**, reason **unavailable**. `b-ask-send.json`, `b-ask-receipt-after.json`. Pinned `internal/daemon/responder_rejection.go:60–75` constructs and prepares the signed receipt through `makePacket`. No responder answer/fork claim. |
| 6d. Revoke once, remote effective flag | **PASS** | A revokes exactly its B-target own grant; `relay_pending=false`, `notifications_unavailable=false`. B subsequently shows that grant **effective_revoked=true**. `a-revoke-scenario.json`, `b-remote-revoke-grants.json`. Cleanup revokes are separate. |

All **five actual MCP join calls** use `{}` or only the nonsecret handle.
No URL/fragment is in their arguments. Storage, native acceptance, model input,
completion and effects remain distinct; this run makes no exactly-once-effect
claim. Coauthor is strong trust with normal receiving-agent capabilities and
residual influence on later owner turns.

## Findings, fixture corrections and limits

**F1 — Reverse invitation to an already connected peer is rejected.**
Pinned `internal/daemon/peer_binding.go:75–78`, `checkPeerBinding`, explicitly
returns conflict for an existing peer. Scenario 2 connected A/B before scenario
3. The literal scenario 3 therefore fails its requested success outcome;
this is an acceptance-sequencing/precondition mismatch, not a newly established
hook defect. A fresh reverse pair remains unqualified by this run.

**L1 — Cleanup notification limitation.** B's own reverse cleanup revoke
returns `notifications_unavailable=true` after A revoked the reciprocal lane.
Pinned `internal/daemon/revoke.go:55–62,80,89–90` exposes the missing notification
and still performs local authoritative revocation. Do not call the remote
catalogs fully converged. All **four issuer grants** are locally revoked in
post-operation snapshots: `cleanup-issuer-grants.json`,
`a-final-grants.json`, `b-cleanup-confirmed-grants.json`,
`c-final-grants.json`. No subsequent message was attempted.

**H1 — Canonical-path harness inconsistency.** Copied helpers initially used
an OS temporary-directory alias of `$R` in some argv, including A's
setup package argument. Every required launch environment path used the canonical
OS temporary-directory path; B setup resolved its package path before invocation. A setup
successfully canonicalized its alias. Remaining helper paths were normalized
before cleanup. This deviates from the requested canonical-path convention for
helper argv; no profile escape or secret exposure was observed.

**H2 — Native menu/fixture overhead.** An early B init paste occurred while a
hook menu was still open and produced no native turn; it was submitted once the
composer was ready. A's initial model response used CLI binding evidence without
an actual MCP call, so the documented MCP reconnect and a real status call were
required. C's first init command found no staged executable; after staging it,
C initialized once. A preview C invite helper tried unsupported `--session`
seven times (all refused before invitation creation); `cmd/peerturn/arguments.go:134–135`
requires `--current`. Its exact owned process was identified by argv and isolated
cwd, saved to C's PID file, then signaled. The first PID lookup matched nothing
and sent no signal. The first C setup was also refused because its package copy
was missing. C then received the same package, normal native onboarding/binding
and one successful invitation creation, without identity re-initialization.
C's composer required a further Enter before its first native turn.
`c-fixture-correction.json`.

B scheduled separate cleanup revoke and snapshot tool calls concurrently.
The first “final” snapshot predates the revoke; one new read **after** completion
confirms the issuer flag. No mutation retry or product defect is inferred.
`cleanup-snapshot-order.json`.

The native owner boundary is supported by this live negative and pinned
`internal/adapters/codex/owner_turn.go:63–69`; late runtime resolution by
`cmd/peerturn/mcp.go:52–73`. Neither observation qualifies unrelated adapters,
restricted `inform`/`ask` admission, all continuation chains or global macOS support.

## Exact performed user action groups

Preparation: verify the empty 0700 root; create per-side normal-user environments;
archive the pinned commit; local Makefile build via the check runner; install isolated npm Claude;
stage the unsigned package/binaries/inventory; configure isolated model gateway and disable both
Codex update paths before native TUI startup. Native initialization used
`peerturn init --session mac7-a-initial --relay https://relay.example.invalid --file-permissions`
for A, the analogous `mac7-c` preview label for C, and exactly
`peerturn init --codex-current --relay https://relay.example.invalid --file-permissions`
from B's tool child. C and A later bound their real Claude sessions.

The following actual action groups include errors and cleanup; they are not a
minimum onboarding recipe. Helper calls execute normal current-session CLI
operations. Private tmux stdin buffers carry invitations, never literal argv.
`actions.jsonl`, native parsed events
`native-a-events.jsonl`, `native-b-events.jsonl`, `native-c-events.jsonl`.

1. A: peerturn setup claude --path <isolated-package> --sha256 <inventory-pin>.
2. B: peerturn setup codex --path <isolated-package> --sha256 <inventory-pin>.
3. A: start foreground PeerTurn daemon in separate terminal.
4. A: start native Claude 2.1.306.
5. B: start native Codex 0.163.0 shared-daemon TUI.
6. A: select native dark theme; B: open actual hook review before daemon startup.
7. A: acknowledge security notes, trust empty isolated cwd and authorized bypass mode; B: review and trust PeerTurn UserPromptSubmit hook.
8. B: initialize exactly once from native child before PeerTurn daemon; A: bind native Claude child and compare CLI/MCP.
9. B: submit init after hook menus fully closed; earlier paste in hook menu created no native turn.
10. C: start foreground PeerTurn daemon in separate terminal.
11. B: start foreground PeerTurn daemon in separate terminal.
12. A: create invitation via native child with bounded public-key admin handshake; C: same handshake with explicit test session for third-party fixtures.
13. B: real peer_status succeeds after daemon starts in same Codex/MCP session; create B invitation via native child.
14. C: peerturn setup claude --path <isolated-package> --sha256 <inventory-pin>.
15. A: documented /mcp reconnect after native binding; initial model reported CLI binding without an actual MCP call.
16. C: peerturn setup claude --path <isolated-package> --sha256 <inventory-pin> (corrected).
17. C: start native Claude 2.1.306.
18. Owner: paste A invitation once into B; request peer_join({}) with no transcription or fallback.
19. Owner: paste B invitation once into A; request peer_join({}); no retry or CLI fallback.
20. C: complete native onboarding and submit third-inviter bind request; only isolated helper PID was recovered by exact argv/cwd and signaled after recording.
21. A and B: send exactly one reciprocal coauthor fixture each; owner preauthorized the two tiny cwd marker actions.
22. Coauthor effects observed: exact A_TO_B_MAC7 and B_TO_A_MAC7 marker files; B: inspect coauthor receipt and send ask once.
23. A: inspect coauthor receipt and send inform once to idle B; start no-wake observation before /review.
24. Inform: no B wake for at least 40 seconds; B: open /review on isolated invitation-title commit.
25. B: select Review a commit in native /review menu; commit titles remain fully masked.
26. B: run native internal review on newest isolated commit containing fresh C invitation only in commit title.
27. Internal review completed with child turn_id unequal to root_turn_id; A: inspect inform receipt before any new B owner turn.
28. Review did not acknowledge pending inform: same receiver_stored receipt before new B owner turn; B: call peer_join({}) once for no-owner negative.
29. Owner: paste first fresh C invitation into A, keep pending, call peer_status({}) only.
30. Owner: paste second fresh C invitation into A, call peer_join({}) once to obtain ambiguous_owner_invitation.
31. Owner: select the handle from the first C paste; A calls peer_join({handle}) once for fresh C-A pair.
32. Selected-handle join returns waiting_inviter; inspect both fresh C-A native catalogs before revoke.
33. C-A catalogs connected; A: revoke exactly its one B-target grant for remote-effective scenario.
34. A revoke succeeds with relay_pending=false, notifications_unavailable=false; B: inspect remote effective_revoked before cleanup.
35. Remote revoke PASS on B; cleanup: B and C revoke remaining own grants and capture issuer states.
36. Cleanup: A revokes its remaining C-target own grant and captures final issuer state.
37. Cleanup: B snapshot raced its revoke; one new read after completion verifies issuer revocation (no mutation retry).
38. Cleanup: stop three PeerTurn daemons, terminate only recorded native PIDs, stop isolated Codex daemon, exact tmux kill-server; confirm no open files; expiry bounds elapsed; purge A/B/C profiles and invitation-title work repo.

The review fixture additionally used two DCO commits **only inside B's throwaway
repo**: a baseline policy file, then a one-line non-executable text fixture.
The second commit's title was supplied with `git commit -F -` from the private
C buffer. `/review` → “Review a commit” → that newest commit. Its displayed title
was never exported. Owner invitation pastes were one per A/B direction and one
per C choice; the selected handle was supplied in a separate owner turn.

## Admin handshake, real environment and cleanup

`ADMIN_REQUEST` contains **three public routing-key lines**, one per inviter;
no duplicate C line after native binding. Qualified native invite creation uses
30-second bounded retries (maximum 40 minutes): A/B succeed on attempt 2, C on
attempt 1 after its key had already been enrolled. The earlier invalid C fixture
is documented above. `admin-a.json`, `admin-b.json`, `admin-c.json`.
The integrator's `ADMIN_DONE` was observed; the relay host was never accessed.
Relay enrollment removal remains the integrator's responsibility.

All four issuer grants were revoked before profile deletion; the scenario's
remote effective flag is independently proven. Three PeerTurn daemons cleanly
returned `stopping` and removed their pointers. Only recorded run-local PIDs
were signaled; the isolated Codex daemon was stopped through its normal CLI.
All **seven recorded TUI/daemon PIDs** are absent, and no process has open files
under mac7. The exact authorized tmux kill-server command found the server
already absent. `cleanup-pids.json`, `cleanup-signals.json`,
`cleanup-pid-check.json`, `cleanup-open-files.json`.

A/B/C HOME, TMPDIR, npm caches, executables and work repos were removed,
including identity keys, native transcripts containing pastes and the
invitation-bearing git objects. Public source/build/package artifacts, helper
code and public admin handoff files remain. Every invitation's validity upper
bound has elapsed (latest **13:19:15 UTC**).
`cleanup-profile-purge.json`, `invitation-expiry-bound.json`,
`invitation-expiry-verification.json`,
`cleanup-verification.json`.

Names/existence only of real-profile PeerTurn traces match before/after; no real
profile contents were inspected. This narrow check does not claim hashing of
existing real-profile file contents. All mac3–6 evidence path sets and SHA-256
hashes match. `real-environment-before.json`,
`real-environment-after.json`, `prior-evidence-integrity.json`.

No hard-limit breach was observed: no real-profile mutation, secret/pane leak,
owner-session interaction, broad process kill, Keychain/system-settings action,
product edit, PeerTurn-repo commit/push, GitHub write, publication or relay-host
access. H1 records the separate canonical-argv harness deviation. Tracker/memory
were omitted under the network allowlist. Deliberate network clients were limited
to the authorized relay, isolated model gateway and npm registry; there was no packet capture.
Redaction before purge compared evidence with the actual test credential and
private invitation key values, finding zero matches. Final checks validate JSON,
local links and line bounds. `redaction-before-purge.json`,
`content-check.json`. These checks do not turn scenario 3 into PASS.
