package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

func TestLongPollNotifyAndReconnectCustody(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	conn, _, err := f.dial(t, "/v1/events?mailbox=box", f.reader)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConnection(t, conn)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var event map[string]any
	if err = wsjson.Read(ctx, conn, &event); err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequestWithContext(ctx, "GET", f.h.URL+"/v1/mailboxes/box/messages?after=0&limit=100&wait=3", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = auth.Sign(r, nil, f.reader, now, now.Add(time.Minute), "YWJjZGVmZ2hpamtsbW5vcA"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		data   []byte
		status int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		resp, requestErr := f.h.Client().Do(r)
		if requestErr != nil {
			done <- result{err: requestErr}
			return
		}
		body, readErr := io.ReadAll(resp.Body)
		done <- result{data: body, status: resp.StatusCode, err: errors.Join(readErr, resp.Body.Close())}
	}()
	select {
	case got := <-done:
		t.Fatalf("poll returned before storage: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	f.request(t, "POST", "/v1/mailboxes/box/messages", packet(t, 1, "offline durable input"), f.writer, 200)
	select {
	case got := <-done:
		if got.err != nil || got.status != 200 || !bytes.Contains(got.data, []byte(`"seq":1`)) {
			t.Fatalf("poll result: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("long poll missed committed input")
	}
	if err = wsjson.Read(ctx, conn, &event); err != nil {
		t.Fatal(err)
	}
	if len(event) != 2 || event["high_seq"] != float64(1) {
		t.Fatalf("notification leaked or lost content: %v", event)
	}
	// Reconnect from durable cursor without depending on whether a hint was received.
	f.request(t, "GET", "/v1/mailboxes/box/messages?after=0", nil, f.reader, 200)
	f.request(t, "POST", "/v1/mailboxes/box/ack", map[string]int{"through": 1}, f.reader, 200)
	f.request(t, "GET", "/v1/mailboxes/box/messages?after=1&wait=1", nil, f.reader, 200)
}
