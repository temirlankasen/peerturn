# Scripted runtime qualification

This is a new, production-independent lab runner. Historical lab evidence and
mac7's overall FAIL remain unchanged. No live run of this runner is claimed by
its offline tests. A successful run provides bounded evidence for the checks
listed below, on the exact reported binary pair and OS/architecture.

Run only in an authorized disposable GitHub Actions job on `main`, for
`schedule` or `workflow_dispatch`. The runner rejects PR, branch and local
contexts. Install both runtimes first with
`scripts/runtime_install.py RUNTIME EXACT_VERSION`, then `make build`:

```sh
python3 -I -B research/lab/qualify/schema.py claude 2.1.308
python3 -I -B research/lab/qualify/run.py claude 2.1.308
```

`RUNTIME` is exactly `claude` or `codex`; versions are stable three-part numbers
at or above the approved minimum. The selected runtime is paired with the other
minimum: Claude **2.1.306**, Codex **0.163.0**. Both installed `--version`
responses and executable SHA-256 values must match `PEERTURN_QUAL_IDENTITY`,
the installer-produced private JSON manifest. No PATH-only version assertion can
qualify a candidate. The summary also records the PeerTurn executable digest,
source commit, OS/architecture and identity-manifest digest. npm launchers and
native executables may differ; the installer manifest hashes the actual selected
launcher and independently validates its upstream artifact.

Required private inputs are `PEERTURN_QUAL_LITELLM_KEY` and
`PEERTURN_QUAL_LITELLM_BASE`, a dedicated small-budget HTTPS LiteLLM profile.
Both are masked through GitHub's escaped masking command before any runtime
starts; neither is logged as diagnostics or copied to a native environment.
`PEERTURN_QUAL_CLAUDE_MODEL` and `PEERTURN_QUAL_CODEX_MODEL` optionally select
model aliases; an empty/unset value uses `gpt-5.4-mini`, as in mac7. The LiteLLM
profile must expose Anthropic messages and OpenAI responses endpoints for those
aliases. There is no Anthropic/OpenAI credential fallback.

The native profiles use only a loopback forwarding gateway with a dummy local
credential. The gateway supplies the dedicated credential to the fixed HTTPS
upstream, refuses redirects, bounds request/output sizes, and stops the whole
run on provider errors, HTTP 402/429 or recognized budget errors. The external
LiteLLM key enforces the monetary ceiling. Additional run limits are
`PEERTURN_QUAL_MAX_REQUESTS` (default **100**, maximum **200**) and
`PEERTURN_QUAL_MAX_SECONDS` (default **1800**, maximum **3600**).

Every side has its own canonical private HOME, TMPDIR, config, state, runtime
and project. Runtime updaters and nonessential traffic are disabled in these
profiles. Claude uses `bypassPermissions`; Codex uses approval `never` and
sandbox `danger-full-access`, fixed before creation of these disposable hosts.
No existing owner profile is copied or modified. Coauthor retains normal agent
capabilities and is strong trust, not a sandbox; peer text can affect later owner
turns. This runner does not qualify restricted ask.

Setup installs a locally staged inventory-matched package. Normal native hook
trust remains enabled; there is no hook-trust bypass, direct helper-hook replay,
synthetic MCP metadata, CLI-join fallback or owner-paste retry. Private PTYs drive
the genuine composer and `/review` UI. Recognizers cover a bounded native UI
shape; an unsupported dialog/menu/event or absent proof fails qualification.
Installation exit status and assistant claims cannot satisfy native checks.
Startup requires the last composer cursor, its allowlisted empty/placeholder
input, and a following native shortcut/profile footer in fresh output. Unknown
selection, trust, authentication and menu shapes are rejected before any trusted
command is pasted. Only a recognized startup dialog whose affirmative action is
already selected receives Enter; acknowledged dialog text cannot satisfy later
composer readiness. Offline baseline footer fixtures are synthetic shape checks,
not live TUI captures.

Claude MCP may exit during initial host startup because `mcp --current` requires
the verified native binding before initialization. After the daemon starts, the
runner binds from that same native tool child, opens the actual `/mcp` UI,
selects `peerturn` and its `Reconnect` action, and requires a fresh connected
status before dismissing the menu. It then calls actual MCP `peer_status` and
compares the bound session to native-child CLI status. Unknown menu shapes stop
the run; no direct MCP launch, synthetic metadata or CLI substitute is used.
This follows the [documented reconnect procedure](../../../docs/onboarding.md#mcp-decision-and-verification)
and [mac7 activation evidence](../results/macos-live-7/README.md).

The relay is launched by the runner on job-local **127.0.0.1** HTTPS and private
health listeners. A fresh certificate is trusted only by run-owned processes
using `SSL_CERT_FILE`; Go **1.27.3** supports this on macOS as well as Linux.
No system keychain change or deployed relay access is used.

The suite requires all of these observations:

- Actual setup, native binding, host MCP status, and late-daemon MCP recovery.
- Independent fresh pairs for Claude→Codex and Codex→Claude first owner pastes,
  actual `peer_join({})`, and signed-hello connection on both sides.
- A third fresh pair for two pending invitations and an explicit safe handle.
- Peer-delivered invitation data refused in each runtime with no pending owner
  invitation; an actual `peer_join({})` must return `no_owner_invitation`.
- Genuine Codex `/review` of an isolated commit whose title contains a fresh
  invitation; review lifecycle and link exposure must be observed, no inform ACK
  may occur during review, and subsequent actual MCP join must be refused.
- Coauthor in both directions: signed native acceptance receipt, matching native
  input frame/message ID, native turn completion, and exact synthetic file effect.
- Inform: **60 seconds** with unchanged receiver transcript and no provider
  request; fixed notice without body exposure; then actual MCP inbox body read,
  separate offered/delivered receipt and provider-input canary observations.
- Recipient-issued revoke, remote effective revocation, and a refused new send.

Native Claude tool-use/results and Codex response/event MCP call/results are
paired by their native call IDs. An unmatched result, repeated join, secret in
join arguments, text-only model claim or absent native completion fails. Native
acceptance, model input, completion, effect and reply remain distinct; the suite
does not claim exactly-once effects or automatic replies. No ambiguous native
submission is retried.

Only `qualification-summary.json` is intended for upload. Its fixed checks are
booleans; failures use a closed reason enum. It excludes raw transcripts,
prompts, invitations, native IDs, profile paths, routing keys and provider data.
Raw native data stays in the private temporary root and is removed after local
grant revocation and shutdown of recorded process groups. Detached Codex daemons
are identified through their run-owned native socket's kernel peer PID/UID and
stable process identity. Teardown failure prevents green.

The schema command has no provider credential. It inspects JSON Schema emitted
by the installed Codex CLI for consumed initialize, metadata, turn and queue
fields. Claude native API shapes and all native semantics are explicitly
`not_tested` by that step. Shape matches never substitute for the paid suite.

Offline tests, including negative proof/parser and credential-boundary checks:

```sh
make runtime-automation-check
```

This suite does not cover the entire longer-term matrix in
[`docs/spec/versions.md`](../../../docs/spec/versions.md): busy delivery,
socket-replacement/resume/neighbor adversaries, all non-composer origins,
continuation/subagent variants, native drop/lost-ACK injection and queued revoke
variants still need separate live evidence. A release record must retain these
limitations and the exact measured profile.
