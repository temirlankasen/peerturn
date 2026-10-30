package store

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func migrationOpen(t *testing.T, path string) *Store {
	t.Helper()
	return migrationOpenWithLimits(t, path, Limits{})
}

func migrationOpenWithLimits(t *testing.T, path string, limits Limits) *Store {
	t.Helper()
	s, err := Open(path, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return s
}

func TestRelaySchemaV2NewDatabaseAndReopenPreserveAdmissionState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.db")
	s := migrationOpen(t, path)
	migrationInt(t, s.db, `PRAGMA user_version`, 2)
	key := strings.Repeat("A", 43)
	created := int64(1_800_000_000_000_000_000)
	migrationExec(t, s.db, `INSERT INTO relay_inviters(routing_key,revoked) VALUES(?,1)`, key)
	migrationExec(t, s.db, `INSERT INTO invites(id,proof_key,packet,digest,expires_ns,inviter_key,created_ns) VALUES('retained','proof',NULL,?,?,?,?)`, migrationDigest("descriptor"), created+int64(time.Minute), key, created)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = migrationOpen(t, path)
	migrationInt(t, s.db, `PRAGMA user_version`, 2)
	migrationInt(t, s.db, `SELECT revoked FROM relay_inviters`, 1)
	var gotKey string
	var gotCreated int64
	if err := s.db.QueryRowContext(t.Context(), `SELECT inviter_key,created_ns FROM invites WHERE id='retained'`).Scan(&gotKey, &gotCreated); err != nil {
		t.Fatal(err)
	}
	if gotKey != key || gotCreated != created {
		t.Fatalf("retained ownership changed: %q, %d", gotKey, gotCreated)
	}
}

func TestRelaySchemaV2UpgradePreservesLegacyCustodyAndAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	now, before := migrationSeedLegacy(t, path)
	s := migrationOpen(t, path)
	s.SetClock(func() time.Time { return now })
	migrationInt(t, s.db, `PRAGMA user_version`, 2)
	if after := migrationSnapshot(t, s.db); !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed legacy records: before=%v, after=%v", before, after)
	}
	migrationInt(t, s.db, `SELECT COUNT(*) FROM invites WHERE inviter_key IS NOT NULL OR created_ns IS NOT NULL`, 0)
	migrationInt(t, s.db, `SELECT COUNT(*) FROM relay_inviters`, 0)
	epoch, err := s.BackupEpoch(t.Context())
	if err != nil || epoch != 7 {
		t.Fatalf("backup epoch: %d, %v", epoch, err)
	}
	batch, err := s.Fetch(t.Context(), "box", "reader", 1, 100, now)
	if err != nil || len(batch) != 1 || batch[0].Seq != 2 || !bytes.Equal(batch[0].Packet, []byte("legacy-pending")) {
		t.Fatalf("legacy pending custody: %+v, %v", batch, err)
	}
	for _, id := range []string{"legacy-acked", "legacy-pending"} {
		receipt, putErr := s.PutMessage(t.Context(), Message{MailboxID: "box", PermitID: "live", PermitEpoch: 5, WriterKey: "writer", ID: id, Packet: []byte(id), ExpiresAt: now.Add(time.Hour)}, now)
		if putErr != nil || !receipt.Duplicate {
			t.Fatalf("legacy dedup %s: %+v, %v", id, receipt, putErr)
		}
	}
	if permitErr := s.CheckPermit(t.Context(), "box", "live", "writer", 5, now); permitErr != nil {
		t.Fatal(permitErr)
	}
	requireStatus(t, s.CheckPermit(t.Context(), "box", "revoked", "writer", 9, now), 403)
	_, err = s.GetInvite(t.Context(), "revoked", now)
	requireStatus(t, err, 410)
	_, err = s.ClaimInvite(t.Context(), "claimed", Claim{ID: "winner", ReaderKey: "joiner", Packet: []byte("legacy-claim")}, now)
	if err != nil {
		t.Fatalf("committed claim retry: %v", err)
	}
	_, err = s.ClaimInvite(t.Context(), "claimed", Claim{ID: "another", ReaderKey: "another", Packet: []byte("other-claim")}, now)
	requireStatus(t, err, 410)
	requireStatus(t, s.CreateMailbox(t.Context(), Mailbox{ID: "extra", ReadKey: "joiner"}, "voucher-claimed"), 403)
	requireStatus(t, s.UseNonce(t.Context(), "writer", "legacy-nonce", now.Add(time.Minute), now), 409)
	_, err = s.ConsumeTicket(t.Context(), "spent-ticket", now)
	requireStatus(t, err, 403)
	if _, ticketErr := s.ConsumeTicket(t.Context(), "legacy-ticket", now); ticketErr != nil {
		t.Fatalf("unspent notification ticket: %v", ticketErr)
	}
	if closeErr := s.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	s = migrationOpen(t, path)
	migrationInt(t, s.db, `PRAGMA user_version`, 2)
	_, err = s.ConsumeTicket(t.Context(), "legacy-ticket", now)
	requireStatus(t, err, 403)
	_, err = s.GetInvite(t.Context(), "revoked", now)
	requireStatus(t, err, 410)
}

func TestRelaySchemaV2OwnershipConstraints(t *testing.T) {
	s := newStore(t, Limits{})
	migrationInt(t, s.db, `PRAGMA user_version`, 2)
	key := strings.Repeat("B", 43)
	migrationExec(t, s.db, `INSERT INTO relay_inviters(routing_key) VALUES(?)`, key)
	for _, test := range []struct {
		name  string
		query string
		args  []any
	}{
		{"invalid_key", `INSERT INTO relay_inviters(routing_key) VALUES('short')`, nil},
		{"invalid_block", `UPDATE relay_inviters SET revoked=2 WHERE routing_key=?`, []any{key}},
		{"unknown_creator", `INSERT INTO invites(id,proof_key,digest,expires_ns,inviter_key,created_ns) VALUES('unknown','proof',X'01',1,?,1)`, []any{strings.Repeat("C", 43)}},
		{"missing_time", `INSERT INTO invites(id,proof_key,digest,expires_ns,inviter_key) VALUES('no-time','proof',X'01',1,?)`, []any{key}},
		{"missing_creator", `INSERT INTO invites(id,proof_key,digest,expires_ns,created_ns) VALUES('no-creator','proof',X'01',1,1)`, nil},
		{"zero_time", `INSERT INTO invites(id,proof_key,digest,expires_ns,inviter_key,created_ns) VALUES('zero','proof',X'01',1,?,0)`, []any{key}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := s.db.ExecContext(t.Context(), test.query, test.args...); err == nil {
				t.Fatal("invalid durable admission state accepted")
			}
		})
	}
	migrationInt(t, s.db, `SELECT revoked FROM relay_inviters`, 0)
	migrationInt(t, s.db, `SELECT COUNT(*) FROM invites`, 0)
}
