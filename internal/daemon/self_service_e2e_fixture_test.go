package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relay"
	"github.com/temirlankasen/peerturn/internal/relay/config"
)

type selfServiceE2ERelay struct {
	server *httptest.Server
	cfg    config.Config
	db     *sql.DB
}

func newSelfServiceE2ERelay(t *testing.T, operator string, open bool) *selfServiceE2ERelay {
	t.Helper()
	f := &selfServiceE2ERelay{server: httptest.NewUnstartedServer(nil), cfg: config.Defaults()}
	if f.cfg.AdmissionMode != config.AdmissionClosed {
		t.Fatal("default relay admission is not closed")
	}
	if open {
		f.cfg.AdmissionMode = config.AdmissionSelfService
	}
	// This independent operator key never signs a daemon or provisioning request.
	f.cfg.AdminKeys = []string{operator}
	f.cfg.DataDir = stateDir(t)
	f.cfg.PublicAuthority = f.server.Listener.Addr().String()
	// httptest supplies verified TLS; no request supplies proxy headers. Match
	// the existing local fixture's explicit loopback termination configuration.
	f.cfg.TrustedProxies = []string{"127.0.0.1/32"}
	s, err := relay.New(f.cfg)
	if err != nil {
		f.server.Close()
		t.Fatal("open real relay:", err)
	}
	// Every request goes directly to production routing, authentication and SQLite.
	f.server.Config.Handler = s.Handler()
	f.server.StartTLS()
	t.Cleanup(func() {
		f.server.Close()
		if closeErr := s.Close(context.Background()); closeErr != nil {
			t.Error("close real relay:", closeErr)
		}
	})
	dsn := (&url.URL{Scheme: "file", Path: filepath.Join(f.cfg.DataDir, "relay.sqlite"), RawQuery: "mode=ro"}).String()
	f.db, err = sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal("open read-only relay observer:", err)
	}
	f.db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := f.db.Close(); err != nil {
			t.Error("close relay observer:", err)
		}
	})
	return f
}

func openSelfServiceE2EDaemon(t *testing.T, f *selfServiceE2ERelay, label string) *Daemon {
	t.Helper()
	key := routingKey(t)
	if len(f.cfg.AdminKeys) != 1 || slices.Contains(f.cfg.AdminKeys, publicKey(key[32:])) {
		t.Fatal("synthetic daemon must be unrelated to the sole operator admin")
	}
	o := InitOptions{StateDir: stateDir(t), RelayURL: f.server.URL, SessionLabel: label, FilePermissions: true, RoutingKey: key}
	status, err := Initialize(t.Context(), o)
	if err != nil {
		t.Fatal("initialize synthetic daemon:", safeCode(err))
	}
	if status.Binding != "test_session" || status.Capabilities.AttachExisting || status.Capabilities.WakeIdle {
		t.Fatal("synthetic session was represented as native binding")
	}
	d, err := Open(t.Context(), Options{StateDir: o.StateDir, HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal("open synthetic daemon:", safeCode(err))
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error("close synthetic daemon:", safeCode(err))
		}
	})
	if d.native != nil || d.state.Codex != nil || d.state.NativeSessionID != "" {
		t.Fatal("fixture must not attach a Claude or Codex native runtime")
	}
	assertSelfServiceE2EEnrollment(t, d, false)
	return d
}

func selfServiceE2ECall(t *testing.T, d *Daemon, r localipc.Request, target any) {
	t.Helper()
	r.Session = d.state.Label
	value, err := d.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatalf("synthetic daemon %s refused: %s", r.Method, safeCode(err))
	}
	if target == nil {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal("cannot encode daemon result")
	}
	if err = json.Unmarshal(raw, target); err != nil {
		t.Fatal("unexpected daemon result shape")
	}
}

func selfServiceE2ESyncUntil(t *testing.T, a, b *Daemon, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	// Manual scheduling exposes relay custody before fetch/ACK. It still runs the
	// normal durable handshake and transport, with headroom for FULL fsync.
	if err := syncFixturePair(ctx, done,
		func(ctx context.Context) error { return a.syncWithBudget(ctx, 30*time.Second) },
		func(ctx context.Context) error { return b.syncWithBudget(ctx, 30*time.Second) }); err != nil {
		t.Fatal("real relay synchronization failed:", safeCode(err))
	}
}

func assertSelfServiceE2EEnrollment(t *testing.T, d *Daemon, want bool) {
	t.Helper()
	saved, err := loadState(d.options.StateDir, d.sealer)
	if err != nil || d.state.Enrolled != want || saved.Enrolled != want {
		t.Fatal("in-memory and sealed enrollment disagree with the acknowledged path")
	}
}

type selfServiceE2ECounts struct {
	inviters, mailboxes, invites, permits int
}

func assertSelfServiceE2ECounts(t *testing.T, f *selfServiceE2ERelay, want selfServiceE2ECounts) {
	t.Helper()
	var got selfServiceE2ECounts
	err := f.db.QueryRowContext(t.Context(), `SELECT
	 (SELECT COUNT(*) FROM relay_inviters), (SELECT COUNT(*) FROM mailboxes),
	 (SELECT COUNT(*) FROM invites), (SELECT COUNT(*) FROM permits)`).
		Scan(&got.inviters, &got.mailboxes, &got.invites, &got.permits)
	if err != nil {
		t.Fatal("read relay admission counts:", err)
	}
	if got != want {
		t.Fatalf("durable admission counts: got %+v, want %+v", got, want)
	}
}

type selfServiceE2EPublication struct {
	owner, proof            string
	created, expires        int64
	revoked                 bool
	packet, digest, voucher []byte
}

func selfServiceE2EStoredPublication(t *testing.T, f *selfServiceE2ERelay, id string) selfServiceE2EPublication {
	t.Helper()
	var p selfServiceE2EPublication
	err := f.db.QueryRowContext(t.Context(), `SELECT inviter_key,proof_key,created_ns,expires_ns,revoked,packet,digest,voucher_hash
	 FROM invites WHERE id=?`, id).Scan(&p.owner, &p.proof, &p.created, &p.expires, &p.revoked, &p.packet, &p.digest, &p.voucher)
	if err != nil {
		t.Fatal("read durable publication metadata:", err)
	}
	return p
}
