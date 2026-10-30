# Support

PeerTurn is pre-alpha. **0.1.3-preview is a prepared candidate, not yet published**;
v0.1.2-preview remains the last published release, with its historical Claude
loader failure fixed in the candidate. The new installer pins are usable once
the exact signed assets and attestations are published. There is no production
support SLA.

The preview implements transport, encryption, grants, onboarding and experimental
Claude/Codex adapters. The [support matrix](README.md#agent-support-and-laboratory-evidence)
keeps pinned Linux/macOS qualification separate from new source/native results.
Integrated source CI and Claude 2.1.308 fresh nonadmin link/current-MCP/reuse/
25-second idle checks passed, with a first-warning ordering deviation. Signed
installation, Haiku, two-host, owner-pasted join and model reply acceptance for
`.3` remain pending. A functional check is not full procedural qualification.

The version policy accepts Claude >=2.1.306 and Codex >=0.163.0 without an upper
bound; newer versions retain existing capabilities with `newer_unverified`.
Ask is separately verified-only; receivers remain closed on macOS and Codex.
Claude supports [connect](docs/connect.md); Codex retains its documented setup.
Relay self-service is operator opt-in, closed by default. A client installer
does not authorize production, accounts or ACL changes.

Start with the [README](README.md), [documentation index](docs/README.md), and [capability evidence](research/lab/results/l1-platform/README.md). The specification is available in English, with [Russian originals](docs/spec/ru/) preserved.

## Questions and feature requests

Use [GitHub issues](https://github.com/temirlankasen/peerturn/issues). Search existing issues first, describe the outcome you need, and distinguish a new request from a documented future milestone. The [maintainer's profile](https://github.com/temirlankasen) identifies the project owner.

For security-sensitive reports, follow [SECURITY.md](SECURITY.md) and keep details private. For community behavior, use the reporting route in the [Code of Conduct](CODE_OF_CONDUCT.md).

## Useful bug reports

Include:

- The commit or version, Go version, OS, and architecture.
- Exact commands, minimal reproduction steps, expected behavior, and observed output.
- Relevant exit codes and sanitized logs using synthetic data.
- For adapter issues, the agent/runtime version, provider class, session lifecycle, and the specific receipt/capability involved.

A successful send or queue response is not proof of model input or a completed action. Identify the last observed delivery stage instead of marking the whole interaction as successful or failed without evidence.

Remove credentials, private keys, full invitation URLs, real messages, personal paths, and transcripts before posting. Do not upload someone else's data. Screenshots are optional; small text reproductions are usually easier to investigate.

Contributions follow [CONTRIBUTING.md](CONTRIBUTING.md). Roadmap and scope decisions follow [GOVERNANCE.md](GOVERNANCE.md).
