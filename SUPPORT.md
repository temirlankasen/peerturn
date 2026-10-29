# Support

PeerTurn is pre-alpha, with **0.1.2-preview as the current experimental release**. The preview implements transport, encryption, grants, onboarding and experimental Claude/Codex adapters with platform-specific acceptance limits. The [support matrix](README.md#agent-support-and-laboratory-evidence) records pinned Linux and macOS 27.0 arm64 qualification and limits. The version policy accepts Claude >=2.1.306 and Codex >=0.163.0 without an upper bound; newer versions retain full existing capabilities with `newer_unverified`. Ask is separately verified-only; receivers remain closed on macOS and Codex. There is no production support SLA.

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
