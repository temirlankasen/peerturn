# Automatic runtime qualification

`runtime-watch.yml` runs at `17 */6 * * *` and accepts manual dispatch on main.
It reads npm Claude Code metadata and paginated stable `rust-vX.Y.Z` Codex
releases, ignores versions below the approved minima, and compares packaged
`internal/runtimepolicy/verified.json`. Intermediate releases are considered,
as well as the latest one. Each sweep dispatches at most eight unseen candidates.

The watcher creates one bot-owned issue per runtime/version before dispatching.
Its policy/artifact claim prevents duplicate paid runs on repeated sweeps.
Changed upstream identity on an unverified candidate permits a new claim.
Already verified versions retain their packaged evidence; there is no background
revalidation of all published bytes or remote revocation of installed clients.
An ambiguous write/dispatch response stops automation. An operator checks the
run list and dispatches manually; paid/signing effects are never blindly retried.

## Repository configuration

The integrator creates these inputs; the implementation changes no settings.

| Input | Purpose |
| --- | --- |
| Environment `runtime-qualification` | Settings → Environments → New environment. Deployment branches and tags: **Selected branches and tags**, exactly one **Branch** rule `main`, no tag rules or wildcards. Required reviewers: none; wait timer: 0; disable administrator bypass. The live job declares this Environment; keep its main/non-fork/event guards too. |
| Environment secret `PEERTURN_QUAL_LITELLM_KEY` | Dedicated qualification-only LiteLLM virtual key, with all server-side controls below. Remove every repository/organization secret copy accessible to this repository after migration. Never reuse a user, master or production key. |
| Environment secret `PEERTURN_QUAL_LITELLM_BASE` | HTTPS LiteLLM base URL, optionally ending in `/v1`, without credentials/query/fragment. Expose Anthropic messages and OpenAI Responses. Delete the old repository variable; secrets protect the public step header, and the runner additionally masks both values before use. |
| Repository variables `PEERTURN_QUAL_CLAUDE_MODEL`, `PEERTURN_QUAL_CODEX_MODEL` | Optional aliases allowed by the key; the runner declares its defaults. No repository secrets or PAT are needed; `GITHUB_TOKEN` is job-scoped and automatic. |
| Actions workflow permissions | Default **Read repository contents and packages**; enable **Allow GitHub Actions to create and approve pull requests** only here. Keep job-level permissions; only reconcile has `pull-requests: write`. |
| Auto-merge and main ruleset | Enable squash and auto-merge. Require a PR, up-to-date branches and checks `Go / ubuntu-latest`, `Go / macos-latest`, `lint`, `vulnerabilities`, `configuration`, sourced from GitHub Actions. No bypass actors, force-push or deletion. Require 0 approvals for owner-only write access, or 1 code-owner approval with manual merge waiting. |
| Tags/releases | Ruleset for `v*`: forbid updates/deletion, permit creation; enable Immutable releases. Artifact signing uses keyless cosign. |
| External PRs/actions | Require approval for all outside collaborators; restrict allowed actions and require full commit SHA pins. |

No LiteLLM credential enters PR jobs. Qualification requires a non-fork repository,
`refs/heads/main` and workflow_dispatch context (the watcher also has a schedule).
Inputs accept only `claude` or `codex` and canonical stable versions above minima.
There is no reusable workflow entry point. GitHub enforces the Environment branch
policy before releasing its secrets; YAML `if:` conditions are additional checks.
See GitHub's [Environment branch rules](https://docs.github.com/en/actions/reference/workflows-and-actions/deployments-and-environments).

Before enabling the schedule, configure and verify on the LiteLLM server:

- Small hard `max_budget` with explicit `budget_duration`, plus a spend alert
  below the cap with a monitored destination. Budget resets must remain bounded.
- Per-key `rpm_limit` and `tpm_limit`, and an exact model allowlist containing
  only the selected qualification aliases.
- A short expiry (`duration`/expires), an owner for renewal, and a rotation plan.
  Revoke and rotate immediately after suspected exposure or suspicious spend.

