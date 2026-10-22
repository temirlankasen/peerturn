package relay

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"github.com/temirlankasen/peerturn/internal/relay/config"
)

func TestMailboxWakesAreIsolated(t *testing.T) {
	f := newFixture(t)
	a, stopA := f.s.watchMailbox("a")
	defer stopA()
	b, stopB := f.s.watchMailbox("b")
	defer stopB()
	ca, cb := f.s.changes(a), f.s.changes(b)
	f.s.signal("a")
	select {
	case <-ca:
	default:
		t.Fatal("affected mailbox not woken")
	}
	select {
	case <-cb:
		t.Fatal("unrelated mailbox woken")
	default:
	}
}

func TestPollAndStreamFairness(t *testing.T) {
	f := newFixtureConfig(t, func(c *config.Config) {
		c.Limits.PollsPerKey = 1
		c.Limits.PollsPerMailbox = 1
		c.Limits.StreamsPerKey = 1
		c.Limits.StreamsPerMailbox = 1
	})
	if !f.s.reservePoll("reader-a", "box-a") {
		t.Fatal("initial poll refused")
	}
	defer f.s.releasePoll("reader-a", "box-a")
	if f.s.reservePoll("reader-a", "box-b") || f.s.reservePoll("reader-b", "box-a") {
		t.Fatal("poll tenant escaped cap")
	}
	if !f.s.reservePoll("reader-b", "box-b") {
		t.Fatal("unrelated tenant poll refused")
	}
	f.s.releasePoll("reader-b", "box-b")
	if !f.s.reserveStream("reader-a", "box-a") {
		t.Fatal("initial stream refused")
	}
	defer f.s.releaseStream("reader-a", "box-a")
	if f.s.reserveStream("reader-a", "box-b") || f.s.reserveStream("reader-b", "box-a") {
		t.Fatal("stream tenant escaped cap")
	}
	if !f.s.reserveStream("reader-b", "box-b") {
		t.Fatal("unrelated tenant stream refused")
	}
	f.s.releaseStream("reader-b", "box-b")
}

func TestStreamCapDoesNotConsumeTicket(t *testing.T) {
	f := newFixtureConfig(t, func(c *config.Config) {
		c.Limits.MaxStreams = 1
		c.Limits.StreamsPerKey = 1
		c.Limits.StreamsPerMailbox = 1
	})
	f.enroll(t)
	data := f.request(t, "POST", "/v1/streams/tickets", map[string]string{"mailbox": "box"}, f.reader, 201)
	var ticket struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(data, &ticket); err != nil {
		t.Fatal(err)
	}
	if !f.s.reserveStream(public(f.reader), "box") {
		t.Fatal("reserve stream")
	}
	conn, status, err := f.dial(t, "/v1/events?ticket="+ticket.Ticket, nil)
	if conn != nil {
		closeConnection(t, conn)
	}
	f.s.releaseStream(public(f.reader), "box")
	if err == nil || status != 429 {
		t.Fatalf("full stream pool status=%d err=%v", status, err)
	}
	conn, status, err = f.dial(t, "/v1/events?ticket="+ticket.Ticket, nil)
	if err != nil {
		t.Fatalf("retry ticket status=%d err=%v", status, err)
	}
	defer closeConnection(t, conn)
}

func TestReaderStreamClosesOnMailboxDelete(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
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
	f.request(t, "DELETE", "/v1/mailboxes/box", nil, f.reader, 200)
	if err = wsjson.Read(ctx, conn, &event); err == nil {
		t.Fatalf("deleted mailbox stream stayed live: %v", event)
	}
}
