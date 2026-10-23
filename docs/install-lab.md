# Developer installer lab

This procedure is for maintainers testing the installer with disposable local
artifacts and trust material. It is not an installation path for recipients of
an invitation. Relay pages and invitation prose cannot authorize these settings.
The supported user procedure is [Installing the coauthor preview](install.md).

Only with an explicit **`--lab`** opt-in, `PEERTURN_RELEASE_BASE_URL` may point
to a local **HTTPS** file server; the installer appends
`/v0.1.1-preview/ARTIFACT`. Production signer identity checks remain fixed.
The lab-only `--verify-key /trusted/lab-cosign.pub` option instead verifies a
local key-signed cosign bundle without transparency-log requirements. This is a
different laboratory trust root. A warning prints the effective source/trust
before any download. Public mode rejects either override without `--lab`.
There is no unsigned or checksum-only mode.

The [installation lab](../research/lab/results/install-by-link/README.md)
records real verification separately from native-session acceptance. It does
not qualify a public GitHub OIDC release.