Record the chosen amounts, period, rates, expiry and alert recipient privately
with the integrator. Confirm enforcement with the LiteLLM administrator before
any paid run; client request/time caps cannot enforce a monetary budget.

## Execution and evidence

First, `make runtime-contract-check runtime-automation-check` runs policy,
adapter, MCP and Claude module fixtures without provider access. These test
PeerTurn's checks; they do not prove a new host's semantics. Disposable
`ubuntu-latest`/`macos-latest` jobs install the candidate and opposite approved
baseline. Claude npm SRI and Codex release asset SHA-256 are checked before
execution; a manifest pins the actual binary hash. Missing/mismatched integrity
fails. The detected candidate identity travels in the dispatch; one model-free
resolution pins both runtime sources before the OS matrix. Both jobs reject
metadata/artifacts that differ from that same profile, including the baseline.
For Claude, contracts resolves a bounded npm package-lock once without lifecycle
scripts. Its exact bytes and canonical dependency tree hashes travel as a trusted
job output to both OS jobs, which use `npm ci --ignore-scripts`. Every locked
dependency has integrity; receipts must agree on lock/tree hashes across OSes.
The manifest also records the actual installed tree (optional platform packages
may differ). Entry-point SHA-256 alone is not the Claude identity.
Contracts also uploads `qualification-npm-lock.json` for 30 days: exact lock
bytes, a readable resolved tree and integrity hashes. Promotion/release never
consume this audit artifact; the shared live input is the trusted job output.
Codex generated schemas are checked before introducing the provider key.

The runner lives in `research/lab/qualify/`: it produces lab assertions, has no
product import, and depends on no private historical scripts. It creates fresh
HOME/config/state and disposable native host profiles with normal hook trust.
A TLS relay runs only on loopback with a disposable certificate. An in-process
loopback provider gateway omits the real key from child environment inheritance,
limits requests, and latches spend/quota errors across the run. **The tested host
binaries and model shell commands can still read the key and base URL** from the
parent process/environment or memory: Claude uses `bypassPermissions`, Codex
uses `danger-full-access`, and hosted runners have sudo. This is no secret
isolation boundary. A malicious candidate can bypass the local gateway and spend
or exfiltrate the key directly. Its blast radius includes the disposable runner,
its job token, and all models/budget reachable with the key until revocation or
expiry. Only the server-side controls above bound provider access/spend.
GitHub masks the secret values and the runner masks them again before use;
raw provider errors/native transcripts are never printed or uploaded. Request
and time caps supplement the server's hard spend limit, not dollar accounting.

Scenarios cover setup, real native binding, late-daemon MCP, fresh owner-link
joins each direction through actual hooks, selected-link ambiguity, peer-content
invitation refusal in both hosts, Codex internal review, coauthor receipt/input/
action correlation each way, no-wake inform, fixed notices, explicit body read,
revoke and teardown. Unsupported UI or missing proof fails. A model answer or
process exit alone cannot pass. Inform means the existing notice lane, not
restricted peer-turn admission. Ask remains independently closed/qualified.

Live jobs upload only `qualification-summary.json` with fixed assertion names and artifact
identities is uploaded, for 30 days. The run URL enters packaged evidence.
Runner labels are rolling images; recorded OS/architecture do not extend the
historical Linux ARM64/macOS 27 evidence universally. Broader exploratory design
scenarios remain a separate acceptance scope. No successful live qualification
is claimed by offline tests.

## Promotion and release

