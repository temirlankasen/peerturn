# M1b: client durable store

`internal/store` implements local custody only. No transport, native adapter,
responder launch, OS keychain, or exactly-once model effects are implemented here.
The owner approved the initial client SQLite schema for this milestone; the SQL
stays within the logical client records in [design §9](spec/design.md).

## SQLite and migrations

The pinned driver is `modernc.org/sqlite v1.61.0`, a maintained BSD-3-Clause,
pure-Go translation of SQLite. It works with `CGO_ENABLED=0`; no native library
is loaded. Upstream publishes [release notes](https://github.com/modernc-org/sqlite/blob/master/CHANGELOG.md),
[security guidance](https://github.com/modernc-org/sqlite/security), and
[driver documentation](https://pkg.go.dev/modernc.org/sqlite@v1.61.0).
The accompanying libc version is the driver's own `v1.79.0` requirement, without
an independent override. `go.sum` pins the transitive module integrity hashes.
The generated SQLite/libc code increases the trusted dependency surface compared
with M1a's stdlib-only core. Upgrade deliberately after reviewing SQLite and
driver advisories, then rerun concurrency, persistence, race and vulnerability
checks. This choice is not a security audit or a promise of future maintenance.

Every connection uses WAL, foreign keys, a 10-second busy timeout,
`synchronous=FULL`, `fullfsync=ON`, `checkpoint_fullfsync=ON`, secure deletion,
and `temp_store=MEMORY` to keep temporary metadata inside process memory.
IMMEDIATE write transactions serialize decisions across handles/processes; the
single connection per handle also avoids read-to-write upgrade deadlocks.
FULL durability still depends on the filesystem/device honoring synchronization.
Embedded numbered SQL migrations and the append-only `schema_version` ledger
commit together. No down migration exists; newer/gapped versions fail closed.
The unreleased initial migration was revised for the review fixes; no shipped
database migration contract exists yet. A pre-fix database fails closed rather
than acquiring a new key or mixing sealing keys.

State/key paths must be canonical absolute paths without symlink ancestors.
The containing directories must belong to the current user and be private
`0700`; DB/key files must be user-owned regular `0600` files. Writable nonsticky
ancestors are rejected. On macOS this requires canonical `/private/...` paths
instead of `/tmp` or `/var` aliases. SQLite creates auxiliary files in the private
directory. No path, SQL text, body or driver diagnostic enters public errors.

## Tables

| Table | Purpose and confidentiality |
| --- | --- |
| `schema_version` | Applied forward-only versions; no automatic downgrade. |
| `key_check` | Sealed fixed check value verified before admitting work; binds the database to its selected external key. |
| `identities` | Explicitly installed root fingerprints and public keys. |
| `devices` | Verified root-signed certificates, keyed by exact certificate digest. |
| `sessions` | Exact binding digest, verified public chain, receiver-owned session turn window/count. |
| `bindings` | Explicit native execution context sealed with record AAD; no cwd discovery or automatic binding. |
| `grants` | Sealed signed grant, exact issuer/subject references, hash/epoch/expiry, revocation/lease generation, rate/burst counters and clock floor. |
| `invites` | Public invite slot, expiry and independent invite-revoked flag. No invitation secret. |
| `invite_claims` | One authoritative winner per invite, exact claim/digest and sealed immutable result for lost-response retries. |
| `outbox` | Immutable sealed wire packet, ciphertext digest, exact directional channel/conversation/budget/hop metadata, data/control lane, storage state, TTL and byte accounting. |
| `inbox` | Validated metadata, sealed original wire packet and body, typed route, independent data/control lane, durable decision, TTL and chain metadata. Parked futures have no plaintext body or dispatch route. |
| `delivery_attempts` | One intent/attempt per message, epoch/lease fence and sealed native result. Native effects are outside SQLite. |
| `receipts` | Highest committed evidence per direction/grant/channel/message; namespace also enters sealed AAD. Includes attempt/stage/rank/terminal flag and local native IDs. |
| `dedup` | Sender records, sparse tombstones, durable sender replay floors, bounded recent mailbox slots and contiguous mailbox frontiers. No body hash. |
| `revocations` | Authoritative grant epoch history. Invite revoke remains separate. |
| `responder_runs` | Reserved logical LocalRun records: exact parent binding/attempt, sealed snapshot/native handles, model/effort/profile/budget/state. No spawn or new responder authority schema/API. |
| `audit` | Monotonic local sequence, time/action, grant/epoch, message/ciphertext digest and bounded reason; actors resolve through exact grant/session references. No body/token/path. |

No budget-reservation schema or paid responder execution is added. Receiver-owned
traffic/model admission counters live in grants/sessions. Cost/provider usage,
resource reservations, snapshots and qualified fork supervision belong to a
separately approved responder implementation.

## Public boundaries

`InstallGrant` verifies M1a pinned chains and recipient-signed grants.
`BindSession` is a trusted adapter/owner operation after binding verification;
changed native context requires a fresh session/epoch. Neither method discovers
or attaches another session.

`OutboxEnqueue` retains the original HPKE packet and returns `sender_stored` only
after commit. Same ID/digest returns its stored stage; different ciphertext or
grant under the same ID conflicts. Retries use `OutboxPacket`, never fresh HPKE.
The caller must construct/authorize outgoing content with M1a and supply the
selected directional grant. `OutboxRequest.Message` is the exact trusted payload
already signed into the packet; the store validates its authority and keeps only
conversation metadata, never its plaintext body. Without the recipient's private
key the sender store cannot independently compare HPKE plaintext to caller data.

`InboxCommit` loads the current grant, replay, conversation and receiver budget
inside the write transaction, then calls M1a's custody admission path.
`LookupConversation` resolves the authenticated conversation ID within that
transaction, including exact-peer outgoing parents. Caller-supplied snapshots
cannot replace committed authority or counters. Routing remains M1a's
`live` / `responder` / `inbox_no_wake` / `control` decision.

The same transaction retains ciphertext, sealed body, replay evidence, grant
routing decision, budgets, and signed E2E receipt outbox before mailbox custody
is exposed. `ReceiptBuilder` is a trusted, side-effect-free signer/HPKE boundary:
it must use the receiver's verified signing key and the peer's selected return
route. The store rejects non-packet output and rolls back if the builder fails;
it does not replace M1a cryptography. Control receipts never create a response
or wake a model. Inbound receipt processing belongs to the custody transaction:
the authenticated reverse grant must match the exact original outbound recipient
and channel. `RecordReceipt` is reserved for trusted local evidence about an
existing row. An inbound/outbound ID collision returns `conflict` in either order.

Authenticated `sequence_gap` futures retain original ciphertext only, bounded by
M1a's 64-item/64-sequence window. Custody may ACK them while dispatch is parked.
After a gap fills, `RecheckParked` revalidates local original packets in sequence;
it needs no relay resend and never submits to a runtime. TTL expiry seals empty
body tombstones, preserves sequence evidence and cannot skip unknown mailbox
sequences. ACK is the highest contiguous durable mailbox sequence, independent
of sender dispatch order. Mailbox lookahead is bounded to 4096. Normal custody
keeps at most 4096 recent contiguous slots plus 4096 sparse slots; `Expire`
folds all contiguous slots into the frontier. Relay-only duplicates and garbage
cannot accumulate lifetime custody records. A compacted retry may report
`custody_committed`: transport custody alone, without a new message admission.

`OpenForCustody` preserves live admission's rejection of expired messages, while
authenticating a first-seen expired packet at the last second of its signed TTL.
Current authoritative epochs still apply. It returns only replay/expiry/kind
metadata with an `expired` error, never body/routing or permission to dispatch.
The store commits an empty tombstone and local terminal evidence, advancing
known sender/mailbox sequences using a separately bounded admission bucket. Invalid
signatures never create authenticated sender-sequence tombstones. An expired
packet or a control does not generate a fresh reply/receipt storm.

After a terminal validation failure, trusted ingress may call `RejectMailbox` to
commit a bounded transport-only tombstone and audit reason. It never trusts an
unverified sender sequence or emits a reply. Durable storage failures and
`sequence_gap` cannot be acknowledged as terminal decisions. Authenticated
scope/rate/queue refusals become `Custody{Stage:rejected,Reason,GrantID}` within
`InboxCommit`; only consumed sequences within the bounded sender window enter
replay evidence. `sequence_window_exceeded` commits rejected transport custody
without skipping a missing sender sequence. `Rejections` reports a bounded per-grant
aggregate with count, reason, last message ID and ciphertext digest; unverified
transport rejections share one aggregate. This is attribution, not full history.

`ConsumeInvite` is called only after M1a claim verification. It serializes M1a's
single-use decision with the sealed response and audit record. Exactly one claim
wins; an identical authenticated retry returns the retained result even after
expiry/revoke. It does not authenticate an unverified claim by its IDs alone.

`DeliveryIntent` rechecks grant/TTL/binding and durably records an attempt before
a native call. Only the first transaction returns `Start=true`; retries return
`Start=false`, including unresolved intents. `DeliveryResult` stores bounded
native evidence; a repeated result must agree. `RecoverInDoubt` fences unresolved
intents before restarting an adapter, after stopping its predecessor. It never
retries model work. A queue ID/exit0 is not model input or turn completion.

`GrantRevoke` serializes with enqueue/admission/result, bumps the epoch and lease
generation, quarantines pending inbox, fences unresolved attempts, requests
cancellation of recorded runs, stops pending data outbox and writes revocation
plus scoped terminal receipts, signed receipt notifications and an audit for
every affected message in the same transaction. A receipt builder is required
when pending inbound data is affected; builder failure rolls back the revoke.
A separate bounded terminal-control reserve permits revoke despite a full
normal control backlog. Both the original and current epoch are idempotent retries.
It cannot undo already-admitted coauthor effects or kill a process itself.
Remote lease timing and real cancellation require later integration.

Receipts advance by an explicit stage order and freeze terminal decisions.
`in_doubt` can be replaced only by `ReconcileReceipt` with exact attempt and
`native_reconciliation` evidence; it never authorizes another submission or
removes a grant's revocation/quarantine. Live and responder stages remain distinct.
`RecordReceipt` is a trusted evidence boundary, not permission for peer text to
assert local events. Native IDs remain local/sealed; peer receipts are sanitized.

## Limits, retention and residuals

The signed baseline is 10 data admissions/minute/grant, burst 5 (one refill per
six seconds), 20 model admissions/ten minutes/receiving session, and hop 8.
New conversations do not reset counters. Admission charges model capacity
conservatively before actual native work. Clock rollback fails closed for data.
Authenticated controls and already-expired admissions share a separate per-grant
bucket with the signed burst/message limits and a clock floor. Exhaustion creates
a bounded rejection instead of parking a relay slot. Duplicate custody is not
charged again. Controls retain their independent lane when data limits are full.

Each grant/direction has data backlog 256 packets/8 MiB and control backlog
128 packets/1 MiB. Retention is capped at 65,536 data records/64 MiB and 8,192
control records/8 MiB per queue; exceeding a ceiling returns typed backpressure.
Owner revocation notifications have a separate per-grant reserve capped at
65,536 records/64 MiB; ordinary admissions cannot spend it.
Byte limits account original wire bytes; sealed blobs/SQLite indexes add overhead.
Caps are explicit storage baselines, not context truncation or silent eviction.

The metadata horizon is `max(expires_at + 7 days, created_at + 14 days)`.
`Expire` wipes sealed bodies/packets and clears byte accounting at TTL in every
state. Finished inbox/outbox/attempt/run/receipt rows are deleted after the horizon;
sender replay records below the contiguous floor are then collected. Sparse
consumed tombstones remain until their missing predecessors are resolved.
Intent/in_doubt metadata and native evidence stay for explicit reconciliation,
while their peer bodies/packets are minimized at TTL. Budgets count retained live
rows/bytes, so collection restores capacity. Audit rows are stamped and purged
using the independent store clock, with 30-day retention.
No forensic immutability or backup rollback defense
is claimed against an attacker with the user's uid or state-directory access.

Default blobs use AES-256-GCM, fresh crypto/rand nonces, and record-specific AAD.
`CreateFileSealer` writes and syncs a private temporary key, publishes it with an
atomic no-replace hard link, and syncs the directory. Explicit chmod tolerates
strict umasks. `OpenFileSealer` only opens existing keys; a missing key fails.
`Open` verifies the sealed key check before any custody operation. There is no
plaintext fallback; losing the key loses content. The key file is file-permissions mode, not an
OS keychain/passphrase or protection against same-uid compromise. Native runtime
transcripts/provider storage are outside this at-rest guarantee.

## Evidence

Tests include 64 competing invite claims, 64 duplicate inbox commits, independent
SQLite handles, lost-response retries, conflicting IDs/results, gap revalidation,
mailbox frontier holes/conflicts, TTL tombstones, revoke rollback, intent recovery,
and aborts between custody/receipt/commit steps. Migration reopen/newer refusal,
PRAGMA durability and DB/WAL/SHM scans have positive plaintext controls.
This is local transactional acceptance, not live relay/native crash qualification.
Commands, red/green cases and final gate exits are in [M1b evidence](store-m1b-evidence.md).
Review-fix evidence is recorded separately in [M1b fixes](store-m1b-fix-evidence.md).
