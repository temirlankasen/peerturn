package store

const schema = `
CREATE TABLE IF NOT EXISTS relay_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), backup_epoch INTEGER NOT NULL CHECK(backup_epoch>0)
);
INSERT INTO relay_state(singleton,backup_epoch) VALUES(1,1) ON CONFLICT(singleton) DO NOTHING;
CREATE TABLE IF NOT EXISTS mailboxes (
 id TEXT PRIMARY KEY, read_key TEXT NOT NULL, next_seq INTEGER NOT NULL DEFAULT 0,
 ack_seq INTEGER NOT NULL DEFAULT 0, issued_seq INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS permits (
 mailbox_id TEXT NOT NULL REFERENCES mailboxes(id), id TEXT NOT NULL,
 writer_key TEXT NOT NULL, epoch INTEGER NOT NULL, expires_ns INTEGER NOT NULL,
 revoked INTEGER NOT NULL, PRIMARY KEY(mailbox_id,id)
);
CREATE TABLE IF NOT EXISTS invites (
 id TEXT PRIMARY KEY, proof_key TEXT NOT NULL, packet BLOB,
 digest BLOB NOT NULL, expires_ns INTEGER NOT NULL, voucher_hash BLOB,
 revoked INTEGER NOT NULL DEFAULT 0,
 claim_id TEXT, claim_reader TEXT, claim_packet BLOB, claim_digest BLOB,
 enrolled_mailbox TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS invite_vouchers ON invites(voucher_hash)
 WHERE voucher_hash IS NOT NULL;
CREATE TABLE IF NOT EXISTS messages (
 mailbox_id TEXT NOT NULL REFERENCES mailboxes(id), seq INTEGER NOT NULL,
 writer_key TEXT NOT NULL, message_id TEXT NOT NULL, permit_id TEXT NOT NULL,
 permit_epoch INTEGER NOT NULL, packet BLOB, digest BLOB NOT NULL,
 expires_ns INTEGER NOT NULL, PRIMARY KEY(mailbox_id,seq),
 UNIQUE(writer_key,message_id)
);
CREATE INDEX IF NOT EXISTS messages_expiry ON messages(expires_ns) WHERE packet IS NOT NULL;
CREATE TABLE IF NOT EXISTS nonces (
 key_hash BLOB NOT NULL, nonce_hash BLOB NOT NULL, expires_ns INTEGER NOT NULL,
 PRIMARY KEY(key_hash,nonce_hash)
);
CREATE INDEX IF NOT EXISTS nonces_expiry ON nonces(expires_ns);
CREATE TABLE IF NOT EXISTS tickets (
 id_hash BLOB PRIMARY KEY, mailbox_id TEXT NOT NULL REFERENCES mailboxes(id),
 reader_key TEXT NOT NULL, permit_id TEXT NOT NULL, writer_key TEXT NOT NULL,
 permit_epoch INTEGER NOT NULL, expires_ns INTEGER NOT NULL, consumed INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS tickets_expiry ON tickets(expires_ns);
PRAGMA user_version=1;
`

const metadataCount = `SELECT
 (SELECT COUNT(*) FROM mailboxes)+(SELECT COUNT(*) FROM permits)+
 (SELECT COUNT(*) FROM invites)+(SELECT COUNT(*) FROM messages)+
 (SELECT COUNT(*) FROM tickets)`

const storedBytes = `SELECT
 COALESCE((SELECT SUM(LENGTH(packet)) FROM messages),0)+
 COALESCE((SELECT SUM(LENGTH(packet)+COALESCE(LENGTH(claim_packet),0)) FROM invites),0)`
