package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/daemon"
	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
	"github.com/temirlankasen/peerturn/internal/relay/config"
)

// Synthetic preview custody only: no native session, model input or peer join.
func TestSelfServiceIdleFirstLinkKeepsClaimPollingWithinDefaultNonceBudget(t *testing.T) {
	f := newIdleFirstLinkRelay(t)
	routing := key(t)
	if slices.Contains(f.cfg.AdminKeys, public(routing)) {
		t.Fatal("synthetic daemon unexpectedly has operator authority")
	}
	const label = "synthetic-idle-first-link"
	dir := idleFirstLinkStateDir(t)
	initialized, err := daemon.Initialize(t.Context(), daemon.InitOptions{
		StateDir: dir, RelayURL: f.http.URL, SessionLabel: label,
		FilePermissions: true, RoutingKey: routing,
	})
	if err != nil {
		t.Fatalf("initialize temporary synthetic daemon failed: code=%s cause_type=%T", localipc.PublicError(err).Code, err)
	}
	if initialized.Binding != "test_session" || initialized.Capabilities.AttachExisting || initialized.Capabilities.WakeIdle {
		t.Fatal("synthetic preview was represented as native binding")
	}
	d, err := daemon.Open(t.Context(), daemon.Options{StateDir: dir, HTTPClient: f.http.Client()})
	if err != nil {
		t.Fatalf("open temporary synthetic daemon failed: code=%s cause_type=%T", localipc.PublicError(err).Code, err)
	}
	t.Cleanup(func() {
		if closeErr := d.Close(); closeErr != nil {
			t.Error("close temporary synthetic daemon failed")
		}
	})
	value, err := d.Dispatch(t.Context(), localipc.Request{Method: "invite", Session: label, Scope: "coauthor"})
	if err != nil {
		t.Fatalf("initial nonadmin publication failed: code=%s cause_type=%T HTTP=%v", localipc.PublicError(err).Code, err, f.events())
	}
	var issued struct {
		URL       string `json:"url"`
		ExpiresAt int64  `json:"expires_at"`
	}
	raw, err := json.Marshal(value)
	if err != nil || json.Unmarshal(raw, &issued) != nil {
		t.Fatal("unexpected public invitation response")
	}
	link, err := invite.ParseLink(issued.URL)
	if err != nil {
		t.Fatal("initial invitation link was invalid")
	}
	before := f.publication(t)
	if before.id != link.ID() || before.expires != issued.ExpiresAt*int64(time.Second) || before.owner != public(routing) {
		t.Fatal("initial publication changed its ID, owner or absolute TTL")
	}
	f.assertDurableState(t, before, routing)
	if got := f.count(t, "SELECT COUNT(*) FROM nonces"); got != 1 {
		t.Fatalf("initial publication used %d durable nonces, want 1", got)
	}
	// Keep live time through initial admission; freezing before identity setup can
	// reject an otherwise valid 600-second invitation at the TTL boundary.
	f.clock.Store(time.Now().UnixNano())
	for cycle := 1; cycle <= 8; cycle++ {
		f.clock.Add(int64(2 * time.Second))
		offset := len(f.events())
		if err := d.Sync(t.Context()); err != nil {
			t.Errorf("idle Sync %d returned an error", cycle)
		}
		value, err := d.Dispatch(t.Context(), localipc.Request{Method: "status", Session: label})
		status, ok := value.(daemon.Status)
		if err != nil || !ok {
			t.Fatal("public daemon status was unavailable")
		}
		if len(status.OnboardingErrors) != 0 {
			t.Errorf("idle Sync %d recorded OnboardingErrors; quota=%t", cycle, slices.Contains(status.OnboardingErrors, "quota"))
		}
		events := f.events()[offset:]
		for _, endpoint := range []string{"/v1/invites/{invite}", "/v1/mailboxes/{mailbox}/messages"} {
			if idleFirstLinkCount(events, "GET", endpoint, 200) != 1 {
				t.Errorf("idle Sync %d stopped successful GET %s; HTTP=%v", cycle, endpoint, events)
			}
		}
		f.assertDurableState(t, before, routing)
	}
	events := f.events()
	t.Logf("idle publication POST attempts=%d; HTTP=%v", idleFirstLinkCount(events, "POST", "/v1/invites/{invite}", 0), events)
	if idleFirstLinkCount(events, "POST", "/v1/invites/{invite}", 0) != 1 {
		t.Error("acknowledged unused invitation repeated its publication POST")
	}
	if f.count(t, "SELECT COUNT(*) FROM nonces") != 1 {
		t.Error("idle claim polling consumed additional durable publication nonces")
	}
	if idleFirstLinkCount(events, "POST", "/v1/mailboxes", 0) != 0 {
		t.Error("first-link flow attempted manual mailbox enrollment")
	}
	// This runs after observing the eight idle Sync calls. Exact signed replays
	// independently keep the default cap at eight; increasing it cannot hide RED.
	used := f.count(t, "SELECT COUNT(*) FROM nonces")
	if used < 1 || used > 8 {
		t.Fatalf("idle publication retained %d durable nonces, outside default budget 1..8", used)
	}
	for ; used < 8; used++ {
		if code, _ := f.replay(t, link.ID(), routing); code != 201 {
			t.Fatalf("exact replay below default nonce cap: POST /v1/invites/{invite} status=%d", code)
		}
	}
	if code, quota := f.replay(t, link.ID(), routing); code != 429 || !quota {
		t.Fatalf("default ninth nonce must refuse: POST /v1/invites/{invite} status=%d quota=%t", code, quota)
	}
	if f.count(t, "SELECT COUNT(*) FROM nonces") != 8 {
		t.Error("refused ninth nonce changed the default durable nonce budget")
	}
	f.assertDurableState(t, before, routing)
}

