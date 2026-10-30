# Operating the PeerTurn relay

The relay stores encrypted packets and routing metadata. It cannot decrypt messages,
interpret grants, launch agents, read client files, or fetch client URLs. A storage
receipt means a committed SQLite write; it does not prove receiver or model input.
Agent adapters, Internet/NAT acceptance, clean VPS qualification, and the 24-hour
128 MiB RSS soak remain separate acceptance work.

Use Go 1.27.3, including for image builds. This includes the
[x509 security update](https://go.dev/doc/devel/release#go1.27.3) that fixes
`GO-2026-6809`. The Docker builder is pinned by digest and asserts its exact Go
version before compiling. Local checks and their limits are in [M2 evidence](m2-evidence.md).

The development [self-service admission candidate](self-service.md) keeps closed
admission by default and proposes a bounded operator opt-in without per-inviter
administrator entries. It is not in the published v0.1.2-preview; do not infer
production readiness or permission to migrate/deploy from that guide.

## Start and isolate

Use Go 1.27.3 and `make build`, or build the image from the repository root:

```sh
docker build -f packaging/docker/Dockerfile -t peerturn-relay:local .
```

Copy [relay.toml](../../packaging/docker/relay.toml), replace the admin PUBLIC
routing key, set `public_authority` to your external hostname and optional port,
and mount a certificate/key obtained for that authority. Config file
arguments may be relative; configured data, TLS, and bootstrap paths are absolute.
Generate and retain routing private keys only in the client/admin's trusted tooling.
There is no shared bearer admin password. Admin routing keys in the local config
authorize mailbox enrollment; they do not authorize reading another mailbox.
The required `public_authority` is one exact canonical authority, for example
`relay.example` or `relay.example:8443`: lowercase, no scheme, path, trailing dot,
or explicit default `:443`. Direct TLS and proxy requests, including notification
tickets, must match it. A signature for another relay authority is rejected.

```sh
peerturn relay --config /etc/peerturn/relay.toml
docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --memory=512m --cpus=1 --pids-limit=128 \
  -p 443:8443 --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  --mount type=volume,src=peerturn-data,dst=/var/lib/peerturn \
  --mount type=bind,src=/etc/peerturn,dst=/etc/peerturn,readonly \
  --mount type=bind,src=/srv/peerturn-tls,dst=/run/secrets,readonly \
  peerturn-relay:local
```

The scratch image runs as UID/GID 65532, includes CA certificates, and has no shell.
Give that UID access to the persistent volume and mounted TLS files. Build your own
image, record its digest, and deploy by digest. Only `/var/lib/peerturn` needs writes.
SIGINT/SIGTERM stops streams and drains HTTP transactions for up to ten seconds.

Alternatively, terminate TLS at Caddy/nginx and remove `tls_cert`/`tls_key`. Set
`trusted_proxies` to the immediate proxy source CIDRs, isolate the HTTP listener
from clients, and have the proxy replace `X-Forwarded-Proto: https`,
`X-Forwarded-Host` with `public_authority`, and `client_ip_header` with verified
client addresses. Supported client headers are `X-Forwarded-For` (default) and
`X-Real-IP`. Preserve path/query bytes and the signature/digest headers.
The authority/protocol headers each require a single unambiguous value.
Only canonical loopback, RFC1918 IPv4 or IPv6 ULA proxy CIDRs are accepted;
public ranges, supernets crossing those boundaries and default routes are rejected. Prefer
the smallest actual proxy subnet. Every host in that subnet can assert client
identity and external transport, so keep untrusted workloads outside it.
Plain HTTP from an untrusted source cannot authenticate, including notify tickets.
Do not forward or log invitation fragments or ticket URLs. Disable access logging
for `/v1/events`; this relay has no request-body or URL access log.

`/healthz`, `/readyz`, and `/metrics` exist only on `private_listen`, which must bind
a literal loopback/private IP. Do not publish that port or proxy these routes.
Private endpoints are unauthenticated operational probes. Public discovery and the
static invitation page are public; every `/v1` request needs a message signature,
except a one-use ticket upgrade limited to notification authority.

## Request and response profile

HTTP Message Signatures follow [RFC 9421](https://www.rfc-editor.org/rfc/rfc9421.html).
Use a single matching signature label and the covered components, in order:
`("@method" "@authority" "@target-uri" "content-digest")`. Required signature
parameters are `created`, `expires`, `nonce`, `keyid`, `alg="ed25519"`. `keyid` is
the canonical unpadded base64url Ed25519 public routing key. `nonce` encodes
16–96 random bytes. `Content-Digest` is `sha-256=:BASE64:` over the actual body,
including an empty body. The signature covers the HTTPS external authority, full
path, query and parameters. Validity is at most 60 seconds with ±60 seconds skew.
Mutating requests commit replay nonces durably for at least two minutes across
restarts, before endpoint effects. GET uses a bounded, expiry-only replay cache
in memory: a restart can accept an otherwise valid GET signature again, but GET
cannot ACK or delete ciphertext custody. Unexpired nonces are never evicted.
Retrying an HTTP operation uses a fresh nonce but preserves the message ID and
encrypted packet.

| API | JSON/profile |
| --- | --- |
| `GET /.well-known/peerturn` | Supported `wire_versions`, public `limits`, `relay_key`, `backup_epoch`; relay key is not E2E peer trust. |
| `GET /i/{id}` | Static HTML, no redemption, no resources/analytics, no-store/no-referrer. |
| `POST /v1/mailboxes` | `{id,read_key,voucher?}`. Configured admin, or winning claimed reader with a scoped voucher. Returns 201 `{mailbox}`. |
| `DELETE /v1/mailboxes/{id}` | Reader only; deletes mailbox custody, permits, tickets and retained tombstones, releasing its metadata budget. |
| `GET /v1/invites/{id}` | Any signer can retrieve `{id,proof_key,packet,expires_at}`. Only the invitation proof-key signer or committed winning reader receives optional `claim_packet`; other signers cannot observe claim status. No plaintext voucher/grant. The inviter verifies the encrypted claim locally. |
| `POST /v1/invites/{id}` | Configured admin only; `{proof_key,packet,expires_at,voucher}` publishes an M1a encrypted descriptor. Exact retries preserve the immutable slot and claim/enrollment state. Added by M2.5; no public anonymous issuance. |
| `POST /v1/invites/{id}/claims` | M1a `invite.ClaimPacket` with JWS invitation-key proof bound to the request signer's `reader_key`. Atomic single winner, immutable same-claim retry. Returns `{claim_id,stage:"relay_stored"}`. |
| `PUT /v1/mailboxes/{id}/permits/{permit}` | Owner signature; `{writer_key,epoch,expires_at}`. Higher epochs replace authority; identical retries are idempotent. |
| `DELETE /v1/mailboxes/{id}/permits/{permit}` | Owner signature; `{epoch}`. Keep the revocation tombstone. |
| `POST /v1/mailboxes/{id}/messages` | M1a `protocol.EncryptedPacket` directly. Returns `{stage:"relay_stored",mailbox,message_id,seq}` only after commit. |
| `GET /v1/mailboxes/{id}/messages` | Reader signature; `after=0&limit=100&wait=0`. `wait` is optional 0–25 seconds. Returns `{messages:[{seq,packet? ,tombstone?}]}`. Pages also cap packet bytes at 1 MiB. |
| `POST /v1/mailboxes/{id}/ack` | Reader signature; `{through}` is the highest contiguous durable seq. Cannot ACK or skip beyond the contiguous range previously issued to that reader. |
| `POST /v1/streams/tickets` | `{mailbox}`. Mailbox reader only. Returns 201 `{ticket,expires_at}`; single upgrade within 60 seconds, no content access. |
| `GET /v1/events` | Reader-signed WSS upgrade `?mailbox=ID`, or reader-minted `?ticket=TOKEN`. Only `{mailbox,high_seq}`, never packets. Writer permits do not authorize mailbox traffic notifications. |

Public mailbox/permit IDs are bounded opaque tokens; message IDs are 128-bit
lowercase hex. Invitation/claim IDs retain the M1a 16-byte base64url profile.
The maximum safe JSON epoch is reserved for terminal revoke. JSON rejects unknown
and duplicate fields. Ciphertext is at most 96 KiB; JSON body
default 135168 bytes includes base64/header overhead. Unsupported wire majors
return 426. Other stable errors: 400 schema/cursor, 401 signature/replay, 403 access,
409 conflicting ID/epoch, 410 expired/revoked/consumed invitation, 413 body size,
429 quota/rate/stream count, 503 storage or backpressure. 429/503 carry Retry-After.
Unknown routes return 404; unsupported HTTP methods return 405.

Reader ACK deletes ciphertext custody while preserving seq/digest tombstones.
Expiry produces tombstones so a cursor can cross gaps. Duplicate writer-key plus
message-ID and identical canonical encrypted packet returns the original receipt,
including after revoke; a new message still requires a live permit. Another digest
returns 409 while the dedup row is retained, including after ACK. ACKed dedup rows
remain until message expiry plus one hour, at most seven days plus that grace;
clients must never reuse message IDs. Unacked tombstones continue to count against
the mailbox item cap. New messages require at least one minute of remaining TTL
at wire-second precision and at most seven days.
Notification streams use per-mailbox wake channels and one-second write deadlines;
they require the reader key when opened, including ticket upgrades. There is no
writer notification lane. Revoking a route permit prevents new writer submissions;
it does not revoke E2E grants or undo previously admitted agent effects.

## Local invitation provisioning

M2.5 adds admin-signed invitation creation for the [installed client](../m25.md).
Invitation revocation remains local provisioning; there is no public revocation endpoint.
A trusted issuer can also prepare an absolute `bootstrap_file` with public mailboxes and M1a
encrypted descriptors/proof PUBLIC keys. Restart with that file to apply it:

```json
{
  "mailboxes": [{"id":"opaque-box","read_key":"PUBLIC_ROUTING_KEY"}],
  "invites": [{
    "id":"BASE64URL_16_BYTES",
    "proof_key":"PUBLIC_INVITE_PROOF_KEY",
    "packet":{"wire_version":1,"invite_id":"BASE64URL_16_BYTES",
              "enc":"BASE64_32_BYTES","ciphertext":"BASE64_CIPHERTEXT"},
    "expires_at":1800000000,
    "voucher":"RANDOM_ENROLLMENT_VOUCHER_AT_LEAST_32_CHARS"
  }],
  "revoked_invites":[]
}
```

These are placeholders, not usable keys or ciphertext. Provision invitations using
the M1a client primitives; never put scopes, transcripts, native paths, client
private keys, or URL fragment secrets in this file. The voucher is a local enrollment
capability that the issuer also includes inside the encrypted descriptor. Protect
the file with mode 0600; SQLite retains only its hash. Exact provisioning retries
preserve claim/enrollment/revocation state. Add an ID to `revoked_invites` and restart
to revoke it; already-issued grants remain the receiver's responsibility.

## Capacity, recovery and backup

The public listener accepts at most 512 connections before TLS negotiation.
Each direct source also caps at `requests_per_ip` sockets (default 32), with IPv6
aggregation applied before TLS. Each immediate trusted proxy caps at
`max(1, max_connections/2)` sockets (default 256), because client headers are not
available yet. Excess sockets close before TLS while other sources remain served.
Per-source admission is limited to `requests_per_ip` requests (default 32) before
body reads; bodies have a separate five-second deadline. After body reads, each
source can occupy at most one eighth of global execution capacity, with a minimum
of one slot: currently 8 of the relay's fixed 64 slots. This execution cap is
independent of the configurable socket and admission allowance, so two busy
sources cannot exhaust execution capacity.
Rate limits apply independently to source addresses and verified routing
keys; defaults are 600/minute with burst 120. IPv6 sources share a /64 bucket by
default; `ipv6_prefix_bits` supports 48–128. Forwarded client addresses are used
only from an immediate trusted proxy. `X-Forwarded-For` is evaluated from the
right, skipping trusted proxy hops; `X-Real-IP` must contain one address.

The defaults budget for about ten daemons behind one office NAT, assuming one
mailbox per daemon. Allow one notification stream, one fetch/long-poll connection
and one spare or idle connection each: 30 sockets fit under 32. The daemon's
healthy ten-second sync costs about 6 fetches/minute; pending onboarding uses a
two-second interval, or 30 polls/minute each. Ten such polling lanes consume 300
of the 600 requests/minute, leaving 300 for onboarding operations, sends, ACKs
and reconnects. A 25-second long poll instead costs about 2.4 requests/minute per
lane; WebSocket ping frames do not consume HTTP request tokens. Burst 120 allows
12 initial HTTP requests per daemon when ten reconnect together. This is a
planning budget, not a guarantee for busy peers, multiple pending invitations or
rapid reconnects: measure traffic and set explicit limits for larger NAT groups.
Existing explicit configuration values remain in effect until an operator edits
them. The rate bound also applies to each verified key; per-key/mailbox poll and
stream caps remain unchanged.

Long polls are limited to 4 per key and 4 per mailbox, with 32 globally; waiting
releases the source admission lease and global execution slot. Admitted WebSocket
notification streams also release those leases. HTTP 429 closes the connection.
Streams are limited to 4 per key and mailbox,
256 globally. Only changes to the affected mailbox wake its polls/streams; there
is no periodic mailbox polling. These defaults are in the example config.
The anonymous enrollment/invitation lane has a separate global bucket (burst 16,
refill 2/second) before durable writes. Normal, control and anonymous durable nonce
budgets use separate hashed key namespaces, so anonymous keys cannot fill the
owner-control nonce budget. Pure reads do not write a durable nonce or force a replay fsync.

Defaults: 1 GiB physical database/WAL budget (`total_bytes`), 1000 unacked items
and 32 MiB ciphertext per mailbox, and 100000 metadata records (`metadata_items`).
The physical budget is not a ciphertext allowance. With integer division:

```text
page_cap = (total_bytes - 65536) / (2 * (4096 + 24))
custody_budget = page_cap * 4096 / 2 - 65536
```

Custody admission charges packet bytes plus 4096 bytes per metadata row, reserving
the rest for replay/control updates and worst-case WAL. The default custody budget
is about 254 MiB before row charges. Configured custody pressure returns 429 before
the physical page cap; controlled full-budget tests preserve signed Fetch/ACK/revoke.
Actual host ENOSPC or I/O failure can still make storage unavailable (503).

Effective metadata capacity is `N = min(metadata_items, max(1, custody_budget/4096))`.
Metadata limits isolate mailboxes, readers and writers: mailbox rows are at
most `max(2, N/8)`, reader/tenant rows `max(3, N/4)`, and writer message rows
`max(1, N/8)`, including retained tombstones.
Permits, including unknown-ID revoke tombstones, are additionally capped at
`min(32, max(1, N/16))` per mailbox. Tickets retain at most 8 rows per mailbox until
their 60-second expiry, including consumed tickets. Combined packet-byte and row
charges, including the proposed new row, cap each tenant at `custody_budget/4`
and each mailbox or writer at `custody_budget/8`, including at the minimum physical
budget; a packet below the configured size ceiling can still exceed these
combined limits on a small database and receive 429. Tenant accounting includes
mailboxes, permits, messages and tickets; mailbox accounting includes permits,
messages and tickets; writers include all their retained messages across mailboxes.
Configured mailbox byte limits still apply. There is no silent ciphertext eviction.

Normal and control durable nonce pools each cap at
`min(metadata_items, page_cap*4096/(8*512))`; anonymous caps at
`max(1, min(128, normal_cap/16))`. Per-key caps are
`min(256, lane_cap, max(3, lane_cap/8))`, additionally at most 8 for anonymous keys.
Cross-lane lookup rejects replay regardless of the current lane.
Expiry cleanup commits separately from rejected writes and runs when due on writes
or the minute maintenance tick. Pure reads avoid a custody commit if their issued
frontier does not advance. SQLite uses WAL, synchronous FULL, PASSIVE checkpoints
and TRUNCATE every 64 committed mutations. No failed write yields a storage receipt.
Do not run multiple relay instances against one volume. Reserve filesystem space
for provisioning files, TLS files, snapshots and operator backups separately.

Messages are retained offline through their signed expiry. Reconnect by fetching
from the receiver's last durable ACK; WSS is only a wake hint. Clients must persist
their inbox before ACK. An ambiguous HTTP outcome is not proof of loss or model
input: retry identical ciphertext/ID with a new HTTP signature, never blindly
repeat a native agent operation.

For a consistent online backup use the SQLite backup API, for example the operator
`sqlite3` tool's `.backup /secure-backups/relay.sqlite` command against the live DB.
Do not copy a live DB file without its WAL. Alternatively stop the relay cleanly
and copy the whole data directory. Back up `relay-signing.key` securely as part of
relay identity; never include client private keys. Restore only with the relay
stopped, into a UID-65532 writable volume, then verify `/readyz` and ciphertext
fetch. A backup may precede revocations: re-enroll routing authorities/permits before
admitting peers after restore. The relay cannot detect external rollback of its
entire disk; the persisted backup epoch alone is not a rollback oracle. Daily
restore drills and real Linux amd64/arm64 self-host/NAT/TLS tests belong to M7.

Relay-terminated TLS certificates are loaded at startup; restart after renewal. Detailed startup
diagnostics and security-event logs remain deferred because their operator-facing
interface needs separate design; aggregate metrics are not an audit trail.

Dependencies are pinned: [modernc SQLite](https://pkg.go.dev/modernc.org/sqlite@v1.61.0)
is pure Go (shared choice with M1b), [Coder WebSocket](https://pkg.go.dev/github.com/coder/websocket@v1.8.15)
is maintained and supports context deadlines, read limits and notify-only closure;
[httpsfv](https://pkg.go.dev/github.com/dunglas/httpsfv@v1.1.2) parses standardized
structured fields, and [go-toml](https://pkg.go.dev/github.com/pelletier/go-toml/v2@v2.4.3)
provides strict TOML decoding. M1b had no SQLite dependency when this pin was chosen.
