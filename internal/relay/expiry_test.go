package relay

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func TestExpiredRetryKeepsOriginalReceiptAndNoNewCustody(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	p := packet(t, 1, "expired custody")
	expires := time.Now().Add(-time.Second)
	p.Header.ExpiresAt = expires.Unix()
	canonical, err := codec.Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.db.PutMessage(t.Context(), store.Message{MailboxID: "box", PermitID: "write", WriterKey: public(f.writer), PermitEpoch: 1, ID: p.Header.MessageID, Packet: canonical, ExpiresAt: expires}, expires.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 200)
	p.Header.MessageID = "00000000000000000000000000000002"
	data := f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 410)
	var problem struct {
		Error string `json:"error"`
	}
	if err = json.Unmarshal(data, &problem); err != nil || problem.Error != "expired_message" {
		t.Fatalf("expired submit = %s, decode error %v", data, err)
	}
	p.Header.ExpiresAt = time.Now().Add(time.Hour).Unix()
	f.request(t, "DELETE", "/v1/mailboxes/box/permits/write", map[string]int{"epoch": 2}, f.reader, 200)
	data = f.request(t, "POST", "/v1/mailboxes/box/messages", p, f.writer, 403)
	if err = json.Unmarshal(data, &problem); err != nil || problem.Error != "forbidden" {
		t.Fatalf("revoked permit = %s, decode error %v", data, err)
	}
}
