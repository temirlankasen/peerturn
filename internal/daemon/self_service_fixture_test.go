package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

type selfServiceHTTPTrace struct {
	discovery, enroll, publish, poll, spent int
	bodies                                  [][]byte
	signatures                              []string
}

type selfServiceHTTPPublication struct {
	body  []byte
	value relayclient.Invite
}

// A signed wire fixture, not evidence of real relay admission or native delivery.
type selfServiceHTTPPeer struct {
	server       *httptest.Server
	key          ed25519.PrivateKey
	relayKey     string
	mu           sync.Mutex
	hint         string
	admin        bool
	status       int
	code         string
	badACK       string
	dropACK      bool
	beforeACK    func()
	trace        selfServiceHTTPTrace
	publications map[string]selfServiceHTTPPublication
}

func newSelfServiceHTTPPeer(t *testing.T, hint string) *selfServiceHTTPPeer {
	t.Helper()
	f := &selfServiceHTTPPeer{key: routingKey(t), relayKey: publicKey(routingKey(t)[32:]), hint: hint, publications: map[string]selfServiceHTTPPublication{}}
	f.server = httptest.NewUnstartedServer(nil)
	var nonces sync.Map
	v, err := auth.NewVerifier(auth.Options{PublicAuthority: f.server.Listener.Addr().String(), Replay: func(_ context.Context, key, nonce string, _ int64) error {
		if _, loaded := nonces.LoadOrStore(key+":"+nonce, true); loaded {
			return auth.ErrReplay
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/.well-known/peerturn" {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.trace.discovery++
			body := map[string]any{"wire_versions": []int{1}, "relay_key": f.relayKey, "backup_epoch": 1, "limits": relayclient.Limits{CiphertextBytes: 65536, BatchItems: 10, MailboxItems: 100, MailboxBytes: 1048576}}
			if f.hint != "" {
				body["admission_mode"] = json.RawMessage(f.hint)
			}
			selfServiceHTTPReply(t, w, 200, body)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, protocol.MaxPacket+1))
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		signer, err := v.Verify(r.Context(), r, body)
		if err != nil {
			t.Error("request signature or replay nonce was invalid")
			selfServiceHTTPReply(t, w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/mailboxes":
			f.trace.enroll++
			if !f.admin {
				selfServiceHTTPReply(t, w, 403, map[string]string{"error": "forbidden"})
				return
			}
			var input relayclient.MailboxRequest
			if codec.Decode(body, protocol.MaxPacket, &input) != nil || input.Voucher != "" || signer != publicKey(f.key[32:]) || input.ReadKey != signer {
				t.Error("legacy admin enrollment changed authority or added a voucher")
				w.WriteHeader(400)
				return
			}
			selfServiceHTTPReply(t, w, 201, map[string]string{"mailbox": input.ID})
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/invites/"):
			f.publish(t, w, r, signer, body)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/invites/"):
			entry, ok := f.publications[strings.TrimPrefix(r.URL.Path, "/v1/invites/")]
			if !ok {
				selfServiceHTTPReply(t, w, 410, map[string]string{"error": "gone"})
				return
			}
			selfServiceHTTPReply(t, w, 200, entry.value)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/messages"):
			f.trace.poll++
			selfServiceHTTPReply(t, w, 200, map[string]any{"messages": []any{}})
		default:
			t.Error("unexpected HTTP request in invitation fixture")
			w.WriteHeader(404)
		}
	})
	f.server.StartTLS()
	t.Cleanup(f.server.Close)
	return f
}

func (f *selfServiceHTTPPeer) publish(t *testing.T, w http.ResponseWriter, r *http.Request, signer string, body []byte) {
	f.trace.publish++
	f.trace.bodies = append(f.trace.bodies, append([]byte(nil), body...))
	f.trace.signatures = append(f.trace.signatures, r.Header.Get("Signature-Input"))
	if signer != publicKey(f.key[32:]) {
		t.Error("publication used a different routing authority")
		w.WriteHeader(403)
		return
	}
	if f.beforeACK != nil {
		f.beforeACK()
	}
	if f.status != 0 {
		selfServiceHTTPReply(t, w, f.status, map[string]string{"error": f.code})
		return
	}
	var input relayclient.InviteRequest
	id := strings.TrimPrefix(r.URL.Path, "/v1/invites/")
	if codec.Decode(body, protocol.MaxPacket, &input) != nil || input.Packet.InviteID != id {
		t.Error("invalid publication wire body")
		w.WriteHeader(400)
		return
	}
	if old, exists := f.publications[id]; exists {
		previous, oldErr := selfServicePublicationMaterial(old.body)
		current, newErr := selfServicePublicationMaterial(body)
		if oldErr != nil || newErr != nil {
			t.Error("invalid retained publication body")
			w.WriteHeader(400)
			return
		}
		if !bytes.Equal(previous, current) {
			selfServiceHTTPReply(t, w, 409, map[string]string{"error": "conflict"})
			return
		}
	} else {
		f.trace.spent++
		f.publications[id] = selfServiceHTTPPublication{body: append([]byte(nil), body...), value: relayclient.Invite{ID: id, ProofKey: input.ProofKey, Packet: input.Packet, ExpiresAt: input.ExpiresAt}}
	}
	if f.dropACK {
		f.dropACK = false
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("fixture response does not support hijacking")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Error("could not simulate response loss")
			return
		}
		if err = conn.Close(); err != nil {
			t.Error(err)
		}
		return
	}
	if f.badACK != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		// Escape only the request-derived ID. Canonical base64url IDs stay
		// byte-identical, leaving deliberately malformed ACK payloads intact.
		if _, err := io.WriteString(w, strings.ReplaceAll(f.badACK, "$id", html.EscapeString(id))); err != nil {
			t.Error(err)
		}
		return
	}
	ack := map[string]string{"id": id}
	if f.hint == `"self_service"` || input.RequireOwnMailbox {
		ack["mailbox"] = routeMailbox(f.key[32:])
	}
	selfServiceHTTPReply(t, w, 201, ack)
}

