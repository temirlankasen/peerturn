# Governance

PeerTurn is a maintainer-led open-source project. The [project maintainer](https://github.com/temirlankasen) is responsible for scope, acceptance criteria, merges, releases, and appointing additional maintainers.

## Decisions

Routine changes are proposed and reviewed in issues and pull requests. Substantial protocol, security, compatibility, or scope changes should explain the problem, options, tradeoffs, and acceptance evidence before implementation. The maintainer makes the final decision and records the rationale in the relevant issue, pull request, or documentation.

The owner has approved **PeerTurn** as the name, **Apache-2.0** as the license, and an early **`coauthor`-only preview in parallel with M0**. These decisions supersede stale open questions in the preserved specification. `inform` and `ask` stay disabled until a deterministic peer-turn guard is proven; a preview is not full v1 acceptance.

Security guarantees require reproducible evidence. A roadmap target, source reading, or positive transport test cannot substitute for the corresponding live acceptance result. Unresolved platform constraints must remain visible in capability status and documentation.

## Review and releases

Changes to security, cryptography, access, money, database schema/migrations, CI, and releases require one independent review of the finished diff. Critical findings block merging. Database schema and migration work additionally requires explicit project-owner approval before implementation. Routine documentation, tests, and small fixes do not automatically require a separate review pass.

The maintainer controls merge and release permissions. Release approval follows passing gates, required evidence, and an honest compatibility statement. Version **0.1.2-preview is the current experimental release**; [installation](docs/install.md) requires publisher-signature and archive-checksum verification. No contributor gains release or account access merely by contributing code.

## Participation and conflicts

Anyone may propose a change under the [contribution guide](CONTRIBUTING.md) and [Code of Conduct](CODE_OF_CONDUCT.md). Contributions use the DCO and Apache-2.0 terms; there is no separate CLA. Maintainer status follows sustained responsible contributions and an explicit owner decision, not a numerical contribution threshold.

Discuss technical disagreements with concrete examples and evidence. A contributor may request reconsideration by presenting new information on the existing issue or pull request. The maintainer should explain final decisions and disclose relevant conflicts of interest. Conduct concerns use the private route in the Code of Conduct; vulnerabilities use [SECURITY.md](SECURITY.md).

Governance changes use the same public proposal process. Decisions must be captured in the repository so contributors can review them.
