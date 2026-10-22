package store

import (
	"database/sql"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestExpiredOutboxGrantsReportsOnlyDistinctExpiredSenderCustody(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	first, second := outgoingRequest(t, f, 1), outgoingRequest(t, f, 2)
	for _, r := range []OutboxRequest{first, second} {
		if _, err := s.OutboxEnqueue(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	if items, err := s.ExpiredOutboxGrants(t.Context(), testNow); err != nil || len(items) != 0 {
		t.Fatal("live pending packet reported expired", items, err)
	}
	// This API uses metadata only; corrupt packet bytes must not be opened.
	if _, err := s.db.ExecContext(t.Context(), `UPDATE outbox SET packet=X'00'`); err != nil {
		t.Fatal(err)
	}
	items, err := s.ExpiredOutboxGrants(t.Context(), first.Message.ExpiresAt)
	if err != nil || len(items) != 1 || items[0] != first.GrantID {
		t.Fatal("expired grant was duplicated or packet decrypted", items, err)
	}
	for _, r := range []OutboxRequest{first, second} {
		if err = s.MarkRelayed(t.Context(), r.Message.MessageID, testNow); err != nil {
			t.Fatal(err)
		}
	}
	if items, err = s.ExpiredOutboxGrants(t.Context(), first.Message.ExpiresAt); err != nil || len(items) != 0 {
		t.Fatal("relayed packets reported untransmitted expiry", items, err)
	}
	for _, now := range []int64{-1, 0, int64(protocol.MaxSafeInteger) + 1} {
		if _, err = s.ExpiredOutboxGrants(t.Context(), now); !fault.IsCode(err, fault.InvalidEncoding) {
			t.Fatal("invalid expiry clock accepted", err)
		}
	}
}

func TestExpiredOutboxGrantsBoundsMetadataWithoutDroppingGrantIDs(t *testing.T) {
	s := testStore(t)
	fixture(t, s)
	if err := s.write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<4097)
INSERT INTO grants(id,issuer,subject,signed,hash,epoch,expires_at)
SELECT printf('expiry_%04d',i),issuer,subject,signed,hash,epoch,expires_at FROM n CROSS JOIN grants WHERE id='grant_one'`)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(t.Context(), `INSERT INTO outbox(id,grant_id,digest,packet,lane,stage,expires_at,bytes,created_at)
SELECT id,id,'metadata-only',X'00','control','sender_stored',?,0,? FROM grants WHERE id LIKE 'expiry_%'`, testNow, testNow-1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if items, err := s.ExpiredOutboxGrants(t.Context(), testNow); !fault.IsCode(err, Backpressure) || items != nil {
		t.Fatal("over-limit grants silently truncated", len(items), err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE outbox SET stage='relay_stored' WHERE grant_id='expiry_4097'`); err != nil {
		t.Fatal(err)
	}
	items, err := s.ExpiredOutboxGrants(t.Context(), testNow)
	if err != nil || len(items) != 4096 || items[0] != "expiry_0001" || items[4095] != "expiry_4096" {
		t.Fatal("bounded ordered expiry metadata lost", len(items), err)
	}
}
