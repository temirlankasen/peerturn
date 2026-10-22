package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyncBackoff(t *testing.T) {
	attempts := make(chan time.Time, 8)
	var calls atomic.Int64
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/messages") {
			attempts <- time.Now()
			if calls.Add(1) == 3 {
				if _, err := io.WriteString(w, `{"messages":[]}`); err != nil {
					t.Error(err)
				}
				return
			}
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := io.WriteString(w, `{"error":"storage_unavailable"}`); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(s.Close)
	dir := stateDir(t)
	if _, err := Initialize(t.Context(), InitOptions{StateDir: dir, RelayURL: s.URL, SessionLabel: "backoff", FilePermissions: true}); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), Options{StateDir: dir, HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	d.state.Enrolled = true
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); d.loop(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	next := func() time.Time {
		select {
		case when := <-attempts:
			return when
		case <-deadline.C:
			t.Fatal("sync did not retry transient failure")
			return time.Time{}
		}
	}
	first, second := next(), next()
	if delay := second.Sub(first); delay < time.Second {
		t.Fatalf("first transient retry omitted backoff: %v", delay)
	}
	d.signal()
	third := next()
	if delay := third.Sub(second); delay < 2*time.Second {
		t.Fatalf("transient retry did not grow backoff or wake bypassed it: %v", delay)
	}
	d.signal()
	fourth, fifth := next(), next()
	if delay := fourth.Sub(third); delay >= time.Second {
		t.Fatalf("successful sync retained the error fence: %v", delay)
	}
	if delay := fifth.Sub(fourth); delay < time.Second || delay >= 2*time.Second {
		t.Fatalf("successful sync did not reset retry backoff: %v", delay)
	}
}