func selfServicePublicationMaterial(body []byte) ([]byte, error) {
	var input relayclient.InviteRequest
	if err := codec.Decode(body, protocol.MaxPacket, &input); err != nil {
		return nil, err
	}
	// The requirement is transient and can disappear after a validated receipt.
	// Ciphertext, voucher, proof key and absolute TTL still define the same spend.
	input.RequireOwnMailbox = false
	return codec.Canonical(input)
}

func selfServiceHTTPReply(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Error(err)
	}
}

func (f *selfServiceHTTPPeer) snapshot() selfServiceHTTPTrace {
	f.mu.Lock()
	defer f.mu.Unlock()
	trace := f.trace
	trace.bodies = append([][]byte(nil), trace.bodies...)
	trace.signatures = append([]string(nil), trace.signatures...)
	return trace
}

func openSelfServiceDaemon(t *testing.T, f *selfServiceHTTPPeer, native *nativeFixture) *Daemon {
	t.Helper()
	o := InitOptions{StateDir: stateDir(t), RelayURL: f.server.URL, SessionLabel: "explicit-self-service-fixture", FilePermissions: true, RoutingKey: f.key}
	options := Options{StateDir: o.StateDir, HTTPClient: f.server.Client()}
	if native != nil {
		o.Native, o.CodexThreadID, options.Native = native, native.thread, native
	}
	if _, err := Initialize(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	d, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return d
}

func reopenSelfServiceDaemon(t *testing.T, d *Daemon) *Daemon {
	t.Helper()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), d.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	return reopened
}

func requestSelfServiceInvite(t *testing.T, d *Daemon) error {
	t.Helper()
	_, err := d.Dispatch(t.Context(), localipc.Request{Method: "invite", Session: d.state.Label, Scope: "coauthor"})
	return err
}

func selfServiceJournal(t *testing.T, d *Daemon, enrolled bool) clientState {
	t.Helper()
	saved, err := loadState(d.options.StateDir, d.sealer)
	if err != nil {
		t.Fatal("could not reload existing sealed journal", err)
	}
	if d.state.Enrolled != enrolled || saved.Enrolled != enrolled {
		t.Fatal("enrollment was guessed or was not saved after a valid publication ACK")
	}
	return saved
}
