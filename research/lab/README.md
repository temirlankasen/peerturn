# Laboratory evidence summaries

These sanitized reports describe version-pinned experiments from 21–22 October
2026. They separate transport storage, adapter acceptance, observed model input,
turn completion, and a reply. A successful command exit alone proves none of the
later stages.

| Report | Scope |
| --- | --- |
| [L1 native ingress](results/l1-platform/README.md) | Claude Code and Codex ingress, idle/busy behavior, restart caveats. |
| [L2 prior-art probes](results/l2-tools/README.md) | Transport, authority, ambiguity, and revocation observations. |
| [Monitor wake](results/host-monitor-ws.md) | A separate macOS Claude login profile with WebSocket wake. |
| [M0 guard](results/m0-guard/README.md) | Failures blocking a restricted automatic peer-turn guard. |
| [M0b fork feasibility](results/m0b-fork-responder.md) | Context forks and bounded read-only controls. |
| [M3 Claude](results/m3-claude/README.md) | Initial adapter acceptance and historical limitations. |
| [M3 corrections](results/m3-claude/fixes/README.md) | Binding, notice/body separation, and pending revoke. |
| [M4 Codex](results/m4-codex/README.md) | Initial shared-daemon and adapter controls. |
| [M4 corrections](results/m4-codex/fix-20261022/README.md) | Final pinned Codex run and remote receipt caveat. |
| [Mixed integration](results/int-m3-m4/README.md) | Claude ↔ Codex, notice/body separation, and local revoke. |
| [Internet relay smoke](results/relay-vps-smoke.md) | Synthetic CLI sessions through a public TLS relay. |

Raw transcripts, native identifiers, collection scripts, local configuration,
third-party snapshots, and deployment details are retained privately and excluded
from the public repository. These are historical reports, not independently
reproducible public fixtures. Original summaries remain archived locally;
publication sanitization did not rerun the experiments.

Results apply to the named versions, profiles, artifacts, and observed intervals.
They do not qualify a release, hardened `ask`, universal origin detection, crash
recovery, or exactly-once effects. `coauthor` uses the receiving agent's normal
capabilities and remains strong trust. See the [public prior-art overview](../../docs/prior-art.md).
