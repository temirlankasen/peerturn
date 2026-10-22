package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestStopRevokedReceiptIsScopedAtomicAndIdempotent(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	var control string
	if err := s.db.QueryRowContext(t.Context(), `SELECT id FROM outbox WHERE lane='control'`).Scan(&control); err != nil {
		t.Fatal(err)
	}
	if err := s.StopRevokedReceipt(t.Context(), control, "grant_one", testNow); !fault.IsCode(err, NotFound) {
		t.Fatalf("relay denial must not terminate live peer receipts: %v", err)
	}
	if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow+1, f.receipt); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(t.Context(), `SELECT id FROM outbox WHERE priority=1`).Scan(&control); err != nil {
		t.Fatal(err)
	}
	// A different channel's control and a data row must remain untouched even
	// when their caller supplies the identifier of a real revoked grant.
	for i, lane := range []string{"control", "data"} {
		id := strings.Repeat(string(rune('b'+i)), 32)
		channel := "unrelated-channel"
		if lane == "data" {
			channel = channelKey(f.recipient.cert.Binding, f.sender.cert.Binding)
		}
		if _, err := s.db.ExecContext(t.Context(), `INSERT INTO outbox(id,grant_id,digest,packet,lane,stage,expires_at,bytes,created_at,channel) SELECT ?,grant_id,digest,packet,?,'sender_stored',expires_at,bytes,created_at,? FROM outbox WHERE id=?`, id, lane, channel, control); err != nil {
			t.Fatal(err)
		}
		if err := s.StopRevokedReceipt(t.Context(), id, "grant_one", testNow+2); !fault.IsCode(err, NotFound) {
			t.Fatalf("unrelated %s was terminalized: %v", lane, err)
		}
		var stage string
		if err := s.db.QueryRowContext(t.Context(), `SELECT stage FROM outbox WHERE id=?`, id).Scan(&stage); err != nil || stage != "sender_stored" {
			t.Fatalf("unrelated custody changed: %s %v", stage, err)
		}
	}
	injected := errors.New("interrupted terminal receipt transaction")
	s.abort = func(step string) error {
		if step == "before_commit" {
			return injected
		}
		return nil
	}
	if err := s.StopRevokedReceipt(t.Context(), control, "grant_one", testNow+2); !errors.Is(err, injected) {
		t.Fatalf("terminalization abort: %v", err)
	}
	s.abort = nil
	if _, err := s.OutboxPacket(t.Context(), control); err != nil {
		t.Fatal("failed transaction lost receipt retry", err)
	}
	for range 2 {
		if err := s.StopRevokedReceipt(t.Context(), control, "grant_one", testNow+2); err != nil {
			t.Fatal(err)
		}
	}
	var stage string
	var audits int
	if err := s.db.QueryRowContext(t.Context(), `SELECT stage,(SELECT COUNT(*) FROM audit WHERE action='revoke_notification_undeliverable' AND message_id=? AND grant_id='grant_one' AND epoch=2 AND digest!='' AND reason='route_closed') FROM outbox WHERE id=?`, control, control).Scan(&stage, &audits); err != nil || stage != "rejected" || audits != 1 {
		t.Fatalf("terminal/audit evidence: stage=%s audits=%d err=%v", stage, audits, err)
	}
	if _, err := s.OutboxPacket(t.Context(), control); !fault.IsCode(err, NotFound) {
		t.Fatal("terminal receipt still retries", err)
	}
	if receipt, err := s.Receipt(t.Context(), f.message.MessageID); err != nil || receipt.Stage != "revoked" {
		t.Fatalf("notification failure changed native evidence: %+v %v", receipt, err)
	}
}
