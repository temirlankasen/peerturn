# Install-by-link native acceptance

**Passed locally on 2026-10-22:** fresh Claude Code **2.1.306** and Codex CLI
**0.163.0** sessions installed an unpublished, locally served signed preview,
joined their selected CURRENT sessions, received native coauthor input, and
produced the expected assistant replies. Three disposable machines used Linux
ARM64 and synthetic data only.

The public [structured observations](evidence-summary.json) retain artifact
hashes, runtime settings, session aliases, activation steps, join states, exact
synthetic markers and independent receipt outcomes. Private harnesses, prompts,
transcripts and raw evidence are excluded from publication.

| Receiver | Activation and binding | Native input → assistant reply | Independent receipt |
| --- | --- | --- | --- |
| Claude | Same session after `/reload-plugins`; native nonce binding accepted | `PUB_NATIVE_CLAUDE` → `PUB_NATIVE_CLAUDE_ACK` | `adapter_accepted`, `claude_prompt_submit` |
| Codex | Same native thread resumed; actual hook reviewed and trusted | `PUB_NATIVE_CODEX` → `PUB_NATIVE_CODEX_ACK` | `adapter_accepted`, `codex_queue` |

Both original invitations were actually pasted as prose plus the complete URL.
Each also had an owner-provided private `0600` copy for stdin. The pasted URL
matched that copy, including its fragment; comparison of surrounding invitation
text permits the composer's trailing-newline normalization. The agents fetched
the fragment-free instruction page, inspected the installer, verified the real
cosign signature and archive checksum, and joined through their own native tool
children. Both progressed from `waiting_inviter` to signed-hello `connected`.
No invitation secret appeared in their shell command text.

The sender was an explicitly labeled **test_session** fixture; native source
binding is not claimed. A separate Claude session in the same directory was
refused with `needs_rebind` and received no coauthor marker. Codex neighboring
session isolation and direct Claude↔Codex exchange were not exercised here.

Repeated `setup claude` and `setup codex` both exited **0** against the same pinned
package. Claude reported the installed plugin was current; Codex accepted the
existing marketplace and added/refreshed its plugin. This does not qualify an
upgrade to different content or automatic hook-trust invalidation.

This was an assisted bootstrap. Claude required the normal reload step; its host
showed one loaded plugin and hook without a separate trust dialog. Codex required
resume and its real hook-trust UI. Its initial native-tool daemon launch did not
survive exit; the same persisted client was recovered under a separate supervisor
with an explicit runtime environment. No reinitialization or invitation replay
was used for that recovery. Native model, effort and permission defaults were
preserved.

The release used an explicit disposable cosign key and **cosign 3.1.3**, with no
public OIDC or transparency claim. The verifier binary matched the upstream
[published release checksums](https://github.com/sigstore/cosign/releases/tag/v3.1.3);
that bootstrap relied on HTTPS and published hashes, rather than independently
qualifying the verifier publisher. An altered signed checksum manifest was
rejected before installation, and untrusted TLS was rejected.

Receipts, native model input and assistant reply were observed separately.
Assistant text does not establish automatic reverse-message transport,
exactly-once effects, or completion receipts. Coauthor retains the receiving
agent's normal capabilities: **strong trust, not a sandbox**; peer text can
influence later owner turns. Production publication, Internet deployment,
restricted ask and other platforms remain outside this run.

Teardown succeeded for only the three assigned disposable machines; their
processes and containers were removed. No unrelated lab container was targeted.
