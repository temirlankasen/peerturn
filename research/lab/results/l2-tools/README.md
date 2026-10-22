# L2 — prior-art transport and authority probes

Run: 21 October 2026, approximately 11:15–11:50 UTC. Separate disposable
containers used distinct synthetic identities and home directories; another
container hosted relay/broker fixtures. This sanitized report summarizes private
fault and canary experiments; raw messages and collection tooling remain private.

The probes separated transport storage from live model input. They included lost
acknowledgements, retries, wrong-session addressing, pending input, revocation,
and synthetic action canaries. Source review supplemented live observations;
these are different evidence classes.

Claude Code **2.1.306** used DeepSeek; Codex **0.163.0** used
`gpt-5.4-mini` through a test gateway. Owner bypass modes remained unchanged.
Selected source pins and bounded results:

| Candidate | Pinned commit | Observation |
| --- | --- | --- |
| retalk 0.3.0 | `a3b99c18d5b219d3ed115494cc4cd33d2e64ee52` | Invite, offline/restart, and ciphertext-replay smoke passed; output failure reproduced acknowledgement before successful downstream output. |
| agent-talk 0.2.4 | `8a7fc4f140ef233f163c4f5816ff739dea096cdc` | Codex hooks delivered at a turn boundary; the tested path did not wake idle Codex. |
| hcom 0.7.31 | `de33e31a0cdbba4c5863f7cc050d8b79aa50bcdf` | Existing Claude/Codex idle wake observed; trusted group membership also permitted remote lifecycle actions, beyond PeerTurn's session-only scope. |
| OCS 0.9.3 | `c06f9766b7404be6e863c5d2703b6f75b9dce755` | Existing idle Claude and Linux Codex wake observed after the runtime dependency was installed. |
| AI-Connect | `b9fdf600f9ec8420bb92e763475997be4a1c04c9` | Existing idle Claude wake observed in the tested profile. |
| agmsg 1.5.3 | `183e4f494a61cef20b1f3c5691021cc59ded4a20` | Existing Codex wake observed; reference database ran PostgreSQL 15.19 rather than its reference version 17. |

Findings carried into PeerTurn:

- Transport acceptance or shell success does not prove intended model input.
- Lost acknowledgements create ambiguity; blind retries can duplicate input or
  actions. Custody and deduplication require explicit application contracts.
- Discovery, shared directories, display names, and local trust do not authorize
  one exact recipient session.
- Mailbox admission does not restrict model actions after it consumes peer text.
- Revocation needs admission checks. It cannot undo completed effects or erase
  information already read.
- Terminal carriers and native agent ingress have different target, busy, and
  draft failure modes.

These observations informed separate storage, adapter, model-input, completion,
and reply states. They do not certify upstream security or exactly-once effects.
The [prior-art overview](../../../../docs/prior-art.md) links public sources.
See [M0](../m0-guard/README.md) for later deterministic guard experiments.
