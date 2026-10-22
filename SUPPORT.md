# Support

PeerTurn is pre-alpha and has no published release. The executable currently provides help and version output; transport, encryption, grants, onboarding, and adapters remain planned. There is no production support SLA.

Start with the [README](README.md), [documentation index](docs/README.md), and [capability evidence](research/lab/results/l1-platform/README.md). The specification is currently in Russian; an English translation is planned before the first release.

## Questions and feature requests

Use [GitHub issues](https://github.com/temirlankasen/peerturn/issues) once the repository is published. Search existing issues first, describe the outcome you need, and distinguish a new request from a documented future milestone. GitHub links may be unavailable before publication. The [maintainer's profile](https://github.com/temirlankasen) identifies the project owner.

For security-sensitive reports, follow [SECURITY.md](SECURITY.md) and keep details private. For community behavior, use the reporting route in the [Code of Conduct](CODE_OF_CONDUCT.md).

## Useful bug reports

Include:

- The commit or version, Go version, OS, and architecture.
- Exact commands, minimal reproduction steps, expected behavior, and observed output.
- Relevant exit codes and sanitized logs using synthetic data.
- For future adapter issues, the agent/runtime version, provider class, session lifecycle, and the specific receipt/capability involved.

A successful send or queue response is not proof of model input or a completed action. Identify the last observed delivery stage instead of marking the whole interaction as successful or failed without evidence.

Remove credentials, private keys, full invitation URLs, real messages, personal paths, and transcripts before posting. Do not upload someone else's data. Screenshots are optional; small text reproductions are usually easier to investigate.

Contributions follow [CONTRIBUTING.md](CONTRIBUTING.md). Roadmap and scope decisions follow [GOVERNANCE.md](GOVERNANCE.md).