func idleFirstLinkStateDir(t *testing.T) string {
	t.Helper()
	// Initialize's private store rejects symlink ancestors, including macOS /var.
	canonical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize private synthetic fixture directory failed: code=%s cause_type=%T", localipc.PublicError(err).Code, err)
	}
	return filepath.Join(canonical, "state")
}

type idleFirstLinkEvent struct {
	method, endpoint string
	status           int
}

type idleFirstLinkRelay struct {
	http            *httptest.Server
	cfg             config.Config
	db              *sql.DB
	clock           atomic.Int64
	mu              sync.Mutex
	trace           []idleFirstLinkEvent
	publicationBody []byte
}

func newIdleFirstLinkRelay(t *testing.T) *idleFirstLinkRelay {
	t.Helper()
	f := &idleFirstLinkRelay{http: httptest.NewUnstartedServer(nil), cfg: config.Defaults()}
	f.cfg.AdmissionMode = config.AdmissionSelfService
	f.cfg.AdminKeys = []string{public(key(t))}
	f.cfg.DataDir = filepath.Join(t.TempDir(), "relay")
	f.cfg.PublicAuthority = f.http.Listener.Addr().String()
	f.cfg.TrustedProxies = []string{"127.0.0.1/32"}
	if f.cfg.Limits != config.Defaults().Limits || f.cfg.SelfService != config.Defaults().SelfService {
		t.Fatal("fixture changed default resource or self-service limits")
	}
	s, err := newWithClock(f.cfg, func() time.Time {
		if now := f.clock.Load(); now != 0 {
			return time.Unix(0, now).UTC()
		}
		return time.Now().UTC()
	})
	if err != nil {
		f.http.Close()
		t.Fatal("open temporary real relay failed")
	}
	f.http.Config.Handler = f.observe(s.Handler())
	f.http.StartTLS()
	t.Cleanup(func() {
		f.http.Close()
		if closeErr := s.Close(context.Background()); closeErr != nil {
			t.Error("close temporary real relay failed")
		}
	})
	dsn := (&url.URL{Scheme: "file", Path: filepath.Join(f.cfg.DataDir, "relay.sqlite"), RawQuery: "mode=ro"}).String()
	f.db, err = sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal("open temporary read-only relay observer failed")
	}
	f.db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if closeErr := f.db.Close(); closeErr != nil {
			t.Error("close temporary relay observer failed")
		}
	})
	return f
}

func (f *idleFirstLinkRelay) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint := "/unexpected"
		switch {
		case r.URL.Path == "/.well-known/peerturn":
			endpoint = "/.well-known/peerturn"
		case r.URL.Path == "/v1/mailboxes":
			endpoint = "/v1/mailboxes"
		case strings.HasPrefix(r.URL.Path, "/v1/mailboxes/") && strings.HasSuffix(r.URL.Path, "/messages"):
			endpoint = "/v1/mailboxes/{mailbox}/messages"
		case strings.HasPrefix(r.URL.Path, "/v1/invites/"):
			endpoint = "/v1/invites/{invite}"
		}
		var publication bytes.Buffer
		if r.Method == "POST" && endpoint == "/v1/invites/{invite}" {
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.TeeReader(r.Body, &publication), r.Body}
		}
		response := &idleFirstLinkResponse{ResponseWriter: w}
		next.ServeHTTP(response, r)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.trace = append(f.trace, idleFirstLinkEvent{r.Method, endpoint, response.status})
		if len(f.publicationBody) == 0 && publication.Len() > 0 && response.status == 201 {
			f.publicationBody = append([]byte(nil), publication.Bytes()...)
		}
	})
}

