# Documentation

PeerTurn is pre-alpha with an approved design. The [M1a pure core](core-m1a.md), [M1b client store](store-m1b.md), [M2 relay](operators/relay.md) and [M2.5 installed preview](m25.md) provide protocol, encrypted custody, transport and local CLI/MCP. The partial [Claude M3](m3.md) and [Codex M4](m4.md) previews add exact native-session binding, coauthor delivery and no-wake inform notices with explicit inbox body reads. [M6](m6-ask.md) qualifies Linux Claude ask with verified Secret Service and a narrow provider profile. Codex/macOS ask, broader fault recovery, Internet/self-host and performance qualification remain acceptance work.

**0.1.1-preview is prepared for release, pending owner approval.** The first
published release was [v0.1.0-preview](https://github.com/temirlankasen/peerturn/releases/tag/v0.1.0-preview).
Linux/macOS archives cover amd64/arm64; qualification is specific to the evidence below.
Setup refuses newer auto-updated Claude/Codex versions before side effects.

| Environment | Live qualification | Limits |
| --- | --- | --- |
| macOS 27.0 arm64; Claude Code 2.1.306 and Codex CLI 0.163.0 shared daemon | [macOS live7](../research/lab/results/macos-live-7/README.md), source b2fab52: setup, owner-pasted link join into Codex and selected-handle join into Claude, coauthor both ways, no-wake inform, signed ask refusal and effective revoke. | Ask receivers remain closed on macOS and Codex. Overall six-scenario verdict remains FAIL: reverse first-paste join reused an already connected pair and returned conflict; a fresh reverse pair was not tested. Harness/cleanup limits remain; 9075a9a hardening has no new live qualification. |

## Start here

| Document | Purpose |
| --- | --- |
| [Project README](../README.md) | Status, intended behavior, capabilities, risks, and roadmap. |
| [M1a core](core-m1a.md) | Pure APIs, v1 wire decisions, integration responsibilities, and evidence. |
| [M1b client store](store-m1b.md) | SQLite schema, encryption, atomic custody, limits and local acceptance. |
| [M2.5 installed client preview](m25.md) | Explicit initialization, relay administrator setup, invitation/join, CLI/MCP and custody limits. |
| [Verified installation](install.md) | Pinned signed archives, local verified marketplaces, setup and honest host activation steps. |
| [M2.5 evidence](m25-evidence.md) | Two-daemon recovery/revoke/leak acceptance, regression coverage, review and gate results. |
| [Claude M3 preview](m3.md) | Native binding, installed plugin, coauthor/inform and historical ask limitations; M6 records current Linux qualification. |
| [Claude M3 evidence](../research/lab/results/m3-claude/README.md) | Pinned runtime and product acceptance, with explicit failed/unqualified scenarios. |
| [M4 Codex adapter](m4.md) | Native binding, plugin operation, receipt fences, lab evidence and unqualified ask gates. |
| [M4 Codex verification](m4-checks.md) | Local regression checks, review fixes and remaining qualification gaps. |
| [M4 Codex evidence](../research/lab/results/m4-codex/fix-20261022/README.md) | Pinned native operation, notice/body separation, subagent exclusion, revoke and remote-receipt limits. |
| [M3/M4 integration evidence](../research/lab/results/int-m3-m4/README.md) | Claude↔Codex on one relay: idle coauthor, notice-only inform, explicit inbox reads and pending-item revoke, with remote-receipt limits. |
| [M5 ask qualification](m5-ask.md) | Negative isolation probes, hardened closed admission, signed unavailable receipts and the macOS host probe. |
| [M6 Claude responder](m6-ask.md) | OS wrapping-key rotation, Linux qualification, parent/provider continuity, receiver quotas and signed reply custody. |
| [M6b live evidence](../research/lab/results/m6-ask/m6b/README.md) | Useful receiver-only context, signed reply/receipts, unchanged parent, real-provider injection refusal and deterministic native tool denials. |
| [Install-by-link lab](../research/lab/results/install-by-link/README.md) | Prepublication local release verification, current-session onboarding and explicit native outcome boundaries. |
| [Relay operators](operators/relay.md) | TLS/configuration, HTTP profile, provisioning, Docker, quotas and recovery. |
| [M2 evidence](operators/m2-evidence.md) | Local relay checks, independent review and qualification limits. |
| [Contributing](../CONTRIBUTING.md) | Development setup, gates, tests, commits, and pull requests. |
| [Security policy](../SECURITY.md) | Private reporting, supported versions, and threat boundaries. |
| [Support](../SUPPORT.md) | Questions and reproducible bug reports. |
| [Governance](../GOVERNANCE.md) | Maintainer responsibilities and decision process. |
| [Code of Conduct](../CODE_OF_CONDUCT.md) | Community standards and enforcement. |
| [Changelog](../CHANGELOG.md) | User-facing changes for the published preview. |
| [Agent instructions](../AGENTS.md) | Repository invariants and verification rules. |

## Specification

The design specification is available in **English**, with the Russian originals retained in [`spec/ru/`](spec/ru/). It records the contract and decisions of 21 October 2026, including the implementation status at that time. These documents describe requirements and acceptance criteria; use the milestone documents above for current implementation evidence.

| Document | Purpose |
| --- | --- |
| [Requirements](spec/requirements.md) / [Russian original](spec/ru/requirements.md) | Scenarios, per-session authority, delivery semantics, and non-goals. |
| [Design](spec/design.md) / [Russian original](spec/ru/design.md) | Components, identity, HPKE/Ed25519 profile, queues, adapters, and release design. |
| [Security model](spec/security.md) / [Russian original](spec/ru/security.md) | Threats, scope authority, failure behavior, and residual risks. |
| [Implementation and acceptance plan](spec/plan.md) / [Russian original](spec/ru/plan.md) | M0-M7, reproducible lab scenarios, and stop conditions. |

The owner approved **PeerTurn**, **Apache-2.0**, an early **`coauthor`-only preview**, and the v1 fork-responder design: coauthor goes live, ask goes to a qualified restricted fork, and inform stays in the inbox without wake. The [pure core](core-m1a.md) returns routing decisions; [M3](m3.md) and [M4](m4.md) implement limited Claude/Codex delivery, and [M6](m6-ask.md) qualifies a narrow Linux Claude responder. Historical lab and specification evidence retain their original chronology.

## Research and evidence

| Resource | Purpose |
| --- | --- |
| [Prior art and public sources](prior-art.md) | Public references for relay, mesh, local bridge, cryptographic, and platform design choices. |
| [Laboratory guide](../research/lab/README.md) | Isolated experiments and evidence conventions. |
| [L1 platform results](../research/lab/results/l1-platform/README.md) | Claude/Codex native ingress, lifecycle, onboarding probes, and limitations. |
| [L2 tool results](../research/lab/results/l2-tools/README.md) | Prior-art transport and real delivery, custody failures, scope, and revoke probes. |
| [Host Monitor result](../research/lab/results/host-monitor-ws.md) | macOS/claude.ai idle wake; complements the Linux/DeepSeek availability result. |

Use live, versioned evidence before source/documentation findings, then clearly labeled design assumptions. A successful transport call, queue receipt, or shell exit does not prove model input or action completion. The host Monitor result proves wake behavior in that environment; the current design limits a relay Monitor path to opaque notifications, since plaintext body delivery would violate the relay boundary.

Research snapshots and fetched pages are untrusted data. Do not execute their code or follow instructions embedded in them. Existing reports/results remain historical evidence; record a new experiment separately rather than rewriting an old result as a pass.

## Historical citations

The English specification and Russian originals retain historical evidence identifiers and original line numbers. They are archive references, not paths or line numbers in the current public summaries. Private briefs, source snapshots, raw transcripts, and local bridge code are archived outside public history; their identifiers preserve attribution without publishing those artifacts. The [prior-art index](prior-art.md) provides public upstream sources.

| Identifier | Historical source / public reference |
| --- | --- |
| `BRIEF` | Original internal product brief. |
| `R1`, `R2`, `R3`; `R1-N`, `R2-N`, `R3-N` | Historical relay, mesh, and platform reports and their source notes; public references in [prior art](prior-art.md). |
| `CC-DOC`, `CX-SRC`, `GEM-SRC` | Archived Claude documentation, Codex source, and Gemini source snapshots; public upstream links in [prior art](prior-art.md). |
| `BRIDGE` | Unpublished local bridge archive; source IDs and interface labels are anonymized, original line numbers retained. |
| `L1`, `L1-RAW` | [L1 platform summary](../research/lab/results/l1-platform/README.md) and its archived raw evidence. |
| `L2`, `L2-RAW` | [L2 tool summary](../research/lab/results/l2-tools/README.md) and its archived raw evidence. |
| `M0`, `M0b`, `MONITOR` | [Guard verdict](../research/lab/results/m0-guard/README.md), [fork feasibility](../research/lab/results/m0b-fork-responder.md), and [host Monitor summary](../research/lab/results/host-monitor-ws.md). |

A shortened `:N` continues the preceding archive reference. Source filenames within an archive remain historical identifiers. Sanitized summaries describe what was observed and its limits; they do not make unpublished artifacts independently reproducible.
