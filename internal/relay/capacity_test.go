package relay

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/relay/config"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func TestPhysicalQuotaPreservesSignedOwnerControl(t *testing.T) {
	f := newFixtureConfig(t, func(c *config.Config) {
		c.Limits.TotalBytes = 4 << 20
		c.Limits.MailboxBytes = 4 << 20
		c.Limits.MetadataItems = 1000
	})
	f.enroll(t)
	p := packet(t, 1, "capacity fixture")
	p.Ciphertext = make([]byte, 64<<10)
	accepted := 0
	for i := 1; i <= 1000; i++ {
		p.Header.MessageID = fmt.Sprintf("%032x", i)
		data, err := codec.Canonical(p)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.s.db.PutMessage(t.Context(), store.Message{MailboxID: "box", PermitID: "write", WriterKey: public(f.writer), PermitEpoch: 1, ID: p.Header.MessageID, Packet: data, ExpiresAt: time.Unix(p.Header.ExpiresAt, 0)}, f.s.now())
		if err != nil {
			e, ok := errors.AsType[*store.Error](err)
			if !ok || e.Status != 429 {
				t.Fatalf("data growth failed before logical quota: %v", err)
			}
			break
		}
		accepted++
	}
	if accepted == 0 || accepted == 1000 {
		t.Fatalf("quota fixture accepted=%d", accepted)
	}
	// Distinct tenants fill the derived global budget too, beyond box's fair share.
	full := false
	for i := 1; i <= 1000; i++ {
		box, reader, writer := fmt.Sprintf("tenant-%d", i), fmt.Sprintf("reader-%d", i), fmt.Sprintf("writer-%d", i)
		err := f.s.db.CreateMailbox(t.Context(), store.Mailbox{ID: box, ReadKey: reader}, "")
		if err != nil {
			e, ok := errors.AsType[*store.Error](err)
			if !ok || e.Status != 429 {
				t.Fatal(err)
			}
			full = true
			break
		}
		err = f.s.db.PutPermit(t.Context(), box, reader, store.Permit{ID: "write", WriterKey: writer, Epoch: 1, ExpiresAt: f.s.now().Add(time.Hour)})
		if err != nil {
			e, ok := errors.AsType[*store.Error](err)
			if !ok || e.Status != 429 {
				t.Fatal(err)
			}
			full = true
			break
		}
		_, err = f.s.db.PutMessage(t.Context(), store.Message{MailboxID: box, PermitID: "write", WriterKey: writer, PermitEpoch: 1, ID: fmt.Sprintf("other-%d", i), Packet: make([]byte, 64<<10), ExpiresAt: f.s.now().Add(time.Hour)}, f.s.now())
		if err != nil {
			e, ok := errors.AsType[*store.Error](err)
			if !ok || e.Status != 429 {
				t.Fatal(err)
			}
		}
	}
	if !full {
		t.Fatal("global quota not reached")
	}
	// Exercise actual HTTP signature + durable nonce admission with the data lane full.
	f.request(t, "GET", "/v1/mailboxes/box/messages?limit=1", nil, f.reader, 200)
	f.request(t, "POST", "/v1/mailboxes/box/ack", map[string]int{"through": 1}, f.reader, 200)
	f.request(t, "DELETE", "/v1/mailboxes/box/permits/write", map[string]int{"epoch": 2}, f.reader, 200)
	f.request(t, "DELETE", "/v1/mailboxes/box", nil, f.reader, 200)
}
