# M3 corrections — pinned Claude live recheck

Run: 22 October 2026 local date; container timestamps were UTC. Disposable Linux
ARM64 containers used Claude Code **2.1.306**, DeepSeek, Node **24.22.1**, and
Python **3.11.2**. Owner `bypassPermissions` stayed unchanged. This sanitized
summary preserves the outcomes of private acceptance fixtures.

Final binary SHA256:
`2be88bfbca3a5c1a07b1aa979639f3bba3ba6097eb5aaa3a5800e48fc3e6ddd7`.
Plugin register SHA256:
`d73654c1c6fbc509f3bdaf9273700c4cf856d7c87ed966505f019212565bcdf3`.

An earlier bootstrap failed: an unconfirmed instance received `needs_rebind`
at the owner boundary, and the helper exited before actual binding. A model's
ready marker was not counted as native binding evidence. That artifact was not
used for delivery acceptance. A corrected intermediate build passed binding;
behavioral acceptance used the final pin above.

| Final control | Observed result |
| --- | --- |
| Exact native binding | Real tool children bound the current native sessions; parent-process guard and signed hello were observed. |
| Idle coauthor | Plugin-origin input and reply occurred in the same native receiver, with bypass unchanged. |
| Busy coauthor | Delivery waited for the actual owner turn to finish. |
| Inform pending | No wake and no body input before the next owner turn. |
| Owner notice | Only fixed count/sender metadata and an inbox instruction entered the boundary. |
| Notice receipt | The offer was acknowledged after successful native `next`, separately from body read. |
| Explicit inbox read | Body appeared in a native tool result; unread count reached zero and signed body-returned receipt followed. |
| Revoke before authorization | Target stayed at intent, then became terminal `revoked/not_sent`; sender received that outcome. |
| Post-owner observation | Revoked target body/ACK absent; native helper and binding still live. |

Aggregate `in_doubt` after revoke referred to two previously admitted messages,
not the never-authorized target. Revocation cannot undo effects already admitted.

The archived evidence validator checked 91 JSON artifacts, pins, acceptance
conditions, links, line limits, and secret patterns with zero issues. This was a
historical content check, not a new run during publication sanitization.
Containers were removed. Native-next timeout/drop, helper replacement, HTTP
fault injection, and restart recovery were not rerun here. `ask` remained closed.
