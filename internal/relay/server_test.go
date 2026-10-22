package relay

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
	"github.com/temirlankasen/peerturn/internal/relay/config"
)

type fixture struct {
	s      *Server
	h      *httptest.Server
	admin  ed25519.PrivateKey
	reader ed25519.PrivateKey
	writer ed25519.PrivateKey
}

func TestPermitReservesTerminalEpochForRevoke(t *testing.T) {
	f := newFixture(t)
	f.enroll(t)
	input := map[string]any{"writer_key": public(f.writer), "epoch": protocol.MaxSafeInteger, "expires_at": time.Now().Add(time.Hour).Unix()}
	f.request(t, "PUT", "/v1/mailboxes/box/permits/write", input, f.reader, 400)
	input["epoch"] = protocol.MaxSafeInteger - 1
	f.request(t, "PUT", "/v1/mailboxes/box/permits/write", input, f.reader, 200)
	f.request(t, "DELETE", "/v1/mailboxes/box/permits/write", map[string]int64{"epoch": protocol.MaxSafeInteger}, f.reader, 200)
}

func key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func public(k ed25519.PrivateKey) string {
	return base64.RawURLEncoding.EncodeToString(k[ed25519.SeedSize:])
}

func newFixture(t *testing.T) *fixture {
	return newFixtureConfig(t, nil)
}

func newFixtureConfig(t *testing.T, customize func(*config.Config)) *fixture {
	t.Helper()
	f := &fixture{admin: key(t), reader: key(t), writer: key(t)}
	c := config.Defaults()
	c.DataDir = filepath.Join(t.TempDir(), "data")
	c.AdminKeys = []string{public(f.admin)}
	c.TrustedProxies = []string{"127.0.0.1/32"}
	c.Limits.RatePerMinute, c.Limits.RateBurst = 100000, 100000
	if customize != nil {
		customize(&c)
	}
	f.h = httptest.NewUnstartedServer(nil)
	c.PublicAuthority = f.h.Listener.Addr().String()
	var err error
	now := time.Now().UTC()
	f.s, err = newWithClock(c, func() time.Time { return now })
	if err != nil {
		f.h.Close()
		t.Fatal(err)
	}
	f.h.Config.Handler = f.s.Handler()
	f.h.StartTLS()
	t.Cleanup(func() {
		f.h.Close()
		if err := f.s.Close(t.Context()); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *fixture) request(t *testing.T, method, path string, body any, signer ed25519.PrivateKey, want int) []byte {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := http.NewRequestWithContext(t.Context(), method, f.h.URL+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if signer != nil {
		now := f.s.now()
		nonce := make([]byte, 16)
		if _, err = rand.Read(nonce); err != nil {
			t.Fatal(err)
		}
		if err = auth.Sign(r, data, signer, now, now.Add(30*time.Second), base64.RawURLEncoding.EncodeToString(nonce)); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := f.h.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d want %d: %s", method, path, resp.StatusCode, want, got)
	}
	return got
}

func (f *fixture) enroll(t *testing.T) {
	t.Helper()
	f.request(t, "POST", "/v1/mailboxes", map[string]any{"id": "box", "read_key": public(f.reader)}, f.admin, 201)
	f.request(t, "PUT", "/v1/mailboxes/box/permits/write", map[string]any{"writer_key": public(f.writer), "epoch": 1, "expires_at": time.Now().Add(time.Hour).Unix()}, f.reader, 200)
}

func TestPublicDiscoveryAndPrivateHealth(t *testing.T) {
	f := newFixture(t)
	f.request(t, "GET", "/.well-known/peerturn", nil, nil, 200)
	f.request(t, "GET", "/i/public-id", nil, nil, 200)
	f.request(t, "GET", "/healthz", nil, nil, 404)
	f.request(t, "GET", "/v1/mailboxes/box/messages", nil, nil, 401)
	f.request(t, "POST", "/v1/execute", nil, f.admin, 404)
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		f.s.PrivateHandler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("private %s: %d", path, w.Code)
		}
	}
}

func TestMailboxAuthorityAndEpochs(t *testing.T) {
	f := newFixture(t)
	f.request(t, "POST", "/v1/mailboxes", map[string]any{"id": "bad", "read_key": public(f.reader)}, f.writer, 403)
	f.enroll(t)
	f.request(t, "GET", "/v1/mailboxes/box/messages?after=0&limit=100", nil, f.reader, 200)
	f.request(t, "GET", "/v1/mailboxes/box/messages", nil, f.writer, 403)
	f.request(t, "GET", "/v1/mailboxes/box/messages?limit=101", nil, f.reader, 400)
	f.request(t, "PUT", "/v1/mailboxes/box/permits/write", map[string]any{"writer_key": public(f.reader), "epoch": 1, "expires_at": time.Now().Add(time.Hour).Unix()}, f.reader, 409)
	f.request(t, "DELETE", "/v1/mailboxes/box/permits/write", map[string]any{"epoch": 2}, f.reader, 200)
	f.request(t, "DELETE", "/v1/mailboxes/box/permits/write", map[string]any{"epoch": 1}, f.reader, 409)
}
