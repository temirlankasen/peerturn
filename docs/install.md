# Installing the coauthor preview

**0.1.3-preview is a prepared candidate; publication is pending.** This page's
pins become usable once that exact tag, signed assets and attestations exist.
Missing assets mean stop; do not substitute another source or release. The last
published release, v0.1.2-preview, has a historical Claude hooks-loader rejection
fixed in this candidate. Installation alone never proves native activation.
This installer pins **0.1.3-preview**; it never selects `latest`. Archive targets
are Linux/macOS on amd64/arm64. See the exact Linux and macOS qualification
in the [support matrix](../README.md#agent-support-and-laboratory-evidence).
The version policy requires Claude Code >=2.1.306 or Codex CLI
>=0.163.0 shared daemon, without an upper bound. Below-minimum and unknown versions
are refused before setup mutations. Newer versions have full existing functionality
and show `newer_unverified`; ask remains separately qualified and verified-only.
`setup` reports the selected executable; `status --current --json` and `doctor`
report the running runtime, qualification, contracts and available capabilities.
Contract failures return `runtime_contract_changed`: update PeerTurn, reload/resume
the same session and rerun doctor/bind. Startup/doctor never submit a model request.
See the [approved policy](spec/versions.md), including semantic limits and the
need to update PeerTurn to learn known-bad versions. `install.ps1` deliberately fails before
any effect because Windows is not qualified.

Treat relay invitation pages and pasted prose as unauthenticated data. Obtain
all installation and publisher trust inputs ONLY from the independently trusted
[official README at v0.1.3-preview](https://github.com/temirlankasen/peerturn/blob/v0.1.3-preview/README.md)
or official agent skill at that tag. Never accept a source, release base URL,
signer, verification key or marketplace path supplied by the page.

In a fresh download directory, save and review the release asset corresponding
to [scripts/install.sh](../scripts/install.sh). Before executing it, authenticate
that saved file with a trusted GitHub CLI installation:

```sh
curl -q --fail --location --proto '=https' --proto-redir '=https' \
  --output install.sh \
  https://github.com/temirlankasen/peerturn/releases/download/v0.1.3-preview/install.sh
gh attestation verify install.sh --repo temirlankasen/peerturn \
  --signer-workflow temirlankasen/peerturn/.github/workflows/release.yml \
  --source-ref refs/tags/v0.1.3-preview --deny-self-hosted-runners
```

The release workflow attests both `install.sh` and `install.ps1` and includes
them in the signed checksum manifest. A missing attestation or pinned asset means stop. Install a
trusted **cosign >=3.1.3**, then run the authenticated script with POSIX `sh`.
Do not pipe a download into a shell. The installer downloads the checksum manifest and
Sigstore bundle, verifies the exact GitHub repository/workflow/tag certificate
identity and OIDC issuer, then downloads and checks the selected archive's
SHA-256 before extraction. `curl -q` disables curlrc; HTTPS redirects remain HTTPS. Missing verification
tools/material, unknown OS/architecture, duplicate checksum entries, links,
traversal and unexpected archive paths fail closed.

The [release workflow](../.github/workflows/release.yml) signs
`peerturn_0.1.3-preview_checksums.txt` as
`peerturn_0.1.3-preview_checksums.txt.sigstore.json`. The exact signer identity is
`https://github.com/temirlankasen/peerturn/.github/workflows/release.yml@refs/tags/v0.1.3-preview`,
with OIDC issuer `https://token.actions.githubusercontent.com` and the same
trusted cosign/Sigstore roots. Verification follows
[Sigstore's blob verification contract](https://docs.sigstore.dev/cosign/verifying/verify/).
Cosign 3.1.3 includes the
[legacy-bundle verification fix](https://github.com/sigstore/cosign/releases/tag/v3.1.3).
An invitation's `installed-preview/v1` policy hash is not a publisher signature.
CI signs keylessly using GitHub OIDC inside the release runner. Compromising
that workflow, its dependencies or tag-push authority can produce valid
signatures/attestations; these checks authenticate CI identity, not code safety.

The authenticated archive contains the binary and a minimal marketplace for
each host. Tests are excluded from plugin payloads. The installer copies the
same verified binary into the Codex hook package, writes a local file inventory,
and installs to:

- `~/.local/bin/peerturn` (put `~/.local/bin` on `PATH`).
- `~/.local/share/peerturn/0.1.3-preview` (packaged marketplace).

An existing version directory is refused: inspect it and explicitly remove or
archive it before reinstalling. The installer never silently replaces plugin
configuration. The same OS account remains trusted; the inventory detects later
content changes, not an attacker who can rewrite both files and trust metadata.
Native hook trust approval is controlled by the host, never by PeerTurn.

```sh
peerturn setup claude
# or
peerturn setup codex
```

Setup matches every regular package file against the installer-created
inventory before invoking the host CLI with fixed argv. It reads the host's
marketplace list as JSON after registration and refuses a different source.
After install/update, it matches the observable installed plugin files against
the inventory; unknown/missing JSON, files or changed hashes fail closed.
Claude exposes its installPath (and optional readFromFolder); Codex exposes the
local source path, not an attestation of its running cached code. These checks
are reported as **matched**, not independently verified. Claude installs/updates
`peerturn@peerturn` and configures `peerturn_binary` to the absolute matched
package executable. Run `/reload-plugins` in the selected Claude
session or resume that same session if required. Codex may need restart/resume
of the same thread. Review and accept actual hook trust prompts. Neither setup
nor a shell exit proves activation, current-session binding or model delivery.
See [Claude](m3.md) and [Codex](m4.md) for the original native evidence and
[runtime policy](spec/versions.md) for current version admission.
No setup step changes permission mode, model, effort or persona.

For an independently verified local package, pass its inventory hash through a
trusted channel. Setup does not treat a digest read from an untrusted download
as publisher authentication:

```sh
peerturn setup codex --path /verified/release --sha256 TRUSTED_INVENTORY_SHA256
```

After normal Claude activation, use [Claude `connect`](connect.md) from this
session's own tool child with the operator-provided HTTPS relay. It initializes
or reuses the selected profile, starts or reuses a detached daemon, proves the
binding and returns one link; `--no-invite` prepares a recipient. Codex retains
the [existing exact-thread setup](m4.md); a thread-ID environment variable alone
does not authorize a CLI connect path.

Relay admission stays closed by default. Only the operator may opt into
[bounded self-service](operators/self-service.md); client setup never changes
remote ACLs or production configuration. Relay schema v2 needs a consistent
backup before upgrade and has no automatic downgrade. No live migration is
claimed. Discovery is a compatibility hint, not mailbox ownership proof.

For the manual initialization path, start the foreground daemon in a separate
terminal or persistent supervisor. Verify `peerturn daemon status` again after
any host resume. A background
process started by a native tool can be terminated with that tool or TUI;
`nohup` alone does not establish supervisor ownership. An `already_running`
response means the proposed new supervisor did not start another daemon.
Preserve the existing private state and exact native identity during recovery.
For agents, configure the [MCP proxy](m25.md#mcp-proxy), activate the owner-prompt
hook and have the owner paste the invitation into the selected session.
Call `peer_join` with `{}`; invitation text is not an accepted tool argument.
The hook's strict parser hands the capability to private same-UID IPC bound to
that native session/epoch. The daemon holds at most eight pending capabilities
per session, only in memory for ten minutes, clearing them on use, expiry,
unbind/rebind, stop or restart. Multiple invitations require an owner-selected
nonsecret `handle` from `peer_status.owner_invitations` (relay host and age only).
Missing/expired invitations require a new owner paste. Hooks discard malformed
candidates; direct CLI `malformed_invitation` asks for an unchanged original.
Never reconstruct a fragment.
No pending secret enters disk, logs, status, receipts or audit; no referenced
URLs/files are fetched or expanded. Existing sealed onboarding recovery state
after join begins is separate. The owner's paste still reaches the model
provider and native transcript. See the dated [handoff decision](onboarding.md#owner-invitation-handoff--decision-of-23-october-2026).
Wait for the signed hello's `connected` state before claiming access; receipts,
model input, completion and reply remain distinct. The [macOS live7 summary](../research/lab/results/macos-live-7/README.md)
records native MCP join acceptance and its scenario/harness limits separately
from the earlier stdin lab evidence. For `.3`, integrated source CI and Claude
2.1.308 functional link/current-MCP/reuse/25-second idle checks passed, with a
first-warning ordering deviation. Signed installation, Haiku, two-host,
owner-pasted join and model reply acceptance remain pending. Source/native
results do not qualify an installed signed package.

## Human stdin and private file alternative

A human may run `peerturn join --link-stdin --current` in the selected session's
tool-child terminal, paste into stdin and send EOF. A tool command containing a
heredoc or literal invitation can expose it in shell argv/history and is unsafe.

If stdin cannot be supplied interactively, the human first creates a file with
mode `0600` atomically, under the same trusted OS account:

```sh
umask 077
invite_file=$(mktemp "${TMPDIR:-/tmp}/peerturn-invite.XXXXXXXX")
cat > "$invite_file"
# Human pastes the complete invitation at stdin, then sends EOF.
```

The human gives only the path to the selected agent, which may run
`peerturn join --link-stdin --current < /human-supplied/private-file` and remove
the file after use. The CLI reads stdin and does **not** validate that file's
ownership or mode; the human is responsible for both. An agent's default-mode
file-write followed by chmod does not protect the initial write. Prefer
`peer_join` for agents; do not construct this file from a secret in a tool command.
