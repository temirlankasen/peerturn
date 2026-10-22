# M6c review regression acceptance — 22 October 2026

**Scoped PASS:** Linux ARM64, Claude Code **2.1.306**, the existing lab custom
HTTPS gateway, requested `deepseek-flash[1m]`, effective `deepseek-flash`, effort
`max`. Only disposable `a1`, `a2`, `a3` were used. No publication, host keychain,
account change or commit was performed. Raw keys, native journals, invitation
fragments, exact native identifiers and message bodies stayed inside the lab.

Final receiver product SHA-256:

```text
23c2d584aa0c4869ffc13da5f26c61be2edf92c5315556c77b822acea361494e
```

## Product and native observations

| Check | Verdict and evidence |
| --- | --- |
| Useful signed ask | [09-final-calibration.json](09-final-calibration.json): authenticated reply matches the hash of a shareable fact present only in the receiver's conversation. Storage, responder start, model input, completion, redaction and reply have distinct authenticated evidence. The entire parent stayed byte-identical. |
| Earlier candidate | [01-initial-calibration.json](01-initial-calibration.json) preserves the earlier successful candidate, including actual synthetic token replacement. Later output may refuse to echo the token; redaction-stage evidence alone is not a replacement claim. |
| Fork retention | [02-fork-retention.json](02-fork-retention.json): no native artifact containing the product child UUID remains; explicit `--resume <child>` exits 1 with “No conversation found”. Neither native history nor `.claude.json` contains that UUID. |
| HOME root | [04-home-root.json](04-home-root.json): a separate real native session started in HOME and bound to a real OS-isolated daemon reports `fork_responder=false`, `project_root_unqualified`. Coauthor/inform capability remains. No signed peer ask is claimed for that separate identity; daemon admission is exercised independently below. |
| Queue wedge and restart | [05-linux-regressions.json](05-linux-regressions.json): Linux race tests use actual SQLite custody, bounded signed packet fixtures, daemon methods and restart. Full control lanes cannot prevent terminal custody, consume a new fork reservation or create a live uncertainty fence; recovery discards uncertain answers without replay. This is an executable daemon/store fixture, not a real-relay 429 experiment. |
| Live coauthor after restart | [06-live-coauthor.json](06-live-coauthor.json): the original receiving native session produced the expected assistant marker after daemon replacement/restart. The control-lane fault itself is exercised by the separate fixtures above. |
| Forced native tools | [03-native-denials.json](03-native-denials.json): eight actual native tool results, exact Read/Grep/Glob inventory, public Read success, in-project `.ssh`/nested `.docker` Read denials, direct `.env` Grep denial, root Grep without denied canaries, outside Read denial despite broad user allow rules, no `--pre=` process effect, and no files returned by Glob `../`. User/project hook effects are absent. |

The forced dispatcher fixture uses a loopback fake Anthropic endpoint and an
invented credential/model. It does not prove provider delivery or daemon grant
admission. Its settings were copied inside the container from the actual product
fork; the product's temporary settings and transcript were subsequently removed.
Broad user/project `permissions.allow:["Read(//**)"]`, SessionStart and
UserPromptSubmit hook canaries were installed only for the deterministic native
check, then the original settings bytes were restored.

Grep over the project and Glob traversal returned empty results rather than
permission errors. The direct denied-file operations returned actual permission
errors. Neither result is inferred from the model verbally refusing a request.

## Transcript and environment qualification

A separate synthetic native probe compared recursive hashes under `.claude` and
the `.claude.json` file before/after `-p --resume --fork-session --session-id`.
Only `projects/<project>/<child>.jsonl` changed. The supplied UUID appeared in
init/result; history and last-session configuration had no child reference.
The product preselects/seals that UUID before spawn and removes its journal,
project subdirectory and UUID-specific debug/tasks/session-env/file-history/todo
artifacts after Wait, including failure/cancellation paths. Removal failure
suppresses output and closes further asks. Cleanup metadata survives GC until
deletion is confirmed. An unverified legacy config root or still-running orphan
is not silently treated as cleaned.

The pinned binary contains all four selected environment names, and the fixed
child environment sets `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`,
`DISABLE_AUTOUPDATER=1`, `DISABLE_TELEMETRY=1`, `DISABLE_ERROR_REPORTING=1`, plus
`ENABLE_TOOL_SEARCH=false`. This checks the pinned names and configured launch;
it is not a packet-capture proof that every non-provider network request ceased.

## Reproduction and checks

Own-source orchestration is in [live.py](live.py), [accept.py](accept.py),
[observe_native.py](observe_native.py), [receipts.go](receipts.go) and
[native_denials.py](native_denials.py). Use fresh assigned disposable machines,
the base checkout's lab client and the pinned toolchain. The fixture-only HTTP
server serves the compiled product, plugin, sanitizer and source archive from a
temporary artifact directory. Provider credentials are inherited only inside
containers; they are never copied into these artifacts.

After daemon replacement, reload the native plugin and rebind the same UUID.
A first attempt without reloading correctly lost its binding; it was not
represented as an ask reaching the model or automatically resubmitted. Every
later explicit submission used a fresh message ID. A completed idle parent is
required before freezing or submitting. The temporary HOME test used a separate
identity and keystore to preserve the original parent's binding.

All builds, tests and linters used isolated Go caches.
Go **1.27.3** was also installed inside disposable `a2` for native Linux race
execution with the memory-keystore and native-executable fixtures. The normal
full gate's build, vet and repository race tests passed; its first lint step
found six fixable diagnostics. Corrected lint plus vulnerability/workflow/release
checks passed separately without repeating successful full tests. The newly
tracked Go evidence packages were additionally linted with research exclusions
removed; that check also passed. Exact commands and exit codes are in the
implementation handoff, not inferred from the lab broker's outer shell exit.

Failed intermediate fixture attempts remain in temporary implementation logs:
missing Linux XDG runtime, an early sanitizer observation before reply custody,
and native plugin reload after replacement. They do not become positive delivery
evidence. The 90-second diagnostic full-daemon run timed out under concurrent
load; the required full race run completed successfully in 213.847 seconds.

[teardown.json](teardown.json) records removal of all assigned machines after
evidence collection. No raw material was exported or force-added.
