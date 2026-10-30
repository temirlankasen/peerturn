package relayclient

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relay/auth"
)

// This HTTPS peer describes wire behavior, not a deployed relay or native join.
func selfServicePeer(t *testing.T, key ed25519.PrivateKey, hint string, mutation func(http.ResponseWriter, *http.Request, []byte)) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(nil)
	v, err := auth.NewVerifier(auth.Options{PublicAuthority: s.Listener.Addr().String(), Replay: func(context.Context, string, string, int64) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/.well-known/peerturn" {
			body := `{"wire_versions":[1],"relay_key":"` + keyID(key) + `","backup_epoch":1,"limits":{"ciphertext_bytes":65536,"batch_items":10,"mailbox_items":100,"mailbox_bytes":1048576,"long_poll_seconds":0}`
			if hint != "" {
				body += `,"admission_mode":` + hint
			}
			if _, err := io.WriteString(w, body+`}`); err != nil {
				t.Error(err)
			}
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, protocol.MaxPacket+1))
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		signer, err := v.Verify(r.Context(), r, body)
		if err != nil || signer != keyID(key) {
			t.Error("mutation did not carry the publisher's valid signature")
			w.WriteHeader(401)
			return
		}
		mutation(w, r, body)
	})
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func TestSelfServiceDiscoveryOptionalHint(t *testing.T) {
	for _, hint := range []string{"", `"self_service"`} {
		t.Run("hint="+hint, func(t *testing.T) {
			key := testKey(t)
			s := selfServicePeer(t, key, hint, func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(500) })
			c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client()})
			if err != nil {
				t.Fatal(err)
			}
			d, err := c.Discovery(t.Context())
			if err != nil {
				t.Fatalf("valid optional admission hint was refused: %v", err)
			}
			// Assert the public result without requiring a not-yet-declared field.
			wire, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			var exposed map[string]json.RawMessage
			if err = json.Unmarshal(wire, &exposed); err != nil {
				t.Fatal(err)
			}
			if string(exposed["admission_mode"]) != hint {
				t.Fatal("discovery lost the optional admission hint or invented opt-in")
			}
		})
	}
}

func TestSelfServiceDiscoveryRejectsInvalidHintAndRetainsStrictDecoding(t *testing.T) {
	for _, hint := range []string{`"open"`, `""`, `"SELF_SERVICE"`, `" self_service "`, `true`, `1`, `null`, `{}`, `[]`, `"self_service","admission_mode":"self_service"`, `"self_service","unexpected":true`} {
		t.Run(hint, func(t *testing.T) {
			key := testKey(t)
			s := selfServicePeer(t, key, hint, func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(500) })
			c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Discovery(t.Context()); err == nil || err.Error() != "relay: invalid_response" {
				t.Fatalf("invalid discovery must fail closed: %v", err)
			}
		})
	}
}
