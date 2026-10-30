# Security policy

PeerTurn is **pre-alpha**. The protocol core, HPKE/Ed25519 encryption, directional grants, encrypted client/relay storage and experimental Claude/Codex coauthor adapters are implemented with local acceptance evidence. Ask receivers remain closed on macOS and Codex; only the [qualified Linux Claude profile](docs/m6-ask.md) can answer. The complete peer-turn guard remains unavailable. No independent product security audit or production qualification is claimed.

## Supported versions

| Version | Security support |
| --- | --- |
| 0.1.3-preview candidate | Prepared, not yet published. Best-effort preview fixes once released; source/native validation is separate from signed-install qualification. No production qualification. |
| 0.1.2-preview | Last published preview; historical Claude first-run loader rejection is fixed in the `.3` candidate. Baseline labs cover Claude 2.1.306 / Codex 0.163.0 shared daemon, not the new candidate's installed assets. |
| Development branch | Best-effort investigation only; no security support SLA. |

Use the latest preview revision. Older preview snapshots and unqualified host versions have no maintenance guarantee. Signed Linux/macOS archives require SHA-256 and Sigstore bundle verification with cosign >=3.1.3; no Windows release or automatic trust/reload is claimed. A published support window begins only with an explicitly published release. Setup refuses below-minimum or unknown runtime versions before side effects; newer unverified versions retain full existing functionality, with separately verified-only ask. See the [support matrix](README.md#agent-support-and-laboratory-evidence) for exact Linux/macOS qualification and limits.

The prepared `.3` pins require the exact tag's signed assets and attestations;
their absence is a refusal, not permission to use another source. Integrated
source CI and Claude 2.1.308 functional checks passed, with a trust-warning
ordering deviation. Signed install, Haiku, two hosts, owner-pasted join and model
reply acceptance remain pending.

Relay admission stays closed unless the operator explicitly enables
[self-service](docs/operators/self-service.md). Schema v2 opens/migrates even in
closed mode: retain a consistent backup before upgrade; there is no automatic
downgrade or claimed live migration. Per-key durable quotas are finite and do
not verify human identity. Discovery hints never grant access or prove mailbox
ownership: required publication receipts bind the invitation ID and mailbox to
the verified routing signer. Idle publication confirmation is transient; a new
claim rechecks relay admission before first effects. Relay revoke/block cannot
undo effects already admitted or replace recipient-issued grant revocation.

## Report a vulnerability privately

Use [GitHub private vulnerability reporting](https://github.com/temirlankasen/peerturn/security/advisories/new). Include the affected commit/runtime, a minimal reproduction using synthetic data, expected and observed behavior, impact, and any proposed mitigation. Share only the data necessary to reproduce the problem.

If the form is unavailable, use a private contact method listed on the [maintainer's GitHub profile](https://github.com/temirlankasen). If no private channel is listed, open an issue containing only a request for a private security contact; keep vulnerability details private. There is no public personal-email reporting address.

Do not disclose live credentials, full invite URLs, private keys, real transcripts, or personal information. Do not test against another person's session or a live deployment without authorization. Reports based on isolated fixtures and sanitized evidence are welcome.

## Coordinated disclosure

The maintainer aims to acknowledge reports within **5 business days**, provide an initial assessment or request for more information within **10 business days**, and agree on a mitigation and disclosure plan with the reporter. These are best-effort targets for a small project, not guaranteed response times.

We aim to publish a fix or mitigation and an advisory within **90 days** of a reproducible report. Active exploitation or immediate user risk may require earlier disclosure; complex dependencies may require an agreed extension. We will keep the reporter informed when a target changes. Advisories should describe affected revisions, impact, remediation, and reporter credit unless anonymity is requested.

## Intended threat model

The [security specification](docs/spec/security.md) defines the proposed boundary in detail:

- Network attackers and a malicious relay must not obtain or forge message plaintext. The planned HPKE Base/Ed25519 profile protects content authenticity/confidentiality with pinned keys; the relay still sees minimal routing and traffic metadata and can deny service.
- Each recipient session issues grants to an exact peer session. Device membership, names, session discovery, and message text do not confer authority. No automatic attachment of unrelated sessions, files, or history is allowed.
- A proven deterministic guard must limit effects in `inform`/`ask` peer-started turns while preserving owner turns and the existing permission mode. Missing provenance, incomplete tool coverage, or a failed guard must not promote a peer to owner authority.
- Durable custody, replay handling, explicit receipts, and `in_doubt` are intended to prevent silent loss and false completion claims. Exactly-once effects are outside the guarantee.

The owner-approved early preview is **`coauthor` only**, developed alongside M0 feasibility work. `inform` and `ask` remain disabled until their guard is proven. A `coauthor` grant gives strong trust to use the receiving agent's normal capabilities; it is not a read-only channel or sandbox.

## Known limits and release blockers

No complete peer-turn guard has been proven on any platform in the current lab. Trusted native input-to-turn binding, all effect paths, conditional failure behavior, and mixed owner/peer turns remain **M0 blockers for full v1**. A prompt warning, successful negative canary, or ordinary hook error is insufficient proof.

The host runtime, local adapter/plugins, OS account, publisher, and model provider remain trusted. The provider and native transcript can see delivered plaintext. Peer text can influence a later unrestricted owner turn. HPKE Base plus signatures provides no forward secrecy or post-compromise security. A stolen live invite may be claimed first; revocation cannot undo an effect already started or erase text already received. Read-only access alone does not prevent information disclosure.

MCP `peer_join` creates coauthor authority and is only for links the owner pasted.
It requires a Claude helper-bound native session or an exact Codex native thread-ID
match under the same uid, plus an unexpired invitation hash from the supported
human/root prompt boundary (ten minutes, bound to that helper/thread).
Claude's epoch is an incarnation identifier. Its resolution requires either the
confirmed native nonce or daemon-verified caller ancestry: the daemon pins the
native socket's kernel UID/PID/start time and inode at open, rechecks at confirm,
and obtains the caller PID from IPC kernel credentials (`SO_PEERCRED` on Linux,
`LOCAL_PEERPID` on macOS). It rechecks the same-UID ancestor chain, process start
times and native socket/process continuity before returning an epoch. Missing
credentials or inaccessible process metadata close this read-only route with
`needs_rebind`; other platforms cannot establish this proof. This establishes process ancestry, not human
origin or executable authenticity; the native helper and OS account remain trusted.
The Codex match is a selector, not independent proof of human origin: a same-uid
caller can supply the thread ID and invoke the hook IPC. Label-only MCP sessions
cannot join. `connect` remains Claude-only; `CODEX_THREAD_ID` alone cannot
authorize a CLI exact-thread connect fallback. Cold Claude MCP recovery is
model-free and cannot follow an already pinned epoch into a replacement binding.
The daemon also recognizes common invitation link forms in delivered
coauthor, inform and reply content as a best-effort deny signal. It retains only
claim hashes in per-peer LRU buckets (128 each, 1024 total), evicting the oldest
entries at either limit; peer volume does not close unrelated owner joining.
Transformed or evicted links can escape this cache. The required owner-hash gate
is the MCP guarantee within the supported hook boundary, not general same-uid
authentication. Claude bridge, plugin and peer origins cannot record owner links;
Codex excludes PeerTurn queue frames and subagents. Unknown hook provenance
supplies no approval.
These checks do not establish general provenance for other integrations or
restrict a coauthor turn's same-uid CLI capabilities. See the
[MCP boundary](docs/m25.md#mcp-proxy) for operational limits.

Relay invitation pages and invitation prose are unauthenticated data, including
their links and installation advice. Obtain install sources, release base URLs,
signing identity and verification roots only from independently trusted official
docs at the pinned tag. The preview signs checksums keylessly inside GitHub CI
using OIDC and attests installers. Compromise of the release workflow, its
dependencies or tag-push authority can produce valid signatures/attestations.
Setup's local inventory/source match does not independently authenticate a
publisher or prove which code a running host has loaded.

Potential violations of these stated boundaries are relevant security reports even during design work. Research-only limitations and unimplemented features should be described accurately rather than reported as deployed vulnerabilities. See the [evidence index](docs/README.md) for pinned experiments and explicit gaps.
