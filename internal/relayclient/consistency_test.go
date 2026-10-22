package relayclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelayConsistencyAcceptsUnsignedEchoOverTrustedTLS(t *testing.T) {
	// The fixture retains only the advertised public value, with no relay signing key.
	advertised := keyID(testKey(t))
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var result any = map[string]int{"acked": 0}
		if r.URL.Path == "/.well-known/peerturn" {
			result = Discovery{WireVersions: []int{1}, RelayKey: advertised, BackupEpoch: 1,
				Limits: Limits{CiphertextBytes: 1, BatchItems: 1, MailboxItems: 1, MailboxBytes: 1}}
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(s.Close)
	client, err := New(Options{URL: s.URL, Key: testKey(t), HTTPClient: s.Client(), ExpectedRelayKey: advertised})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Ack(t.Context(), "box", 0); err != nil {
		t.Fatalf("unsigned echo should meet consistency check, not prove key possession: %v", err)
	}
}
