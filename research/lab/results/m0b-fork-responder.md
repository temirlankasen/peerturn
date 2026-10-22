# M0b — native fork responder feasibility

Run: 21 October 2026 in a disposable Linux container. Claude Code **2.1.306**
used DeepSeek; Codex **0.163.0** used a test gateway. This sanitized report asks
whether a separate responder could inherit context while leaving the live
conversation and owner permission mode unchanged.

| Control | Observed result | Boundary |
| --- | --- | --- |
| Claude native resume plus fork | New session inherited a synthetic context word and read a fixture; live transcript remained 47 lines. | One context/read control, not full context equivalence. |
| Claude process tool removal | Write/Bash were unavailable; requested write canaries were absent. | Tool removal does not sanitize inherited history or constrain every read. |
| Codex native fork | New thread inherited its synthetic word; live rollout remained 16 lines. | One version and provider profile. |
| Codex OS read-only sandbox | Read succeeded; actual `touch` failed with a read-only filesystem; canary absent. | Sensitive paths and network isolation needed separate qualification. |
| Codex unavailable namespace sandbox | No command executed; no unsandboxed fallback occurred. | Closed failure was observed in the ordinary container profile. |

The successful Codex OS control used a container profile permitting namespace
setup. Live bypass remained unchanged. Some peer-framed questions were refused
by the model despite synthetic authorization; this differed from runtime tool or
OS enforcement.

Native forks were a feasible `ask` building block, not a qualified product
responder. Remaining work included protected paths, authority/provider material,
outbound redaction, context/model continuity, resource accounting, and revoke
races. Read-only is not confidentiality isolation. See [M0](m0-guard/README.md)
and [Claude](m3-claude/README.md)/[Codex](m4-codex/README.md) product evidence.
