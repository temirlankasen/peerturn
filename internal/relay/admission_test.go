package relay

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/config"
)

type stalledBody struct {
	started chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (b *stalledBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.done
	return 0, io.EOF
}
func (b *stalledBody) Close() error { return nil }

func TestSlowBodyDoesNotAcquireGlobalSlot(t *testing.T) {
	f := newFixture(t)
	body := &stalledBody{started: make(chan struct{}), done: make(chan struct{})}
	r := httptest.NewRequest("POST", f.h.URL+"/v1/mailboxes", body)
	r.RemoteAddr = "198.51.100.10:1234"
	done := make(chan struct{})
	go func() { defer close(done); f.s.Handler().ServeHTTP(httptest.NewRecorder(), r) }()
	defer func() { close(body.done); <-done }()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("body not read")
	}
	if n := len(f.s.slots); n != 0 {
		t.Fatalf("slow unauthenticated body occupies %d global slots", n)
	}
}

func TestLimiterEvictsInsteadOfDenyingNewClients(t *testing.T) {
	f := newFixtureConfig(t, func(c *config.Config) { c.Limits.MailboxItems = 1; c.Limits.MetadataItems = 2 })
	now := time.Now()
	for _, id := range []string{"ip:a", "ip:b", "ip:c"} {
		if !f.s.allow(id, now) {
			t.Fatalf("new client %s refused by full table", id)
		}
	}
	if len(f.s.buckets) > 2 {
		t.Fatal("limiter exceeded bound")
	}
}

func TestProxyClientBucketsAndIPv6Aggregation(t *testing.T) {
	for _, tc := range []struct {
		name, a, b string
		want       int
	}{
		{"distinct clients", "198.51.100.10", "198.51.100.11", 200},
		{"IPv6 subnet", "2001:db8:1::1", "2001:db8:1::2", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixtureConfig(t, func(c *config.Config) { c.Limits.RatePerMinute = 1; c.Limits.RateBurst = 1 })
			for i, ip := range []string{tc.a, tc.b} {
				r := httptest.NewRequest("GET", f.h.URL+"/.well-known/peerturn", nil)
				r.RemoteAddr = "127.0.0.1:1234"
				r.Header.Set("X-Forwarded-For", ip)
				r.Header.Set("X-Forwarded-Proto", "https")
				r.Header.Set("X-Forwarded-Host", r.Host)
				w := httptest.NewRecorder()
				f.s.Handler().ServeHTTP(w, r)
				want := 200
				if i == 1 {
					want = tc.want
				}
				if w.Code != want {
					t.Fatalf("client %s got %d want %d", ip, w.Code, want)
				}
			}
		})
	}
}

func nonceCount(t *testing.T, f *fixture) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(f.s.cfg.DataDir, "relay.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	var n int
	if err = db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM nonces").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUnknownKeyDoesNotWriteDurableNonce(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	before := nonceCount(t, f)
	f.request(t, "GET", "/v1/mailboxes/box/messages", nil, key(t), http.StatusForbidden)
	if after := nonceCount(t, f); after != before {
		t.Fatalf("unknown key wrote nonce: %d -> %d", before, after)
	}
}

func TestWriterCannotSubscribeOrMintNotifyTicket(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	f.request(t, "POST", "/v1/streams/tickets", map[string]string{"mailbox": "box", "permit": "write"}, f.writer, 403)
	conn, status, err := f.dial(t, "/v1/events?mailbox=box&permit=write", f.writer)
	if conn != nil {
		closeConnection(t, conn)
	}
	if err == nil || status != 403 {
		t.Fatalf("writer notify admission status=%d err=%v", status, err)
	}
}
