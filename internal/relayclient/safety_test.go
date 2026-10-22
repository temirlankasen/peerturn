package relayclient

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPRejectsUnknownDuplicateOversizedAndSecretErrors(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
	}{
		{"unknown success", `{"acked":1,"secret":"do-not-reflect"}`, 200},
		{"duplicate success", `{"acked":0,"acked":1}`, 200},
		{"wrong success type", `{"acked":"1"}`, 200},
		{"duplicate error", `{"error":"forbidden","error":"quota"}`, 403},
		{"unknown error", `{"error":"do-not-reflect"}`, 503},
		{"extra error", `{"error":"forbidden","secret":"do-not-reflect"}`, 403},
		{"oversized error", `{"error":"` + strings.Repeat("x", 5000) + `"}`, 503},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(s.Close)
			c, err := New(Options{URL: s.URL, Key: testKey(t), HTTPClient: s.Client()})
			if err != nil {
				t.Fatal(err)
			}
			err = c.Ack(t.Context(), "box", 1)
			if err == nil || strings.Contains(err.Error(), "do-not-reflect") {
				t.Fatalf("unsafe error: %v", err)
			}
			if p, ok := errors.AsType[*Error](err); !ok || p.Code != "invalid_response" {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestHTTPNeverFollowsRedirects(t *testing.T) {
	var destinationCalls atomic.Int64
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { destinationCalls.Add(1); w.WriteHeader(500) }))
	t.Cleanup(destination.Close)
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(s.Close)
	c, err := New(Options{URL: s.URL, Key: testKey(t), HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Ack(t.Context(), "box", 1); err == nil || destinationCalls.Load() != 0 {
		t.Fatalf("redirect: error %v calls %d", err, destinationCalls.Load())
	}
}

func TestTLSDistrustAndRelayConsistencyMismatchFailBeforeMutation(t *testing.T) {
	key := testKey(t)
	var mutations atomic.Int64
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/.well-known/peerturn" {
			mutations.Add(1)
			w.WriteHeader(500)
			return
		}
		d := Discovery{WireVersions: []int{1}, RelayKey: keyID(key), BackupEpoch: 1, Limits: Limits{CiphertextBytes: 1, BatchItems: 1, MailboxItems: 1, MailboxBytes: 1}}
		if err := json.NewEncoder(w).Encode(d); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(s.Close)
	c, err := New(Options{URL: s.URL, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Ack(t.Context(), "box", 0); err == nil {
		t.Fatal("trusted unknown CA")
	}
	c, err = New(Options{URL: s.URL, Key: key, HTTPClient: s.Client(), ExpectedRelayKey: keyID(testKey(t))})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Ack(t.Context(), "box", 0); err == nil || mutations.Load() != 0 {
		t.Fatalf("consistency mismatch did not fence mutation: %v", err)
	}
	if p, ok := errors.AsType[*Error](err); !ok || p.Code != "relay_key_mismatch" {
		t.Fatalf("consistency error = %v", err)
	}
}

func TestFetchRejectsGapAndInvalidPackets(t *testing.T) {
	for _, messages := range [][]Message{{{Seq: 2, Tombstone: true}}, {{Seq: 1}}, {{Seq: 1, Tombstone: true, Packet: ptrPacket()}}} {
		key := testKey(t)
		s := signedServer(t, key, func(*http.Request) any { return map[string]any{"messages": messages} })
		c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.Fetch(t.Context(), "box", FetchOptions{}); err == nil {
			t.Fatal("accepted malformed custody page")
		}
	}
}

func TestAckZeroRejectsMissingRequiredField(t *testing.T) {
	key := testKey(t)
	s := signedServer(t, key, func(*http.Request) any { return struct{}{} })
	c, err := New(Options{URL: s.URL, Key: key, HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Ack(t.Context(), "box", 0); err == nil {
		t.Fatal("missing ACK proof accepted as zero")
	}
}
