# M3 — Claude adapter acceptance

Run: 21 October 2026, disposable Linux ARM64 containers. Claude Code
**2.1.306** used DeepSeek `deepseek-flash[1m]`; owner `bypassPermissions`
remained unchanged. This sanitized historical report distinguishes native runtime
controls from product acceptance. Raw transcripts and identifiers remain private.

The initial product runs observed real tool-child current-session binding,
invite/join, signed hello, idle `coauthor` input, busy delivery after the owner
turn, and rejection of another session sharing the same cwd. Receipts and native
model observations were collected separately. Esc interrupted a pending input
without blind resubmission. Signed reverse receipts reported adapter acceptance
and rejection without claiming completion.

The initial notice contract exposed more context at the owner boundary than the
later design; the [22 October correction run](fixes/README.md) supersedes that
behavior with a fixed count/sender notice and explicit inbox body read.

Product `ask` was rejected because authority isolation was not qualified.
Separate native fork controls inherited synthetic context, left the live
transcript unchanged, and blocked write canaries. Those controls were a subset,
not product `ask` acceptance.

Revocation after native admission could not undo admitted effects and was
reported conservatively as `in_doubt`. A sender-state projection repair was
tested separately. The initial run did not qualify revoke before authorization;
that gap was addressed in the correction run with a distinct pinned artifact.

The corrections used binary SHA256
`2be88bfbca3a5c1a07b1aa979639f3bba3ba6097eb5aaa3a5800e48fc3e6ddd7`
and plugin register SHA256
`d73654c1c6fbc509f3bdaf9273700c4cf856d7c87ed966505f019212565bcdf3`.
Success of those later controls does not rewrite initial failures or establish
restart recovery, universal origin handling, Internet delivery, or exactly-once
effects. All assigned containers were removed after evidence collection.