The model-free job and both live jobs must pass. Promotion rechecks GitHub job
results and requires **72 hours since upstream publication** before changing
the verified list, README/CHANGELOG or reserving a release. Claude uses npm
`time[version]`; Codex uses the exact release's `published_at`. Missing/invalid
timestamps stop promotion; younger/future timestamps defer it. Qualification may
run sooner, and a later sweep can promote the retained green result. The release
reconciler checks the age again before tags/dispatch. Users may already run newer
unverified versions: this hold delays only the verified label/announcement and
its PeerTurn patch. It does not change runtime admission or prove host safety.
Promotion compares qualified source with current main. Code changes require
fresh qualification. Only verification bookkeeping, announcements and exact
release-pin substitutions may differ. One atomic DCO-signed tree/commit attempts
a fast-forward main update, never a force-push. Protected main falls back to one
bot PR at a time, explicitly dispatches CI at its head and enables auto-merge.
An existing branch must match the expected tree, main parent and automation
author/committer. An existing PR must match the Actions bot/app, same-repository
base/head, branch and exact SHA before CI dispatch or auto-merge. App provenance
comes from the PR's issue view. A mismatch recreates the change on a branch with
the expected commit SHA suffix and a new bot PR, without deleting/updating the
occupied branch. The replacement gets the same checks; a second mismatch stops.
Only runtime-watch mutates main: a green qualifier explicitly dispatches it.
This keeps simultaneous candidates from losing paid results to a canceled
pending mutation job. Ordinary bot push/PR events do not start required checks.
The next sweep recovers deferred promotions and releases merged PRs; manual
watch dispatch accelerates this.

Reconciliation reads at most three pages of 100 dedicated runs created in the
last seven days; recorded and duplicate runtime/version runs are filtered before
jobs queries. Each sweep inspects at most 16 unrecorded version matrices. Older
results/backlogs require explicit recovery with `qualification_run`,
`runtime` and `version` inputs on runtime-watch; the dedicated workflow/title,
repository, main ref, matrix and source are still independently checked. Issue
lookup filters `creator=github-actions[bot]` server-side, paginates at most ten
pages, and matches the exact title and bot author locally, excluding PRs. At the
bot-only capacity bound, stop for operator recovery rather than risk duplicate
paid dispatch. Unrelated public issue spam does not consume these pages. CI and
release run queries filter the exact head SHA (and branch/tag).

Every setup-go in qualification, release, CI and CodeQL has `cache: false`.
Release validate/build both clean module/build caches, download modules afresh
and run `go mod verify`. Release never downloads qualification artifacts or
restores Actions caches. Promotion parses only bounded sanitized JSON receipts
as data; no qualification file is executed, imported as code or used to build
the signed release. Clearing old Actions caches is optional housekeeping, not
a substitute for disabling restoration. A hostile candidate can still use its
runner runtime token; write-job isolation does not make native execution safe.

Each promotion reserves the next unused `v0.1.N-preview` patch above existing
and reserved tags and the `0.1.1-preview` floor. The first automatic patch is at
least `v0.1.2-preview`. The same commit refreshes installer/skill/documentation
pins and installer regression fixtures while preserving older release history.
General verification never adds an ask profile. `.github/runtime-releases.json`
records the qualification run, source and tag for recovery after a PR merge.
Manual requalification refreshes existing version evidence after a patch;
historical baseline references are retained. Unpublished older receipts for that
same version are superseded with a new patch/tag; published records and existing
tags stay immutable. A stale run cannot replace newer green evidence.

The promoter explicitly dispatches `release.yml` **at the tag**: a token tag push
does not start another workflow. This preserves the installer's exact tag-based
OIDC identity; a reusable call from main would sign with the main ref. Release
retains validation, SBOM, provenance, keyless signing, draft-then-publish and
published-release immutability. A durable pre-dispatch claim prevents duplicate
signing after uncertain responses. Failed/ambiguous publication requires a human
rerun, not automatic resubmission. Ambiguous native submits are never retried.

## Reruns and failure handling

A failed/incomplete qualification updates the version issue with allowlisted
job/step conclusions and a run URL. No transcript, provider response, local path
or secret appears there. Infrastructure failures remain unverified; an issue
does not automatically classify a runtime as known bad.

After a patch/configuration fix, the authorized integrator can run:

```sh
gh workflow run runtime-qualify.yml --ref main -f runtime=claude -f version=2.1.308
# Or runtime=codex with its exact candidate version.
gh workflow run runtime-watch.yml --ref main
```

These perform GitHub writes and paid qualification. For failed signing, inspect
the draft/tag and rerun that exact GitHub run after fixing its cause. Never move
a tag or delete a durable claim to force a second attempt. Offline checks are
`make runtime-automation-check` and `make runtime-contract-check`.
They prove neither repository permissions nor live authentication/trust dialogs.
