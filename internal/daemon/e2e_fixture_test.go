package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relay"
	"github.com/temirlankasen/peerturn/internal/relay/config"
)

type trackedListener struct {
	net.Listener
	mu          sync.Mutex
	connections []net.Conn
}

func (l *trackedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.connections = append(l.connections, c)
		l.mu.Unlock()
	}
	return c, err
}
func (l *trackedListener) drop(t *testing.T) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.connections {
		if err := c.Close(); err != nil && !os.IsTimeout(err) {
			continue
		}
	}
	l.connections = nil
}

type relayFixture struct {
	server   *httptest.Server
	listener *trackedListener
	current  atomic.Pointer[relay.Server]
	offline  atomic.Bool
	events   atomic.Int64
	cfg      config.Config
}

func newRelayFixture(t *testing.T, admin ed25519.PrivateKey) *relayFixture {
	t.Helper()
	f := &relayFixture{}
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events" {
			f.events.Add(1)
		}
		s := f.current.Load()
		if f.offline.Load() || s == nil {
			w.WriteHeader(503)
			return
		}
		s.Handler().ServeHTTP(w, r)
	}))
	f.listener = &trackedListener{Listener: f.server.Listener}
	f.server.Listener = f.listener
	f.cfg = config.Defaults()
	f.cfg.DataDir = stateDir(t)
	f.cfg.PublicAuthority = f.listener.Addr().String()
	f.cfg.AdminKeys = []string{publicKey(admin[32:])}
	f.cfg.TrustedProxies = []string{"127.0.0.1/32"}
	f.cfg.Limits.RateBurst = 100000
	f.cfg.Limits.RatePerMinute = 100000
	f.restart(t)
	f.server.StartTLS()
	t.Cleanup(func() {
		f.server.Close()
		if s := f.current.Swap(nil); s != nil {
			if err := s.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}
	})
	return f
}

func (f *relayFixture) restart(t *testing.T) {
	t.Helper()
	old := f.current.Swap(nil)
	if old != nil {
		if err := old.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	next, err := relay.New(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.current.Store(next)
}

type daemonFixture struct {
	dir, runtime, label string
	cancel              context.CancelFunc
	done                chan error
	client              localipc.Client
	relay               *relayFixture
}

func newDaemonFixture(t *testing.T, f *relayFixture, label string, key ed25519.PrivateKey) *daemonFixture {
	t.Helper()
	runtimeDir := filepath.Join(fixtureDir(t), "r")
	if err := os.Mkdir(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	d := &daemonFixture{dir: stateDir(t), runtime: runtimeDir, label: label, relay: f, client: localipc.Client{RuntimeDir: runtimeDir}}
	if _, err := Initialize(t.Context(), InitOptions{StateDir: d.dir, RelayURL: f.server.URL, SessionLabel: label, FilePermissions: true, RoutingKey: key}); err != nil {
		t.Fatal(err)
	}
	d.start(t)
	t.Cleanup(func() { d.stop(t) })
	return d
}

func (d *daemonFixture) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	d.cancel = cancel
	d.done = make(chan error, 1)
	go func() {
		d.done <- Run(ctx, Options{StateDir: d.dir, RuntimeDir: d.runtime, HTTPClient: d.relay.server.Client()})
	}()
	eventually(t, func() bool {
		select {
		case runErr := <-d.done:
			d.cancel = nil
			t.Fatalf("daemon exited before ready: %v; runtime length %d", runErr, len(d.runtime))
		default:
		}
		_, err := d.client.Call(t.Context(), localipc.Request{Method: "status", Session: d.label})
		return err == nil
	})
}

func (d *daemonFixture) stop(t *testing.T) {
	t.Helper()
	if d.cancel == nil {
		return
	}
	d.cancel()
	if err := <-d.done; err != nil {
		t.Error(err)
	}
	d.cancel = nil
}

func (d *daemonFixture) call(t *testing.T, r localipc.Request, target any) {
	t.Helper()
	r.Session = d.label
	raw, err := d.client.Call(t.Context(), r)
	if err != nil {
		t.Fatalf("%s: %v", r.Method, err)
	}
	if target != nil {
		if err = json.Unmarshal(raw, target); err != nil {
			t.Fatalf("%s decode: %v", r.Method, err)
		}
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(25 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("condition did not become true")
		case <-t.Context().Done():
			t.Fatal("test canceled")
		case <-tick.C:
		}
	}
}

func routingKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func assertPrivateState(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"client.sealed", "seal.key", "state.db"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private state %s: %v", name, err)
		}
	}
}
