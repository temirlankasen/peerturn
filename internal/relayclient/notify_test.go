package relayclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

func TestNotifyReconnectUsesDurableFrontierAndFreshSignature(t *testing.T) {
	key := testKey(t)
	var connections atomic.Int64
	s := httptest.NewUnstartedServer(nil)
	v, err := auth.NewVerifier(auth.Options{PublicAuthority: s.Listener.Addr().String(), Replay: func(context.Context, string, string, int64) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	nonces := make(chan string, 4)
	s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Error(readErr)
			return
		}
		if _, verifyErr := v.Verify(r.Context(), r, body); verifyErr != nil {
			t.Errorf("upgrade signature: %v", verifyErr)
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/v1/events" || r.URL.Query().Get("mailbox") != "box" {
			t.Error("unexpected subscription")
		}
		nonces <- r.Header.Get("Signature-Input")
		conn, acceptErr := websocket.Accept(w, r, nil)
		if acceptErr != nil {
			t.Error(acceptErr)
			return
		}
		// Count before the event: the client may cancel as soon as it reads the
		// second event, before this handler would otherwise reach the counter.
		connections.Add(1)
		if writeErr := conn.Write(r.Context(), websocket.MessageText, []byte(`{"mailbox":"box","high_seq":7}`)); writeErr != nil {
			t.Error(writeErr)
		}
		if closeErr := conn.Close(websocket.StatusGoingAway, "restart"); closeErr != nil {
			return
		}
	})
	s.StartTLS()
	t.Cleanup(s.Close)
	c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var frontier atomic.Int64
	frontier.Store(3)
	var calls int
	err = c.Notify(ctx, "box", NotifyOptions{Frontier: func(context.Context) (int64, error) { return frontier.Load(), nil }, OnEvent: func(_ context.Context, event Event) error {
		calls++
		want := int64(3)
		if calls > 1 {
			want = 5
		}
		if event.After != want || event.Mailbox != "box" || event.HighSeq != 7 {
			t.Errorf("event = %+v, want frontier %d", event, want)
		}
		frontier.Store(5)
		if calls == 2 {
			cancel()
		}
		return nil
	}})
	if !errors.Is(err, context.Canceled) || calls != 2 || connections.Load() != 2 {
		t.Fatalf("notify calls=%d connections=%d error=%v", calls, connections.Load(), err)
	}
	first, second := <-nonces, <-nonces
	if first == second {
		t.Fatal("reconnected with reused signature")
	}
}

func TestNotifyDoesNotRetryForbiddenUpgrade(t *testing.T) {
	var calls atomic.Int64
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		if _, err := io.WriteString(w, `{"error":"forbidden"}`); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(s.Close)
	c, err := New(Options{URL: s.URL, Key: testKey(t), HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Notify(t.Context(), "box", NotifyOptions{Frontier: func(context.Context) (int64, error) { return 0, nil }, OnEvent: func(context.Context, Event) error { return nil }})
	if problem, ok := errors.AsType[*Error](err); !ok || problem.Status != 403 || calls.Load() != 1 {
		t.Fatalf("error=%v calls=%d", err, calls.Load())
	}
}

func TestNotifyBriefEventsDoNotResetReconnectBackoff(t *testing.T) {
	connections := make(chan time.Time, 4)
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer closeStream(conn)
		connections <- time.Now()
		if err = conn.Write(r.Context(), websocket.MessageText, []byte(`{"mailbox":"box","high_seq":0}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(s.Close)
	c, err := New(Options{URL: s.URL, Key: testKey(t), HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	calls := 0
	err = c.Notify(ctx, "box", NotifyOptions{
		Frontier: func(context.Context) (int64, error) { return 0, nil },
		OnEvent: func(context.Context, Event) error {
			calls++
			if calls == 4 {
				cancel()
			}
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) || calls != 4 {
		t.Fatalf("events=%d error=%v", calls, err)
	}
	<-connections
	<-connections
	third, fourth := <-connections, <-connections
	if delay := fourth.Sub(third); delay < 500*time.Millisecond {
		t.Fatalf("brief event reset reconnect backoff: fourth connection after %v", delay)
	}
}
