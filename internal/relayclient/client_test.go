package relayclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func keyID(key ed25519.PrivateKey) string {
	return base64.RawURLEncoding.EncodeToString(key[ed25519.SeedSize:])
}

func testPacket() protocol.EncryptedPacket {
	return protocol.EncryptedPacket{Header: protocol.RoutingHeader{WireVersion: 1, Suite: protocol.Suite, DstMailbox: "box", RoutePermitID: "permit", MessageID: strings.Repeat("a", 32), RecipientKeyEpoch: 1, ExpiresAt: time.Now().Add(time.Hour).Unix()}, Enc: make([]byte, 32), Ciphertext: make([]byte, 16)}
}

func ptrPacket() *protocol.EncryptedPacket { p := testPacket(); return &p }

func signedServer(t *testing.T, key ed25519.PrivateKey, response func(*http.Request) any) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(nil)
	v, err := auth.NewVerifier(auth.Options{PublicAuthority: s.Listener.Addr().String(), Replay: func(context.Context, string, string, int64) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(io.LimitReader(r.Body, protocol.MaxPacket+1))
		if readErr != nil {
			t.Error(readErr)
			w.WriteHeader(500)
			return
		}
		signer, verifyErr := v.Verify(r.Context(), r, body)
		if verifyErr != nil || signer != keyID(key) {
			t.Errorf("signature failed: %v", verifyErr)
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/mailboxes" || r.URL.Path == "/v1/streams/tickets" || r.Method == "POST" && r.URL.Path == "/v1/invites/AAAAAAAAAAAAAAAAAAAAAA" {
			w.WriteHeader(201)
		}
		if err := json.NewEncoder(w).Encode(response(r)); err != nil {
			t.Error(err)
		}
	})
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func TestEveryEndpointUsesMessageSignature(t *testing.T) {
	key := testKey(t)
	packet := testPacket()
	id := "AAAAAAAAAAAAAAAAAAAAAA"
	ip := invite.Packet{Version: 1, InviteID: id, Enc: make([]byte, 32), Ciphertext: make([]byte, 16)}
	s := signedServer(t, key, func(r *http.Request) any {
		switch {
		case r.URL.Path == "/v1/mailboxes":
			return map[string]string{"mailbox": "box"}
		case strings.HasSuffix(r.URL.Path, "/claims"):
			return map[string]string{"claim_id": id, "stage": "relay_stored"}
		case strings.Contains(r.URL.Path, "/invites/") && r.Method == "POST":
			return map[string]string{"id": id}
		case strings.Contains(r.URL.Path, "/invites/"):
			return map[string]any{"id": id, "proof_key": keyID(key), "packet": ip, "expires_at": time.Now().Add(time.Hour).Unix()}
		case strings.Contains(r.URL.Path, "/permits/"):
			return map[string]int64{"epoch": 2}
		case strings.HasSuffix(r.URL.Path, "/messages") && r.Method == "POST":
			return Receipt{Stage: "relay_stored", Mailbox: "box", MessageID: packet.Header.MessageID, Seq: 1}
		case strings.HasSuffix(r.URL.Path, "/messages"):
			return map[string]any{"messages": []Message{{Seq: 1, Packet: &packet}}}
		case strings.HasSuffix(r.URL.Path, "/ack"):
			return map[string]int64{"acked": 1}
		case strings.HasSuffix(r.URL.Path, "/tickets"):
			return Ticket{Ticket: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), ExpiresAt: time.Now().Add(time.Minute).Unix()}
		default:
			return map[string]bool{"deleted": true}
		}
	})
	c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err = c.EnrollMailbox(ctx, MailboxRequest{ID: "box", ReadKey: keyID(key)}); err != nil {
		t.Fatal(err)
	}
	if err = c.PutPermit(ctx, "box", "permit", Permit{WriterKey: keyID(key), Epoch: 2, ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Send(ctx, packet); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Fetch(ctx, "box", FetchOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = c.Ack(ctx, "box", 1); err != nil {
		t.Fatal(err)
	}
	if err = c.CreateInvite(ctx, InviteRequest{ID: id, ProofKey: keyID(key), Packet: ip, ExpiresAt: time.Now().Add(time.Hour).Unix(), Voucher: strings.Repeat("v", 32)}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetInvite(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ClaimInvite(ctx, id, invite.ClaimPacket{Packet: ip, Proof: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.MintTicket(ctx, "box"); err != nil {
		t.Fatal(err)
	}
	if err = c.RevokePermit(ctx, "box", "permit", 2); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteMailbox(ctx, "box"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectUnsafeConfiguration(t *testing.T) {
	key := testKey(t)
	for _, raw := range []string{"http://relay.example", "https://relay.example/path", "https://user:secret" + "@" + "relay.example", "https://relay.example#secret", "https://Relay.example", "https://relay.example:443", "https://relay.example?secret"} {
		if _, err := New(Options{URL: raw, Key: key}); err == nil {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
	if _, err := New(Options{URL: "https://relay.example", Key: make([]byte, 64)}); err == nil {
		t.Fatal("accepted inconsistent private key")
	}
	transport := &http.Transport{}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.TLSClientConfig.InsecureSkipVerify = true
	if _, err := New(Options{URL: "https://relay.example", Key: key, HTTPClient: &http.Client{Transport: transport}}); err == nil {
		t.Fatal("accepted disabled TLS verification")
	}
}
