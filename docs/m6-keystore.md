# Wrapping-key isolation

File-permissions state remains supported and reports
`authority_secrets_isolated=false` with `authority_file_key`. Running
`peerturn keys isolate --state-dir /absolute/private/state` while its daemon is
stopped explicitly migrates it to the Linux OS service. The command refuses a
state lock owned by a running daemon. Normal opens of file mode do
not contact an OS service. A service outage after migration fails closed; there
is no automatic file fallback.

On macOS, `keys isolate` refuses with `macos_unqualified` before state access,
OS key creation or file-key removal. Opening a pending migration also refuses
before keychain access or recovery effects. Host qualification is still missing;
mocked command transfer does not authorize migration. An already active key may
still be read for ordinary coauthor operation; macOS ask remains disabled.

The unqualified macOS backend uses the fixed `/usr/bin/security` executable.
Creating a random item uses one command delivered through a pipe to `security -i`;
the 32-byte random key is
hexadecimal stdin data, never a process argument, environment value or file.
Reads capture bounded stdout, errors are fixed codes, and commands have a
15-second deadline. This relies on `security` as a keychain deputy; it does not
provide a signed-app ACL or protection from arbitrary same-uid processes. No
host/login keychain mutation is part of automated tests.

Linux uses the Secret Service API over the user's D-Bus session connection.
`github.com/godbus/dbus/v5` is pinned to **v5.2.0**, with checksums in `go.sum`.
It is the maintained pure-Go D-Bus implementation; it adds no C toolchain or
subprocess dependency and reuses the existing `golang.org/x/sys` dependency.
The registry tag resolves to upstream commit
`b3631483aaccd8ee46f5373ce93af2b3762e0b29`. The `plain` Secret Service transfer
algorithm uses the local authenticated D-Bus transport rather than custom
cryptography. An existing unlocked default collection is required. The canonical
nonpersistent `session` collection is refused, including when the `default`
alias resolves to it; reads from that collection and unresolved alias paths are
also refused. This rejects the standard volatile collection, but the API does not
attest that an arbitrary provider persists a collection to durable storage. Missing,
locked, duplicate, malformed or prompt-requiring items fail closed; PeerTurn
never unlocks collections or presents a prompt. Existing items are not replaced.

These stores isolate the authority subset only from an admitted Claude fork
whose entire tool surface is file reads. D-Bus and `security` remain accessible
to arbitrary same-uid code. The fork must deny execution, hooks, MCP and sockets;
Codex asks remain disabled until their separate OS channel proofs exist.

Migration generates and reload-verifies a **new** OS key through a fresh service
connection, writes a synced `wrapping.json` pending marker containing only its
random identifier, and atomically reseals every AEAD column in the existing
SQLite schema. Each new
record is decrypted and compared before commit. The journal is atomically
replaced next. Both journal and all retained DB ciphertext are authenticated
with the new key. Immediately before removing the file key, another fresh load
must match the sealer used for that verification. A missing or changed key keeps
the migration pending and leaves the file key intact. Only then is the original
`seal.key` inode overwritten with 32 zero bytes and synced, then unlinked and the
directory synced; the marker then becomes active. No schema or DDL changes are
introduced.

A pending marker supports either old or new ciphertext across interruption
boundaries. Opening recovers the same migration without generating another key.
An active marker plus any `seal.key` path is refused. Missing/corrupt keys,
journal, metadata or ciphertext never cause key replacement. Older snapshots
containing the old key and old ciphertext remain exposed. The overwrite also
clears ordinary hardlink aliases, but secure erasure of old CoW extents,
snapshots and backups cannot be guaranteed. Suspected historical
seed exposure still requires identity revocation/rotation/rebinding.

The Secret Service item is visibly labelled `PeerTurn wrapping key`. Deleting
that item, resetting its keyring, or losing the keyring makes the migrated store
unreadable. PeerTurn cannot reconstruct the key from its identifier and never
generates a replacement for existing ciphertext. Restore the matching OS keyring
and PeerTurn state from a coordinated backup; without that key, establish a new
identity and revoke/rebind the lost identity through the other participants.
Keep the service available to resume a pending migration; do not manually remove
the marker or substitute a file key.

Use a keyring protected by a nonempty password. GNOME Keyring can store an
empty-password keyring unencrypted on disk, exposing its wrapping keys to file
reads. PeerTurn cannot verify the provider's password or at-rest encryption.
Successful creation and fresh-connection reload prove current service access,
not power-loss durability: the Secret Service API exposes no persistence flush
or durability acknowledgement. A power failure after file-key removal can still
lose an unflushed service key permanently. The file and directory syncs protect
PeerTurn's migration records; they cannot force the keyring daemon's storage.

Unit acceptance covers every AEAD column, wrong replacement/commit rollback,
pending/DB/journal/verified/key-overwrite/key-removal interruptions, unavailable backend,
fresh-key identity, pre-wipe key loss, fresh service connections, state-lock
refusal, volatile-collection refusal, macOS pending refusal, stale file refusal
and direct OS initialization. macOS
command transfer and Secret Service response handling use mocks. Live disposable
Linux evidence is recorded separately under `research/lab/results/m6-ask/`.

Sources: [godbus module](https://github.com/godbus/dbus/tree/v5.2.0),
[Secret Service API](https://specifications.freedesktop.org/secret-service/latest/),
[Apple security command implementation](https://github.com/apple-oss-distributions/Security/blob/main/SecurityTool/macOS/security.c).
