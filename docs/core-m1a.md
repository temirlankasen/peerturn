# M1a pure protocol core

M1a implements in-memory protocol decisions. The executable still exposes only help/version. There is no network, storage, native agent submission, installation, or release in this milestone. Entropy comes from `crypto/rand`; clocks, pins, certificates, current epochs, replay records, and budgets are explicit caller inputs.

| Package | Responsibility |
| --- | --- |
| `internal/codec` | Bounded, strict JSON with duplicate-key and exact schema-name checks; canonical JSON subset. |
| `internal/fault` | Typed errors with stable codes and no untrusted diagnostic text. |
| `internal/crypto` | Standard-library HPKE and Ed25519 JWS over exact bytes. |
| `internal/protocol` | Routing, encrypted packets, messages, receipts, version and size checks. |
| `internal/identity` | Pinned root → device → session chains and cross-signed root rotation evidence. |
| `internal/grant` | Recipient-issued directional authority and exact epochs, expiry, limits, scope policy. |
| `internal/invite` | Encrypted descriptors, signed/encrypted claims, fragment links, immutable consume decisions. |
| `internal/envelope` | Seal/open composition and pure replay, chain and budget decisions. |

## Cryptographic and wire decisions

The HPKE profile is RFC 9180 Base mode, DHKEM(X25519, HKDF-SHA256)/HKDF-SHA256/ChaCha20Poly1305 (`0x0020/0x0001/0x0003`). Production uses Go 1.27.2 `crypto/hpke`. Every seal/open creates a fresh single-message context. Typed contexts select distinct `info` strings: `peerturn/v1/envelope`, `peerturn/v1/invite-descriptor`, `peerturn/v1/invite-claim`. A retry retains the original encapsulation and ciphertext.

`RoutingHeader.suite` serializes those IDs as `0020-0001-0003`. `wire_version=1`, `feature_bits=0`; an unknown major, suite, or feature bit is rejected. All routing fields form canonical AAD. The inner JWS payload is `{header,message}`, binding the same destination and routing metadata to the sender signature. The caller supplies the sender's pinned chain and recipient-issued grant from local state; `kid` never triggers fetching or discovery.

JWS Compact uses only protected `alg=EdDSA`, a fixed object-specific `typ`, and `kid=SHA256(signing_public_key)`. Message typ is `peerturn-message-v1`; each certificate, grant, rotation, descriptor, claim, and invitation proof has its own fixed type. Signature verification authenticates the exact original bytes before signed JSON is decoded. Duplicate keys, unknown fields, case variants, `none`, remote-key headers and `crit` are rejected.

Ed25519 keys must use canonical field encodings and exclude small-order points, including negative-zero aliases. Certificate policy caps device validity at 30 days, session validity at 24 hours plus five minutes of grace, and cross-signed root-rotation statements at three days. Child and grant windows still fit their verified parents; these caps do not establish a revocation authority or rotate keys automatically.

The RFC 8785 subset contains null, booleans, valid Unicode strings, arrays, objects, and integers in ±(2^53−1). Object keys sort by UTF-16 code units; strings follow canonical JSON escapes without HTML escaping. Floating point, exponent-form numbers and invalid Unicode are rejected. This subset covers canonical routing headers and authority objects. `sender_seq` retains the full `uint64` domain in exactly signed message JSON; it is not passed through canonical floating-point conversion. A channel at sequence exhaustion must rotate rather than wrap.

Plaintext means the complete decrypted compact JWS and is limited to 64 KiB. Packet ciphertext is bounded at 96 KiB, and packet JSON is bounded before allocation for base64 expansion; the envelope profile further rejects a ciphertext that could exceed the 64 KiB plaintext bound. Body strings are UTF-8 data: paths, URLs, and control-looking text are never expanded or executed.

## Authority and routing policy

Grants carry exact issuer and subject identities, devices, sessions, signing keys, continuity/key epochs, a separate grant epoch, explicit validity, limits and optional exact reply route. The issuer is the receiving session. The sender cannot issue or widen a recipient's grant. Root/device/session certificate windows bound the grant window.

The scope ceiling comes only from the verified recipient-issued grant. `envelope.decideRouting` is the sole dispatcher: permitted `coauthor` selects `live`, `ask` selects `responder`, and `inform` selects `inbox_no_wake`. A stronger grant can request a less powerful intent; body text cannot change either scope or route. Replies require an existing exact conversation and reply route, and select `inbox_no_wake` to avoid automatic reply loops. Receipts select `control`. Duplicates and parked gaps have no execution route.

`coauthor` is strong trust and has the receiving agent's normal capabilities. It is not a sandbox, and peer text can influence later owner turns. M1a returns routing decisions only: it implements no live adapter, fork runtime, protected-path preflight, outbound redactor, or wake mechanism. Runtime qualification remains required before an adapter can honor a responder decision. Owner authority and permission mode are unchanged.