type idleFirstLinkResponse struct {
	http.ResponseWriter
	status int
}

func (w *idleFirstLinkResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *idleFirstLinkResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(body)
}

func (f *idleFirstLinkRelay) events() []idleFirstLinkEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]idleFirstLinkEvent(nil), f.trace...)
}

func idleFirstLinkCount(events []idleFirstLinkEvent, method, endpoint string, status int) int {
	count := 0
	for _, e := range events {
		if e.method == method && e.endpoint == endpoint && (status == 0 || e.status == status) {
			count++
		}
	}
	return count
}

type idleFirstLinkPublication struct {
	id, owner, proof        string
	created, expires        int64
	packet, digest, voucher []byte
}

func (f *idleFirstLinkRelay) publication(t *testing.T) idleFirstLinkPublication {
	t.Helper()
	var p idleFirstLinkPublication
	err := f.db.QueryRowContext(t.Context(), "SELECT id,inviter_key,proof_key,created_ns,expires_ns,packet,digest,voucher_hash FROM invites").
		Scan(&p.id, &p.owner, &p.proof, &p.created, &p.expires, &p.packet, &p.digest, &p.voucher)
	if err != nil {
		t.Fatal("read temporary publication failed")
	}
	return p
}

func (f *idleFirstLinkRelay) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	if err := f.db.QueryRowContext(t.Context(), query, args...).Scan(&count); err != nil {
		t.Fatal("read temporary relay counts failed")
	}
	return count
}

func (f *idleFirstLinkRelay) assertDurableState(t *testing.T, before idleFirstLinkPublication, signer ed25519.PrivateKey) {
	t.Helper()
	for _, query := range []string{
		"SELECT COUNT(*) FROM relay_inviters WHERE revoked=0",
		"SELECT COUNT(*) FROM relay_inviters",
		"SELECT COUNT(*) FROM mailboxes",
		"SELECT COUNT(*) FROM invites",
		"SELECT COUNT(*) FROM invites WHERE revoked=0 AND claim_id IS NULL AND claim_reader IS NULL AND claim_packet IS NULL AND enrolled_mailbox IS NULL",
	} {
		if count := f.count(t, query); count != 1 {
			t.Errorf("idle first link changed registry/mailbox/unused-invite cardinality: got %d, want 1", count)
		}
	}
	if f.count(t, "SELECT COUNT(*) FROM mailboxes WHERE id=? AND read_key=?", selfServiceMailbox(signer), public(signer)) != 1 {
		t.Error("idle first link lost its signer-derived own mailbox")
	}
	if !reflect.DeepEqual(before, f.publication(t)) {
		t.Error("idle first link changed the original ID/packet/absolute TTL or creation metadata")
	}
}

func (f *idleFirstLinkRelay) replay(t *testing.T, id string, signer ed25519.PrivateKey) (int, bool) {
	t.Helper()
	f.mu.Lock()
	body := append([]byte(nil), f.publicationBody...)
	f.mu.Unlock()
	if len(body) == 0 {
		t.Fatal("missing synthetic publication replay bytes")
	}
	r, err := http.NewRequestWithContext(t.Context(), "POST", f.http.URL+"/v1/invites/"+id, bytes.NewReader(body))
	if err != nil {
		t.Fatal("create exact synthetic replay request failed")
	}
	r.Header.Set("Content-Type", "application/json")
	now := time.Now()
	if signErr := auth.Sign(r, body, signer, now, now.Add(30*time.Second), opaqueID(t)); signErr != nil {
		t.Fatal("sign exact synthetic replay failed")
	}
	response, err := f.http.Client().Do(r)
	if err != nil {
		t.Fatal("send exact synthetic replay failed")
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	if closeErr := response.Body.Close(); readErr != nil || closeErr != nil {
		t.Fatal("read exact synthetic replay response failed")
	}
	var problem struct {
		Error string `json:"error"`
	}
	return response.StatusCode, json.Unmarshal(data, &problem) == nil && problem.Error == "quota"
}
