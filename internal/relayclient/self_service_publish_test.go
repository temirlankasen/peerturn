package relayclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/invite"
)

func selfServicePublication(key string) InviteRequest {
	const id = "AAAAAAAAAAAAAAAAAAAAAA"
	return InviteRequest{ID: id, ProofKey: key, Packet: invite.Packet{Version: 1, InviteID: id, Enc: make([]byte, 32), Ciphertext: make([]byte, 16)}, ExpiresAt: time.Now().Add(5 * time.Minute).Unix(), Voucher: strings.Repeat("v", 32)}
}

func TestSelfServiceDiscoveryHintDoesNotAuthorizePublicationOrMailbox(t *testing.T) {
	key := testKey(t)
	var mu sync.Mutex
	requests := 0
	s := selfServicePeer(t, key, `"self_service"`, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(403)
		if _, err := io.WriteString(w, `{"error":"forbidden"}`); err != nil {
			t.Error(err)
		}
	})
	c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client(), ExpectedRelayKey: keyID(key)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Discovery(t.Context()); err != nil {
		t.Fatal("advertised self-service must be decodable", err)
	}
	publicationErr := c.CreateInvite(t.Context(), selfServicePublication(keyID(key)))
	_, enrollmentErr := c.EnrollMailbox(t.Context(), MailboxRequest{ID: "own-box", ReadKey: keyID(key)})
	for _, err := range []error{publicationErr, enrollmentErr} {
		if remote, ok := errors.AsType[*Error](err); !ok || remote.Status != 403 || remote.Code != "forbidden" {
			t.Fatalf("discovery hint replaced server authorization: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("expected two separately refused mutations, got %d", requests)
	}
}

func TestSelfServicePublicationExplicitRetryKeepsBodyAndRefreshesSignature(t *testing.T) {
	key := testKey(t)
	input := selfServicePublication(keyID(key))
	input.RequireOwnMailbox = true
	var mu sync.Mutex
	var bodies [][]byte
	var signatures []string
	s := selfServicePeer(t, key, `"self_service"`, func(w http.ResponseWriter, r *http.Request, body []byte) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "POST" || r.URL.Path != "/v1/invites/"+input.ID {
			t.Error("unexpected publication route")
		}
		bodies = append(bodies, append([]byte(nil), body...))
		signatures = append(signatures, r.Header.Get("Signature-Input"))
		if len(bodies) == 1 {
			w.WriteHeader(429)
			if _, err := io.WriteString(w, `{"error":"quota"}`); err != nil {
				t.Error(err)
			}
			return
		}
		w.WriteHeader(201)
		if err := json.NewEncoder(w).Encode(InviteReceipt{ID: input.ID, Mailbox: selfServiceACKMailbox(key[32:])}); err != nil {
			t.Error(err)
		}
	})
	c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client(), ExpectedRelayKey: keyID(key)})
	if err != nil {
		t.Fatal(err)
	}
	if remote, ok := errors.AsType[*Error](c.CreateInvite(t.Context(), input)); !ok || remote.Code != "quota" {
		t.Fatal("publication did not preserve the quota refusal")
	}
	mu.Lock()
	count := len(bodies)
	mu.Unlock()
	if count != 1 {
		t.Fatal("client retried a mutation without its caller")
	}
	if err = c.CreateInvite(t.Context(), input); err != nil {
		t.Fatal("explicit identical retry failed", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) || signatures[0] == signatures[1] {
		t.Fatal("retry changed publication material or replayed its signature nonce")
	}
	selfServiceAssertMailboxRequirement(t, bodies[0], true)
}

func TestSelfServiceDiscoveryHintPreservesRelayKeyMismatchFence(t *testing.T) {
	key := testKey(t)
	s := selfServicePeer(t, key, `"self_service"`, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		t.Error("relay-key mismatch reached a signed mutation")
		w.WriteHeader(500)
	})
	c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client(), ExpectedRelayKey: keyID(testKey(t))})
	if err != nil {
		t.Fatal(err)
	}
	_, discoveryErr := c.Discovery(t.Context())
	publicationErr := c.CreateInvite(t.Context(), selfServicePublication(keyID(key)))
	for _, err := range []error{discoveryErr, publicationErr} {
		if remote, ok := errors.AsType[*Error](err); !ok || remote.Code != "relay_key_mismatch" {
			t.Fatal("admission hint changed the relay consistency fence", err)
		}
	}
}
