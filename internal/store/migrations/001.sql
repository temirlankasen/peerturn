-- Client logical model v1. No down migration: old binaries must refuse newer data.
CREATE TABLE key_check (id INTEGER PRIMARY KEY CHECK(id=1), sealed BLOB NOT NULL);
CREATE TABLE identities (id TEXT PRIMARY KEY, root_public_key BLOB NOT NULL);
CREATE TABLE devices (id TEXT PRIMARY KEY, identity_id TEXT NOT NULL REFERENCES identities(id), certificate TEXT NOT NULL);
CREATE TABLE sessions (
 id TEXT PRIMARY KEY, device_id TEXT NOT NULL REFERENCES devices(id), certificate TEXT NOT NULL,
 turn_window INTEGER NOT NULL DEFAULT 0, turns INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE bindings (session_id TEXT PRIMARY KEY REFERENCES sessions(id), context BLOB NOT NULL, state TEXT NOT NULL);
CREATE TABLE grants (
 id TEXT PRIMARY KEY, issuer TEXT NOT NULL REFERENCES sessions(id), subject TEXT NOT NULL REFERENCES sessions(id),
 signed BLOB NOT NULL, hash TEXT NOT NULL, epoch INTEGER NOT NULL CHECK(epoch>0), expires_at INTEGER NOT NULL,
 revoked INTEGER NOT NULL DEFAULT 0, lease_generation INTEGER NOT NULL DEFAULT 1,
 minute_start INTEGER NOT NULL DEFAULT 0, messages INTEGER NOT NULL DEFAULT 0,
 burst_start INTEGER NOT NULL DEFAULT 0, burst INTEGER NOT NULL DEFAULT 0, clock_floor INTEGER NOT NULL DEFAULT 0,
 control_start INTEGER NOT NULL DEFAULT 0, control_used INTEGER NOT NULL DEFAULT 0,
 control_minute INTEGER NOT NULL DEFAULT 0, control_messages INTEGER NOT NULL DEFAULT 0,
 control_clock INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE invites (id TEXT PRIMARY KEY, expires_at INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE invite_claims (
 invite_id TEXT PRIMARY KEY REFERENCES invites(id), claim_id TEXT NOT NULL, digest TEXT NOT NULL,
 result BLOB NOT NULL, consumed_at INTEGER NOT NULL
);
CREATE TABLE outbox (
 id TEXT PRIMARY KEY, grant_id TEXT NOT NULL REFERENCES grants(id), digest TEXT NOT NULL,
 packet BLOB NOT NULL, lane TEXT NOT NULL CHECK(lane IN ('data','control')), stage TEXT NOT NULL,
 expires_at INTEGER NOT NULL, bytes INTEGER NOT NULL, created_at INTEGER NOT NULL,
 channel TEXT NOT NULL DEFAULT '', conversation_id TEXT NOT NULL DEFAULT '',
 budget_id TEXT NOT NULL DEFAULT '', hop INTEGER NOT NULL DEFAULT 0,
 priority INTEGER NOT NULL DEFAULT 0 CHECK(priority IN (0,1))
);
CREATE TABLE inbox (
 id TEXT PRIMARY KEY, channel TEXT NOT NULL, grant_id TEXT NOT NULL REFERENCES grants(id),
 epoch INTEGER NOT NULL, sender_seq INTEGER NOT NULL, digest TEXT NOT NULL, packet BLOB NOT NULL,
 body BLOB NOT NULL, route TEXT NOT NULL, state TEXT NOT NULL, expires_at INTEGER NOT NULL,
 bytes INTEGER NOT NULL, created_at INTEGER NOT NULL, conversation_id TEXT NOT NULL DEFAULT '',
 parent_id TEXT NOT NULL DEFAULT '', budget_id TEXT NOT NULL DEFAULT '', hop INTEGER NOT NULL DEFAULT 0,
 lane TEXT NOT NULL DEFAULT 'data' CHECK(lane IN ('data','control')),
 mailbox TEXT NOT NULL DEFAULT '', mailbox_seq INTEGER NOT NULL DEFAULT 0,
 UNIQUE(channel,sender_seq)
);
-- Sender replay records and mailbox custody rows share this logical dedup table.
CREATE TABLE dedup (
 key TEXT PRIMARY KEY, channel TEXT, message_id TEXT NOT NULL, digest TEXT NOT NULL,
 sender_seq INTEGER, mailbox TEXT, mailbox_seq INTEGER, state TEXT NOT NULL, retain_until INTEGER NOT NULL,
 UNIQUE(channel,message_id), UNIQUE(channel,sender_seq), UNIQUE(mailbox,mailbox_seq)
);
CREATE TABLE delivery_attempts (
 id TEXT PRIMARY KEY, message_id TEXT NOT NULL UNIQUE REFERENCES inbox(id), grant_epoch INTEGER NOT NULL,
 lease_generation INTEGER NOT NULL, state TEXT NOT NULL, native_result BLOB, created_at INTEGER NOT NULL
);
CREATE TABLE receipts (
 direction TEXT NOT NULL CHECK(direction IN ('inbound','outbound')),
 grant_id TEXT NOT NULL REFERENCES grants(id), channel TEXT NOT NULL, message_id TEXT NOT NULL,
 attempt_id TEXT NOT NULL, stage TEXT NOT NULL, rank INTEGER NOT NULL,
 terminal INTEGER NOT NULL, evidence BLOB NOT NULL, at INTEGER NOT NULL,
 PRIMARY KEY(direction,grant_id,channel,message_id)
);
CREATE TABLE revocations (
 grant_id TEXT NOT NULL REFERENCES grants(id), epoch INTEGER NOT NULL, at INTEGER NOT NULL,
 PRIMARY KEY(grant_id,epoch)
);
CREATE TABLE responder_runs (
 run_id TEXT PRIMARY KEY, message_id TEXT NOT NULL REFERENCES inbox(id), attempt_id TEXT NOT NULL REFERENCES delivery_attempts(id),
 parent_binding TEXT NOT NULL REFERENCES bindings(session_id), snapshot_handle BLOB NOT NULL,
 fork_native_id BLOB, model TEXT NOT NULL, effort TEXT NOT NULL, profile_hash TEXT NOT NULL,
 budget_id TEXT NOT NULL, state TEXT NOT NULL
);
CREATE TABLE audit (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, action TEXT NOT NULL,
 grant_id TEXT NOT NULL, epoch INTEGER NOT NULL, message_id TEXT NOT NULL, digest TEXT NOT NULL, reason TEXT NOT NULL,
 count INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX inbox_pending ON inbox(grant_id,state);
CREATE INDEX dedup_mailbox ON dedup(mailbox,mailbox_seq);
