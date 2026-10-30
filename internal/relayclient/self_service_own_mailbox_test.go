package relayclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestSelfServiceOwnMailboxClientRequiredACK(t *testing.T) {
	key, proof := testKey(t), testKey(t)
	box := selfServiceACKMailbox(key[32:])
	proofBox := selfServiceACKMailbox(proof[32:])
	for _, tc := range []struct {
		name, ack string
		valid     bool
	}{
		{"legacy-id-only", `{"id":"$id"}`, false},
		{"signer-derived-mailbox", `{"id":"$id","mailbox":"$box"}`, true},
		{"proof-key-derived-mailbox", `{"id":"$id","mailbox":` + strconv.Quote(proofBox) + `}`, false},
		{"empty-mailbox", `{"id":"$id","mailbox":""}`, false},
		{"null-mailbox", `{"id":"$id","mailbox":null}`, false},
		{"wrong-mailbox-type", `{"id":"$id","mailbox":true}`, false},
		{"wrong-id", `{"id":"another-slot","mailbox":"$box"}`, false},
		{"missing-id", `{"mailbox":"$box"}`, false},
		{"empty-id", `{"id":"","mailbox":"$box"}`, false},
		{"null-id", `{"id":null,"mailbox":"$box"}`, false},
		{"wrong-id-type", `{"id":true,"mailbox":"$box"}`, false},
		{"duplicate-mailbox", `{"id":"$id","mailbox":"$box","mailbox":"$box"}`, false},
		{"duplicate-id", `{"id":"$id","id":"$id","mailbox":"$box"}`, false},
		{"unknown-field", `{"id":"$id","mailbox":"$box","unknown":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := selfServiceRequiredMailboxPublication(t, keyID(proof))
			var mu sync.Mutex
			var requests [][]byte
			s := selfServicePeer(t, key, `"self_service"`, func(w http.ResponseWriter, r *http.Request, body []byte) {
				mu.Lock()
				requests = append(requests, append([]byte(nil), body...))
				mu.Unlock()
				if r.Method != "POST" || r.URL.Path != "/v1/invites/"+input.ID {
					t.Error("own-mailbox publication used a different endpoint")
				}
				ack := strings.ReplaceAll(strings.ReplaceAll(tc.ack, "$id", input.ID), "$box", box)
				w.WriteHeader(201)
				if _, err := io.WriteString(w, ack); err != nil {
					t.Error("cannot write own-mailbox ACK")
				}
			})
			c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client(), ExpectedRelayKey: keyID(key)})
			if err != nil {
				t.Fatal("cannot create signed HTTPS client")
			}
			if _, err = c.Discovery(t.Context()); err != nil {
				t.Fatal("valid hint was rejected")
			}
			err = c.CreateInvite(t.Context(), input)
			if tc.valid {
				if err != nil {
					t.Fatal("correct signer-derived own-mailbox ACK was rejected")
				}
			} else if remote, ok := errors.AsType[*Error](err); !ok || remote.Code != "invalid_response" {
				t.Fatal("required own-mailbox ACK accepted missing/invalid id or mailbox")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 1 {
				t.Fatal("invalid ACK caused an automatic publication retry")
			}
			selfServiceAssertMailboxRequirement(t, requests[0], true)
		})
	}
}

func TestSelfServiceOwnMailboxClientLegacyRequestOmitsRequirement(t *testing.T) {
	for _, hint := range []string{"", `"self_service"`} {
		t.Run("hint="+hint, func(t *testing.T) {
			key := testKey(t)
			input := selfServicePublication(keyID(testKey(t)))
			var mu sync.Mutex
			var requests [][]byte
			s := selfServicePeer(t, key, hint, func(w http.ResponseWriter, _ *http.Request, body []byte) {
				mu.Lock()
				requests = append(requests, append([]byte(nil), body...))
				mu.Unlock()
				w.WriteHeader(201)
				if err := json.NewEncoder(w).Encode(map[string]string{"id": input.ID}); err != nil {
					t.Error("cannot write legacy publication ACK")
				}
			})
			c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client()})
			if err != nil {
				t.Fatal("cannot create legacy HTTPS client")
			}
			if _, err = c.Discovery(t.Context()); err != nil {
				t.Fatal("valid optional hint was rejected")
			}
			if err = c.CreateInvite(t.Context(), input); err != nil {
				t.Fatal("legacy publication incorrectly required an own-mailbox ACK")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 1 {
				t.Fatal("legacy publication was not sent exactly once")
			}
			selfServiceAssertMailboxRequirement(t, requests[0], false)
		})
	}
}

func TestSelfServiceOwnMailboxClientTypedReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, ack string
		required  bool
		valid     bool
		owned     bool
	}{
		{"required-owned", `{"id":"$id","mailbox":"$box"}`, true, true, true},
		{"optional-owned", `{"id":"$id","mailbox":"$box"}`, false, true, true},
		{"legacy-id-only", `{"id":"$id"}`, false, true, false},
		{"required-missing-mailbox", `{"id":"$id"}`, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := testKey(t)
			input := selfServicePublication(keyID(testKey(t)))
			input.RequireOwnMailbox = tc.required
			box := selfServiceACKMailbox(key[32:])
			var mu sync.Mutex
			var request []byte
			s := selfServicePeer(t, key, "", func(w http.ResponseWriter, _ *http.Request, body []byte) {
				mu.Lock()
				request = append([]byte(nil), body...)
				mu.Unlock()
				w.WriteHeader(201)
				ack := strings.ReplaceAll(strings.ReplaceAll(tc.ack, "$id", input.ID), "$box", box)
				if _, err := io.WriteString(w, ack); err != nil {
					t.Error("cannot write typed publication ACK")
				}
			})
			c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client()})
			if err != nil {
				t.Fatal("cannot create receipt client")
			}
			receipt, err := c.CreateInviteWithReceipt(t.Context(), input)
			mu.Lock()
			body := append([]byte(nil), request...)
			mu.Unlock()
			selfServiceAssertMailboxRequirement(t, body, tc.required)
			if !tc.valid {
				if remote, ok := errors.AsType[*Error](err); !ok || remote.Code != "invalid_response" || receipt != (InviteReceipt{}) {
					t.Fatal("invalid ACK exposed a successful or partial typed receipt")
				}
				return
			}
			want := InviteReceipt{ID: input.ID}
			if tc.owned {
				want.Mailbox = box
			}
			if err != nil || receipt != want {
				t.Fatal("typed publication receipt lost validated ownership or invented a legacy mailbox")
			}
		})
	}
}

func selfServiceRequiredMailboxPublication(t *testing.T, proof string) InviteRequest {
	t.Helper()
	input := selfServicePublication(proof)
	input.RequireOwnMailbox = true
	return input
}

func selfServiceACKMailbox(rawPublic []byte) string {
	digest := sha256.Sum256(rawPublic)
	return hex.EncodeToString(digest[:])
}

func selfServiceAssertMailboxRequirement(t *testing.T, raw []byte, required bool) {
	t.Helper()
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal("signed publication body was not an object")
	}
	value, present := wire["require_own_mailbox"]
	if required && (!present || string(value) != "true") || !required && present {
		t.Fatal("signed publication lost require_own_mailbox=true or legacy omitempty")
	}
}
