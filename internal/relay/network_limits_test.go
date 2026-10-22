package relay

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/config"
)

func TestBodyDeadlineBoundsStalledConnection(t *testing.T) {
	f := newFixtureConfig(t, func(c *config.Config) { c.Limits.BodyReadSeconds = 1 })
	reader, writer := io.Pipe()
	defer func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	}()
	r, err := http.NewRequestWithContext(t.Context(), "POST", f.h.URL+"/v1/mailboxes", reader)
	if err != nil {
		t.Fatal(err)
	}
	r.ContentLength = 100
	type result struct {
		status int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		resp, doErr := f.h.Client().Do(r)
		if doErr != nil {
			done <- result{err: doErr}
			return
		}
		done <- result{status: resp.StatusCode, err: resp.Body.Close()}
	}()
	if _, err = writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/.well-known/peerturn", nil, nil, 200)
	select {
	case got := <-done:
		if got.err != nil || got.status != 400 {
			t.Fatalf("stalled body result=%+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("body deadline did not bound read")
	}
}

func TestPerIPConcurrencyStopsBodiesBeforeRead(t *testing.T) {
	f := newFixture(t)
	ip := "198.51.100.20"
	for range f.s.cfg.Limits.RequestsPerIP {
		if !f.s.reserveIP(ip) {
			t.Fatal("initial IP reservation refused")
		}
		defer f.s.releaseIP(ip)
	}
	body := &stalledBody{started: make(chan struct{}), done: make(chan struct{})}
	defer close(body.done)
	r := httptest.NewRequest("POST", f.h.URL+"/v1/mailboxes", body)
	r.RemoteAddr = ip + ":1234"
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatalf("IP concurrency status=%d", w.Code)
	}
	select {
	case <-body.started:
		t.Fatal("body read beyond IP concurrency cap")
	default:
	}
}

func TestProxyHeadersCannotSpoofUntrustedPeer(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest("GET", f.h.URL+"/.well-known/peerturn", nil)
	r.RemoteAddr = "198.51.100.20:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.1")
	ip, ok := f.s.clientIP(r)
	if !ok || ip != "198.51.100.20" {
		t.Fatalf("spoofed IP %q ok=%v", ip, ok)
	}
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.1, 198.51.100.20, 127.0.0.1")
	ip, ok = f.s.clientIP(r)
	if !ok || ip != "198.51.100.20" {
		t.Fatalf("wrong proxy hop %q ok=%v", ip, ok)
	}
}

func TestListenerBoundsIdleConnections(t *testing.T) {
	var lc net.ListenConfig
	raw, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := limitListener(raw, 1)
	defer func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	var dialer net.Dialer
	client, err := dialer.DialContext(t.Context(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	first, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	secondClient, err := dialer.DialContext(t.Context(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := secondClient.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	accepted := make(chan net.Conn, 1)
	failures := make(chan error, 1)
	go func() {
		c, acceptErr := listener.Accept()
		if acceptErr != nil {
			failures <- acceptErr
			return
		}
		accepted <- c
	}()
	select {
	case c := <-accepted:
		if closeErr := c.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("idle connection exceeded cap")
	case <-time.After(20 * time.Millisecond):
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-accepted:
		if closeErr := c.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	case err = <-failures:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("closed connection did not release capacity")
	}
}
