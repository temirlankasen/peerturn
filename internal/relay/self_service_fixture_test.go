package relay

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/relay/auth"
	"github.com/temirlankasen/peerturn/internal/relay/config"
)

type selfServiceHTTPFixture struct {
	s         *Server
	cfg       config.Config
	admin     ed25519.PrivateKey
	publisher ed25519.PrivateKey
	recipient ed25519.PrivateKey
	stranger  ed25519.PrivateKey
	clock     atomic.Int64
	observer  *sql.DB
}

func newSelfServiceHTTPFixture(t *testing.T, customize func(*config.Config)) *selfServiceHTTPFixture {
	t.Helper()
	f := &selfServiceHTTPFixture{admin: key(t), publisher: key(t), recipient: key(t), stranger: key(t)}
	f.clock.Store(time.Now().UTC().Truncate(time.Second).UnixNano())
	f.cfg = config.Defaults()
	f.cfg.AdmissionMode = config.AdmissionSelfService
	f.cfg.PublicAuthority = "relay.example"
	f.cfg.DataDir = filepath.Join(t.TempDir(), "data")
	f.cfg.AdminKeys = []string{public(f.admin)}
	f.cfg.TrustedProxies = []string{"127.0.0.1/32"}
	f.cfg.Limits.RatePerMinute, f.cfg.Limits.RateBurst = 100000, 100000
	if customize != nil {
		customize(&f.cfg)
	}
	var err error
	f.s, err = newWithClock(f.cfg, f.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f.observer != nil {
			if closeErr := f.observer.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		if f.s != nil {
			if closeErr := f.s.Close(t.Context()); closeErr != nil {
				t.Error(closeErr)
			}
		}
	})
	// Observe only this fixture's temporary database; never bypass store writes.
	dsn := (&url.URL{Scheme: "file", Path: filepath.Join(f.cfg.DataDir, "relay.sqlite"), RawQuery: "mode=ro"}).String()
	f.observer, err = sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *selfServiceHTTPFixture) now() time.Time { return time.Unix(0, f.clock.Load()).UTC() }

func (f *selfServiceHTTPFixture) advance(delta time.Duration) { f.clock.Add(int64(delta)) }

func (f *selfServiceHTTPFixture) restart(t *testing.T) {
	t.Helper()
	if err := f.s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	var err error
	f.s, err = newWithClock(f.cfg, f.now)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *selfServiceHTTPFixture) signed(t *testing.T, method, path string, data []byte, signer ed25519.PrivateKey) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, "https://relay.example"+path, bytes.NewReader(data))
	r.RequestURI = r.URL.RequestURI()
	r.RemoteAddr = "198.51.100.9:1234"
	if signer != nil {
		if err := auth.Sign(r, data, signer, f.now(), f.now().Add(30*time.Second), opaqueID(t)); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (f *selfServiceHTTPFixture) serve(t *testing.T, r *http.Request, want int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: status=%d want=%d body=%s", r.Method, r.URL.Path, w.Code, want, w.Body.String())
	}
	return w
}

func selfServiceJSON(t *testing.T, body any) []byte {
	t.Helper()
	if body == nil {
		return nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (f *selfServiceHTTPFixture) request(t *testing.T, method, path string, body any, signer ed25519.PrivateKey, want int) []byte {
	t.Helper()
	r := f.signed(t, method, path, selfServiceJSON(t, body), signer)
	return f.serve(t, r, want).Body.Bytes()
}

func selfServiceMailbox(signer ed25519.PrivateKey) string {
	digest := sha256.Sum256(signer[ed25519.SeedSize:])
	return hex.EncodeToString(digest[:])
}

func (f *selfServiceHTTPFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.observer.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type selfServiceHTTPState struct{ inviters, mailboxes, invites int }

func (f *selfServiceHTTPFixture) state(t *testing.T) selfServiceHTTPState {
	t.Helper()
	return selfServiceHTTPState{
		f.count(t, "SELECT COUNT(*) FROM relay_inviters"),
		f.count(t, "SELECT COUNT(*) FROM mailboxes"),
		f.count(t, "SELECT COUNT(*) FROM invites"),
	}
}

func (f *selfServiceHTTPFixture) wantState(t *testing.T, want selfServiceHTTPState) {
	t.Helper()
	if got := f.state(t); got != want {
		t.Fatalf("admission state=%+v want=%+v", got, want)
	}
}

func (f *selfServiceHTTPFixture) publish(t *testing.T, signer ed25519.PrivateKey, invitation selfServiceHTTPInvite, want int) []byte {
	t.Helper()
	return f.request(t, "POST", "/v1/invites/"+invitation.id, invitation.input(), signer, want)
}
