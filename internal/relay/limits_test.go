package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
	"github.com/temirlankasen/peerturn/internal/relay/config"
	"github.com/temirlankasen/peerturn/internal/relay/store"
)

func TestMailboxQuotaAndReservedControlCapacity(t *testing.T) {
	f := newFixtureConfig(t, func(c *config.Config) {
		c.Limits.MailboxItems = 1
		c.Limits.MaxStreams = 1
		c.Limits.StreamsPerKey = 1
		c.Limits.StreamsPerMailbox = 1
	})
	f.enroll(t)
	f.request(t, "POST", "/v1/mailboxes/box/messages", packet(t, 1, "one"), f.writer, 200)
	f.request(t, "POST", "/v1/mailboxes/box/messages", packet(t, 2, "two"), f.writer, 429)
	conn, _, err := f.dial(t, "/v1/events?mailbox=box", f.reader)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConnection(t, conn)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var event map[string]any
	if err = wsjson.Read(ctx, conn, &event); err != nil {
		t.Fatal(err)
	}
	other, resp, err := f.dial(t, "/v1/events?mailbox=box", f.reader)
	if other != nil {
		closeConnection(t, other)
	}
	if err == nil || resp != 429 {
		t.Fatalf("stream cap: %v %v", resp, err)
	}
	// Full stream and mailbox lanes leave owner ACK/revoke and diagnostics available.
	f.request(t, "GET", "/v1/mailboxes/box/messages", nil, f.reader, 200)
	f.request(t, "POST", "/v1/mailboxes/box/ack", map[string]int{"through": 1}, f.reader, 200)
	f.request(t, "POST", "/v1/mailboxes/box/messages", packet(t, 2, "two"), f.writer, 200)
	f.request(t, "DELETE", "/v1/mailboxes/box/permits/write", map[string]int{"epoch": 2}, f.reader, 200)
}

func TestFullMetadataPreservesSignedOwnerControl(t *testing.T) {
	f := newFixtureConfig(t, func(c *config.Config) { c.Limits.MailboxItems = 1; c.Limits.MetadataItems = 3 })
	ctx := t.Context()
	now := time.Now()
	if err := f.s.db.CreateMailbox(ctx, store.Mailbox{ID: "box", ReadKey: public(f.reader)}, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.db.PutPermit(ctx, "box", public(f.reader), store.Permit{ID: "write", WriterKey: public(f.writer), Epoch: 1, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	p := packet(t, 1, "last metadata slot")
	canonical, err := codec.Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.PutMessage(ctx, store.Message{MailboxID: "box", PermitID: "write", WriterKey: public(f.writer), PermitEpoch: 1, ID: p.Header.MessageID, Packet: canonical, ExpiresAt: time.Unix(p.Header.ExpiresAt, 0)}, now); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/v1/mailboxes/box/messages", nil, f.reader, 200)
	f.request(t, "POST", "/v1/mailboxes/box/ack", map[string]int{"through": 1}, f.reader, 200)
	f.request(t, "DELETE", "/v1/mailboxes/box/permits/write", map[string]int{"epoch": 2}, f.reader, 200)
}

func TestRequestAndPollBackpressureAndRate(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	for range cap(f.s.slots) {
		f.s.slots <- struct{}{}
	}
	f.request(t, "GET", "/.well-known/peerturn", nil, nil, 503)
	for range cap(f.s.slots) {
		<-f.s.slots
	}
	for range cap(f.s.polls) {
		f.s.polls <- struct{}{}
	}
	f.request(t, "GET", "/v1/mailboxes/box/messages?wait=1", nil, f.reader, 503)
	for range cap(f.s.polls) {
		<-f.s.polls
	}
	f.s.rateMu.Lock()
	f.s.cfg.Limits.RateBurst = 1
	f.s.buckets = make(map[string]bucket)
	f.s.rateMu.Unlock()
	f.request(t, "GET", "/.well-known/peerturn", nil, nil, 200)
	f.request(t, "GET", "/.well-known/peerturn", nil, nil, 429)
}

func TestDurableHTTPReplayAcrossRestart(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	data, err := json.Marshal(map[string]int{"through": 0})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for attempt, want := range []int{200, 401} {
		r, requestErr := http.NewRequestWithContext(t.Context(), "POST", f.h.URL+"/v1/mailboxes/box/ack", bytes.NewReader(data))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if err = auth.Sign(r, data, f.reader, now, now.Add(time.Minute), "MDEyMzQ1Njc4OWFiY2RlZg"); err != nil {
			t.Fatal(err)
		}
		resp, requestErr := f.h.Client().Do(r)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if resp.StatusCode != want {
			t.Fatalf("attempt %d: status=%d want=%d", attempt, resp.StatusCode, want)
		}
		if attempt == 0 {
			f.h.Close()
			if err = f.s.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			cfg := f.s.cfg
			now := f.s.now
			f.h = httptest.NewUnstartedServer(nil)
			cfg.PublicAuthority = f.h.Listener.Addr().String()
			f.s, err = newWithClock(cfg, now)
			if err != nil {
				t.Fatal(err)
			}
			f.h.Config.Handler = f.s.Handler()
			f.h.StartTLS()
		}
	}
}
