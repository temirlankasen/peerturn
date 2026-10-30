package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"net/url"
	"testing"
	"time"
)

// Freeze the shipped v1 format: deriving a legacy fixture from schema would
// silently turn the migration regression into a fresh-database test.
const migrationLegacySchema = `
CREATE TABLE relay_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), backup_epoch INTEGER NOT NULL CHECK(backup_epoch>0)
);
CREATE TABLE mailboxes (
 id TEXT PRIMARY KEY, read_key TEXT NOT NULL, next_seq INTEGER NOT NULL DEFAULT 0,
 ack_seq INTEGER NOT NULL DEFAULT 0, issued_seq INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE permits (
 mailbox_id TEXT NOT NULL REFERENCES mailboxes(id), id TEXT NOT NULL,
 writer_key TEXT NOT NULL, epoch INTEGER NOT NULL, expires_ns INTEGER NOT NULL,
 revoked INTEGER NOT NULL, PRIMARY KEY(mailbox_id,id)
);
CREATE TABLE invites (
 id TEXT PRIMARY KEY, proof_key TEXT NOT NULL, packet BLOB,
 digest BLOB NOT NULL, expires_ns INTEGER NOT NULL, voucher_hash BLOB,
 revoked INTEGER NOT NULL DEFAULT 0,
 claim_id TEXT, claim_reader TEXT, claim_packet BLOB, claim_digest BLOB,
 enrolled_mailbox TEXT
);
CREATE UNIQUE INDEX invite_vouchers ON invites(voucher_hash) WHERE voucher_hash IS NOT NULL;
CREATE TABLE messages (
 mailbox_id TEXT NOT NULL REFERENCES mailboxes(id), seq INTEGER NOT NULL,
 writer_key TEXT NOT NULL, message_id TEXT NOT NULL, permit_id TEXT NOT NULL,
 permit_epoch INTEGER NOT NULL, packet BLOB, digest BLOB NOT NULL,
 expires_ns INTEGER NOT NULL, PRIMARY KEY(mailbox_id,seq), UNIQUE(writer_key,message_id)
);
CREATE INDEX messages_expiry ON messages(expires_ns) WHERE packet IS NOT NULL;
CREATE TABLE nonces (
 key_hash BLOB NOT NULL, nonce_hash BLOB NOT NULL, expires_ns INTEGER NOT NULL,
 PRIMARY KEY(key_hash,nonce_hash)
);
CREATE INDEX nonces_expiry ON nonces(expires_ns);
CREATE TABLE tickets (
 id_hash BLOB PRIMARY KEY, mailbox_id TEXT NOT NULL REFERENCES mailboxes(id),
 reader_key TEXT NOT NULL, permit_id TEXT NOT NULL, writer_key TEXT NOT NULL,
 permit_epoch INTEGER NOT NULL, expires_ns INTEGER NOT NULL, consumed INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX tickets_expiry ON tickets(expires_ns);
PRAGMA user_version=1;
`

var migrationLegacyQueries = map[string]string{
	"relay_state": `SELECT * FROM relay_state ORDER BY singleton`,
	"mailboxes":   `SELECT * FROM mailboxes ORDER BY id`,
	"permits":     `SELECT * FROM permits ORDER BY mailbox_id,id`,
	"invites": `SELECT id,proof_key,packet,digest,expires_ns,voucher_hash,revoked,
 claim_id,claim_reader,claim_packet,claim_digest,enrolled_mailbox FROM invites ORDER BY id`,
	"messages": `SELECT * FROM messages ORDER BY mailbox_id,seq`,
	"nonces":   `SELECT * FROM nonces ORDER BY key_hash,nonce_hash`,
	"tickets":  `SELECT * FROM tickets ORDER BY id_hash`,
}

func migrationSQL(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := (&url.URL{Scheme: "file", Path: path}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func migrationExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("fixture SQL: %v", err)
	}
}

func migrationInt(t *testing.T, db *sql.DB, query string, want int64) {
	t.Helper()
	var got int64
	if err := db.QueryRowContext(t.Context(), query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s: got %d, want %d", query, got, want)
	}
}

func migrationRows(t *testing.T, db *sql.DB, query string) [][]any {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			if blob, ok := value.([]byte); ok {
				values[i] = bytes.Clone(blob)
			}
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func migrationSnapshot(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	result := make(map[string][][]any, len(migrationLegacyQueries))
	for table, query := range migrationLegacyQueries {
		result[table] = migrationRows(t, db, query)
	}
	return result
}

func migrationDigest(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func migrationSeedLegacy(t *testing.T, path string) (time.Time, map[string][][]any) {
	t.Helper()
	now := time.Unix(1_800_000_000, 0).UTC()
	db := migrationSQL(t, path)
	migrationExec(t, db, migrationLegacySchema)
	migrationExec(t, db, `INSERT INTO relay_state VALUES(1,7)`)
	migrationExec(t, db, `INSERT INTO mailboxes VALUES('box','reader',2,1,2),('joined','joiner',0,0,0)`)
	migrationExec(t, db, `INSERT INTO permits VALUES('box','live','writer',5,?,0),('box','revoked','writer',9,?,1)`, now.Add(time.Hour).UnixNano(), now.Add(time.Hour).UnixNano())
	for i, packet := range []string{"legacy-acked", "legacy-pending"} {
		var stored []byte
		if i == 1 {
			stored = []byte(packet)
		}
		migrationExec(t, db, `INSERT INTO messages VALUES('box',?,'writer',?,'live',5,?,?,?)`, i+1, packet, stored, migrationDigest(packet), now.Add(time.Hour).UnixNano())
	}
	for _, id := range []string{"open", "claimed", "revoked"} {
		migrationExec(t, db, `INSERT INTO invites(id,proof_key,packet,digest,expires_ns,voucher_hash) VALUES(?,'legacy-proof',?,?,?,?)`, id, []byte("descriptor-"+id), migrationDigest("descriptor-"+id), now.Add(5*time.Minute).UnixNano(), migrationDigest("voucher-"+id))
	}
	migrationExec(t, db, `UPDATE invites SET claim_id='winner',claim_reader='joiner',claim_packet=?,claim_digest=?,enrolled_mailbox='joined' WHERE id='claimed'`, []byte("legacy-claim"), migrationDigest("legacy-claim"))
	migrationExec(t, db, `UPDATE invites SET revoked=1,packet=NULL WHERE id='revoked'`)
	migrationExec(t, db, `INSERT INTO nonces VALUES(?,?,?)`, migrationDigest("writer"), migrationDigest("legacy-nonce"), now.Add(2*time.Minute).UnixNano())
	for _, consumed := range []bool{false, true} {
		id := "legacy-ticket"
		if consumed {
			id = "spent-ticket"
		}
		migrationExec(t, db, `INSERT INTO tickets VALUES(?,'box','reader','','',0,?,?)`, migrationDigest(id), now.Add(time.Minute).UnixNano(), consumed)
	}
	snapshot := migrationSnapshot(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return now, snapshot
}
