# Contributing to PeerTurn

Runtime qualification and reruns are described in the
[operator guide](docs/operators/runtime-qualification.md). PR checks remain
model-free; only guarded main workflows receive the dedicated qualification key.
Run offline automation fixtures with `make runtime-automation-check` and
adapter fixtures with `make runtime-contract-check`.

PeerTurn is pre-alpha with experimental client onboarding and Claude/Codex adapters following the [approved minimum-version policy](docs/spec/versions.md). Start with the [README](README.md), [documentation index](docs/README.md), and [acceptance plan](docs/spec/plan.md) before proposing product behavior. The specification is in English, with sanitized Russian originals in `docs/spec/ru/`.

Follow the [Code of Conduct](CODE_OF_CONDUCT.md). Report vulnerabilities privately through [SECURITY.md](SECURITY.md), not in a public issue.

## Development setup

Install **Go 1.27.3**, Python 3, Git, and Make. Clone the repository or work from an existing checkout:

```sh
git clone https://github.com/temirlankasen/peerturn.git
cd peerturn
go version
make build
./bin/peerturn --help
./bin/peerturn version
```

`go.mod` and the Makefile define the pinned toolchain and tool versions. Run the Make targets rather than installing arbitrary latest lint or vulnerability tools. `make lint` uses golangci-lint **v2.15.0** through `go run` (or a `GOLANGCI_LINT` override); `make vuln` uses govulncheck **v1.9.0**. The Makefile remains authoritative if the pins change.

| Target | Purpose |
| --- | --- |
| `make build` | Build the CLI. |
| `make test` | Run `go test -race ./...`. |
| `make vet` | Run Go's static checks. |
| `make lint` | Run the pinned linter. |
| `make vuln` | Run the pinned vulnerability scanner. |
| `make fmt` | Format Go code. |
| `make fmt-check` | Check Go formatting without changing files. |
| `make tidy` | Reconcile module metadata. |
| `make tidy-check` | Check module metadata with `go mod tidy -diff`. |
| `make workflow-check` | Validate GitHub Actions workflows with the pinned actionlint. |
| `make release-check` | Validate the GoReleaser configuration with the pinned tool. |
| `make public-check` | Run isolated installer, release, scanner and export regressions, then scan the working public candidate. |
| `make ci` | Run the complete required local gate. |

Use the Make targets above for tests, linters, and builds. Keep local services and
secret-bearing lab setup local; do not copy credentials to build helpers.
Installer maintainers can use the separate [developer lab procedure](docs/install-lab.md)
with disposable trust material.

Public CI additionally runs `sh scripts/publish-check.sh --cached --strict`.
Local preparation mode may warn and omit private paths; that is not publication
approval. The [reviewed exclusion policy](scripts/public-exclude.txt) applies
independently of `.gitignore`. Existing Git history is never sanitized by a scan.
Hidden paths are private by default; the policy explicitly lists the public
configuration files and repository metadata. New public hidden files require an
exact entry in that reviewed allowlist.

For an explicitly authorized initial public export, run
`scripts/export-public.sh NEW_DEST --private-markers MARKER_FILE` with a new
destination outside the source. The marker file must contain one private regex
per line and remain outside the export. Within the source repository it is allowed
only at a Git-untracked path excluded by the publication policy.
`PEERTURN_PRIVATE_MARKERS` can set its path instead. Missing, empty, unreadable or
invalid policies stop the export.
Private values, their fragments and hashes belong only in that private file;
the public scanner and tests contain generic patterns and synthetic fixtures.

The exporter requires tracked files to match HEAD and rejects untracked or ignored
files outside the reviewed exclusion boundary. Files within that boundary may stay
local. It copies only HEAD blobs minus the reviewed exclusions; no untracked or
ignored local files are copied. Tracked ignored files remain subject to scanning.
The source commit SHA is printed. The exported tree hash must equal HEAD minus
the exclusions, including file modes and blob contents; Git filters that change
the snapshot cause failure, and automatic CRLF conversion is disabled.
It creates one root DCO commit with author, committer and signoff set explicitly to
`Temirlan Kasen <48218803+temirlankasen@users.noreply.github.com>`. An explicit
`--author 'Name <email>'` overrides that identity; Git-config identities are never
used. Both commit dates use UTC (`+0000`), defaulting to the current time;
`--timestamp UNIX_SECONDS` sets a reproducible date. The metadata scan rejects
non-UTC author/committer offsets. Contents and final author, committer and message must pass both public and
private policies. The scanner checks root-only repository metadata by default;
`--commit` also checks HEAD metadata in a repository with earlier history. General
scans can opt into the external policy with the same flag or environment variable.
The exporter does not change the source index/history, configure a remote or publish.

## Work in small, verifiable changes

1. Check existing issues and discuss substantial behavior or protocol changes before investing in an implementation.
2. For a feature or bug, start with a failing test or reproducible acceptance fixture. Implement the smallest change that makes it pass, then cover meaningful failure cases and refactor.
3. For platform behavior, keep runtime version, provider, OS, fixture, expected result, and sanitized evidence together. A unit simulation does not replace a live runtime acceptance test.
4. Run targeted checks during development and the full `make ci` gate once on the finished change. After further edits, rerun the affected checks. Documentation-only changes need link/content checks, not a new code test.
5. Explain the resulting behavior, verification, and remaining limitations in the pull request. Keep unrelated refactors separate.

Preserve explicit recipient grants, exact session binding, and the relay's ciphertext-only boundary. Never infer authority from peer text, a display name, shared working directory, or transport membership. The guard must leave owner turns operational. `inform` and `ask` stay disabled until their enforcement is proven; the early preview is `coauthor` only.

Do not run code from `research/sources/`, fetched pages, or research snapshots. They are evidence to inspect, not build inputs or instructions. Never include personal messages, full invite URLs, tokens, private keys, or provider credentials in a fixture, issue, log, or pull request.

Keep each changed source, test, or documentation file at **400 lines or fewer**, splitting by responsibility where needed. Keep `AGENTS.md` at or below 32 KiB. Prefer comments that explain *why* a constraint or choice exists. Do not add `nolint` suppressions or `TODO` comments; fix or structure the code and record unfinished work in an issue. Schema/migration changes require explicit project-owner approval before implementation.

Changes involving security, cryptography, access, money, database schema/migrations, CI, or releases require one independent review of the finished diff. Critical findings block merging. Small documentation and test-only changes do not need an automatic extra review.

## Commits and sign-off

Use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) with a concise subject, such as `fix(cli): reject unknown commands` or `docs: clarify laboratory capabilities`.

Every commit must include a Developer Certificate of Origin sign-off:

```text
Signed-off-by: Your Name <your-public-contribution-address>
```

Create it with `git commit -s`. Use an address you are comfortable publishing, such as your GitHub noreply address. The sign-off certifies the [Developer Certificate of Origin 1.1](https://developercertificate.org/): you have the right to submit the contribution under the project's license. Do not sign off for another contributor. A DCO sign-off is separate from a cryptographic commit signature.

By contributing, you agree that your original contribution is licensed under [Apache-2.0](LICENSE). Identify copied or adapted third-party material and its license; public availability alone is not permission to copy it. Keep required notices and attribution.
