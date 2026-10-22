# M4 — Codex adapter acceptance

Run: 21 October 2026 UTC, disposable Debian 12 Linux ARM64 containers. Codex
CLI **0.163.0** used `gpt-5.4-mini`, effort `low`. Owner `never` and
`danger-full-access` remained unchanged. This is a sanitized private-run report.

Native controls observed exact shared-daemon thread binding, idle wake, busy
FIFO, inherited synthetic fork context, and OS-denied writes. A separate idle
fork left the live rollout unchanged. An embedded TUI accepted storage without
being a loaded live thread; this was a negative attachment control.

Product controls observed real tool-child `init`/`join`, exact binding despite a
shared cwd, idle `coauthor`, busy FIFO, and storage receipts distinct from native
model input. `inform` did not wake idle receivers; the initial owner-boundary
body contract was later replaced by the notice/body separation in the
[correction run](fix-20261022/README.md). `ask` was closed with
`responder_unavailable/protected_path_policy_failed`.

Initial final-client SHA256:
`ff7fbb41f2380cebeededfa4b48b74f85a3a54c31cf0af1343be737d310f93d8`.
The relay used an earlier artifact. A later local build with SHA256
`f0c0c2862bb1e5be27d6065e142e3d82eb6e2d5c0e23b0870512df25950b8f98`
was not live-rerun here and did not inherit acceptance from those client runs.

An early generic owner hook consumed pending inform on queued peer input. The
product needed explicit distinction of its queue events. Embedded startup flags
and arbitrary startup config could select a different runtime mode; normal hook
trust and shared-daemon loading had to be observed separately.

The controls did not qualify hardened authority-path isolation, redaction,
localhost/descendant networking, provider-auth isolation, crash recovery,
concurrent budgets, arbitrary turn origins, Internet/NAT, or exactly-once effects.
A stock read-only fork could read more than the workspace. Containers were removed
after collection; the full M4 matrix was not declared green.