`envelope.Open` checks packet/schema/bound route, HPKE, pinned sender chain and signature, strict signed schema, recipient chain/key, exact audience and epochs, live grant/expiry/scope, replay, then conversation and budget decisions. Receipt bodies use the receipt schema and never request a model turn. `key_update`, `grant_update`, and `revoke` remain wire kinds but are rejected by the model-message pipeline: their authority must use dedicated typed identity/grant operations rather than interpreting peer body text.

## Invitation and state boundaries

Each invitation independently generates a random Ed25519 seed (`ik`) and X25519 private key (`ek`). The link is HTTPS `/i/<128-bit-id>#v=1&ik=...&ek=...&fp=...&mh=...`; no secret is accepted in the query. `mh` hashes the canonical encrypted descriptor packet, including encapsulation. `FetchURL` strips the fragment without fetching. Secrets live behind an unexported pointer boundary and redact formatting/logging, including unexported enclosing fields; generic JSON/text serialization fails. Only `SecretURL` explicitly exports the full URL. A full pasted URL remains visible to the model provider and transcript, and a stolen live link can be claimed first.

The descriptor authenticates both proposed scopes, the inviter chain, exact relay origin, a maximum ten-minute single-use policy, a separately trusted publisher-manifest hash, and a bounded enrollment voucher. The claim carries the joining session chain, a reverse recipient-issued grant, requested scope, exact audience, claim ID and routing keys. It is session-signed inside HPKE to the inviter. The invitation-key proof covers invite ID, claim ID, encrypted packet hash and expiry. `OpenClaim` verifies that proof before deriving a first-contact claimant fingerprint from the introduced chain; a supplied pin must still match. The returned verified claimant fingerprint identifies a link holder, not a human identity established out of band.

`State.Consume` is an immutable transition scoped to one invitation ID. The identical claim ID/digest returns the retained result; another claim or changed digest conflicts. Invite revoke blocks new claims and does not revoke already issued grants. Calling Consume on two old snapshots does not serialize them. M1b must persist the authoritative compare/consume and grant result atomically; no M1a test claims to prove concurrent transactions.

`ClaimRef` verifies the invitation proof and ciphertext binding without a current-time check. It lets a lost-ACK retry find an already retained consume result after invite/claim expiry. It cannot authorize a new claim: a cache miss must pass current-time `OpenClaim` and authoritative consume checks.

Replay snapshots belong to one exact directional session channel. Equal ID/equal packet digest returns duplicate, equal ID/different packet digest conflicts, and stale or reused sequence numbers return `replay`. Future sequences return `sequence_gap` plus verified ID/digest/sequence/expiry and exact sender/recipient bindings, without plaintext or a dispatch route. At most 64 future positions and 64 pending packets per channel are parkable; overflow returns `budget_exceeded`. Repeated pending ID/digest uses the same slot; conflicting IDs/digests or sequence reuse cannot grow it. Custody must persist ciphertext and the pending snapshot atomically, enforce aggregate channel/disk quotas, and expire pending ciphertext. Filling a gap reruns authority, conversation and budget checks before dispatch; an absent future parent does not prevent parking. Re-encrypting the same signed message changes its packet digest and conflicts under the existing ID. Changing the outer ID cannot reuse the signed header.

Open returns a proposed `Admission`, never a storage receipt, adapter acceptance, model input or turn completion. M1b must atomically commit replay records, budgets, inbox and receipt outbox, recheck live epochs before dispatch, and retain records for the replay horizon. The caller authenticates/binds transport principals and permits, supplies current device/session chains and pins, looks up every incoming conversation ID even on root messages, and serializes authoritative state updates. Reusing an existing conversation as a new root is forbidden. `WantsModel` is derived from the proposed live/responder route and does not authorize native effects or prove delivery. There is no exactly-once-effect guarantee.

## Evidence and remaining acceptance

Published RFC 9180 Appendix A.2.1 ciphertext is decrypted with the exact suite and standard-library recipient context. RFC 8037 Appendix A Ed25519 JWS is reproduced byte for byte using the production signing primitive; its bare RFC header is intentionally rejected by the stricter PeerTurn profile. Sources and public test-key provenance are recorded with the fixtures in [api/fixtures/v1](../api/fixtures/v1/).

Golden routing, message, receipt, packet, and invitation wire fixtures are accompanied by strict decoder, signature/AAD-field tamper, chain/rotation, directional grant, scope, expiry, replay/conflict, invitation/publisher, and pure single-use tests. The all-zero `packet.json` is a schema fixture only and must fail cryptographic admission. Valid cryptographic behavior is covered by published vectors and seal/open tests.

Bounded fuzz targets cover every decoder and the composition boundaries. [M1a fix evidence](core-m1a-evidence.md) records reproduction commands, exits, review and limitations. Independent live interoperability, authoritative storage/crash/race behavior, relay principal binding, responder qualification, real onboarding, OS key custody and the remaining M1–M7 acceptance scenarios are separate work. No forward secrecy or post-compromise security is claimed.
