package update_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/update"
)

func TestNewClientBoundsTimeouts(t *testing.T) {
	t.Parallel()
	client := update.NewClient()
	if client.Timeout != 10*time.Minute {
		t.Errorf("overall timeout = %v, want 10m", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", client.Transport)
	}
	if transport.ResponseHeaderTimeout != 15*time.Second || transport.TLSHandshakeTimeout != 15*time.Second {
		t.Errorf("header timeout = %v, TLS timeout = %v, want 15s", transport.ResponseHeaderTimeout, transport.TLSHandshakeTimeout)
	}
}

func TestNewClientFollowsOnlyHTTPSRedirects(t *testing.T) {
	t.Parallel()
	client := update.NewClient()
	request := func(target string) *http.Request {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	if err := client.CheckRedirect(request("https://objects.example/asset"), nil); err != nil {
		t.Errorf("HTTPS redirect refused: %v", err)
	}
	if err := client.CheckRedirect(request("http://objects.example/asset"), nil); err == nil {
		t.Error("a redirect to plain HTTP was followed")
	}
	via := make([]*http.Request, 5)
	if err := client.CheckRedirect(request("https://objects.example/asset"), via); err == nil {
		t.Error("a redirect chain longer than five hops was followed")
	}
}

func TestTailKeepsTheLastReadableLine(t *testing.T) {
	t.Parallel()
	if got := update.Tail([]byte("first\nError: bad thing\n\n"), 100); got != "Error: bad thing" {
		t.Errorf("Tail = %q", got)
	}
	if got := update.Tail([]byte("x\x1b[31mred\x00\x07 text\n"), 100); got != "x[31mred text" {
		t.Errorf("control characters survived: %q", got)
	}
	if got := update.Tail([]byte(strings.Repeat("é", 300)), 10); len([]rune(got)) > 11 || !strings.HasSuffix(got, "…") {
		t.Errorf("long line was not shortened: %q", got)
	}
	if got := update.Tail(nil, 10); got != "" {
		t.Errorf("empty output produced %q", got)
	}
}
