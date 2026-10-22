package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

func TestSlowRelayDoesNotBlockOwnerStatus(t *testing.T) {
	for _, operation := range []string{"doctor", "sync", "send"} {
		t.Run(operation, func(t *testing.T) {
			a, b := pairedStores(t)
			started, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				close(started)
				<-release
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			client, err := relayclient.New(relayclient.Options{URL: server.URL, Key: routePrivate(a.state.RoutingSeed), HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			a.relay = client
			a.state.Enrolled = true
			done := make(chan error, 1)
			go func() {
				if operation == "sync" {
					done <- a.Sync(t.Context())
				} else {
					_, callErr := a.Dispatch(t.Context(), localipc.Request{Method: operation, Session: a.state.Label, Peer: peerID(b.state.Chain), Kind: "coauthor", Body: "slow"})
					done <- callErr
				}
			}()
			<-started
			statusDone := make(chan error, 1)
			go func() {
				_, callErr := a.Dispatch(context.Background(), localipc.Request{Method: "status"})
				statusDone <- callErr
			}()
			select {
			case err = <-statusDone:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Error("owner status blocked by relay network I/O")
			}
			close(release)
			err = <-done
			if operation == "send" && err != nil || operation != "send" && err == nil {
				t.Fatalf("unexpected slow transport outcome: %v", err)
			}
		})
	}
}
