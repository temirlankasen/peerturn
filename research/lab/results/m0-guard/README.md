# M0 — restricted peer-turn guard feasibility

Run: 21 October 2026, disposable Linux ARM64 containers. Claude Code
**2.1.306** used DeepSeek `deepseek-flash[1m]`; Codex **0.163.0** used
`gpt-5.4-mini`. Owner bypass/never modes were preserved. This is a sanitized
summary of private hook, provenance, and action-canary experiments.

**Neither tested stock runtime qualified a deterministic restricted peer-turn
guard.** Healthy hooks could deny individual tools; failure did not preserve the
required peer-only fence.

| Question | Result | Meaning |
| --- | --- | --- |
| Claude healthy deny | Bash, Write, Edit, Agent denied; read fixture allowed. | Healthy callback capability demonstrated. |
| Claude owner control | Same writes succeeded on a later owner turn with guard loaded. | Owner availability preserved in that control. |
| Claude exception, timeout, helper loss | Mutating canaries created. | Decision-producing guard failure was fail-open. |
| Claude callback removal/reload | Empty callback module reloaded; later peer effect executed. | No runtime-owned immutable guard requirement. |
| Codex synchronous provenance | No trusted queue-to-turn mapping in observed synchronous hook input; async correlation existed. | Async attribution could not enforce before-effect decisions. |
| Codex deny coverage | Exec, MCP write, and spawn denied. | Native patch, wrappers, and hosted tools not universally qualified. |
| Codex nonempty `write_stdin` | Existing shell accepted mutating input without PreToolUse. | A real effect bypassed the proposed boundary. |
| Exact tool-child binding | Distinct sessions in one cwd distinguishable on both platforms. | Full instance/epoch/resume/tampering qualification incomplete. |

Native `apply_patch` was not demonstrated in the live Codex inventory; a shell
command with that name tested only exec. Source observations at
`c419fd01a70dd1d05356da443ac44328bda2e6f4` were separate from binary proof.
Source/release correspondence was not established.

Restricted peer turns need trusted provenance before the first effect, complete
coverage, a runtime-owned failure fence, and immutable restriction lifetime.
Missing callbacks, unload, state loss, timeout, or unknown origin must not become
owner authority. A global hook-failure block impairs verified owner turns and
does not meet the contract.

Unsupported restricted admission therefore requires `guard_unavailable`.
The later approved `coauthor` preview is a separate strong-trust mode with normal
receiver capabilities. [M0b](../m0b-fork-responder.md) explored native forks as a
different boundary. Raw evidence remains private; containers were removed.
Universal coverage and release qualification are not claimed.
