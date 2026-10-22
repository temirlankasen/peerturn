package relayclient

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelayErrorsPreserveTypedDenialWithoutInferringRevocation(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"expired_message", 410},
		{"gone", 410},
		{"forbidden", 403},
	} {
		t.Run(tc.code, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if _, err := io.WriteString(w, `{"error":"`+tc.code+`"}`); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(s.Close)
			client, err := New(Options{URL: s.URL, Key: testKey(t), HTTPClient: s.Client()})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Send(t.Context(), testPacket())
			if problem, ok := errors.AsType[*Error](err); !ok || problem.Code != tc.code || problem.Status != tc.status {
				t.Fatalf("relay error = %v; want %d %s", err, tc.status, tc.code)
			}
		})
	}
}
