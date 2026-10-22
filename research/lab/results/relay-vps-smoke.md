# Internet relay — synthetic CLI smoke

Run: 21 October 2026, 19:13–19:17 UTC. Preview build `preview-6dd78cc` ran a
public TLS relay on an external Linux VPS. Two isolated Linux clients behind NAT
used explicit CLI test sessions. **Native agent adapters were not involved.**
This is a sanitized historical report; addresses, private deployment configuration,
invites, and run identifiers are omitted.

The relay ran as a dedicated non-root system account with bounded service
resources, a private data directory, and loopback backend listeners. A reverse
proxy terminated publicly trusted ACME TLS and replaced forwarded headers.
Private health/readiness/metrics probes returned 404 through the public proxy.
These are observations of that test deployment, not a current hosted service.

| Smoke control | Observation |
| --- | --- |
| Discovery | Public `/.well-known/peerturn` returned 200. |
| Invite/join | A synthetic coauthor invite was piped to the second client; both reached connected in about three seconds. |
| Forward message | Relay storage receipt; synthetic marker appeared in the recipient inbox after approximately 0.97 seconds. |
| Reverse message | Synthetic reverse delivery succeeded. |
| Relay plaintext marker scan | No files matched the test plaintext markers across the inspected data directory, including SQLite/WAL. |

The marker scan is bounded evidence about those messages and files; it is not a
proof of all possible metadata or plaintext absence. Test labels are not native
binding evidence. The run did not qualify 24-hour operation, the full NAT/offline
matrix, native agent adapters over the Internet, or an independent colleague's
machine. It does not establish exactly-once effects or current service availability.
