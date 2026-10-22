package daemon

import (
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestNotifyRecovery(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "unauthorized_upgrade"
		if malformed {
			name = "malformed_frame"
		}
		t.Run(name, func(t *testing.T) {
			key := routingKey(t)
			f := newRelayFixture(t, key)
			upstream := f.server.Config.Handler
			var calls atomic.Int64
			retried := make(chan struct{})
			attempts := make(chan time.Time, 2)
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/events" {
					switch calls.Add(1) {
					case 1:
						attempts <- time.Now()
						if malformed {
							conn, err := websocket.Accept(w, r, nil)
							if err != nil {
								t.Error(err)
								return
							}
							if err = conn.Write(r.Context(), websocket.MessageText, []byte(`{}`)); err != nil {
								t.Error(err)
							}
							if err = conn.CloseNow(); err != nil {
								return
							}
						} else {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusUnauthorized)
							if _, err := io.WriteString(w, `{"error":"unauthorized"}`); err != nil {
								t.Error(err)
							}
						}
						return
					case 2:
						attempts <- time.Now()
						close(retried)
					}
				}
				upstream.ServeHTTP(w, r)
			})
			d := newDaemonFixture(t, f, "notify-recovery", key)
			d.call(t, localipc.Request{Method: "invite", Scope: "coauthor"}, nil)
			select {
			case <-retried:
			case <-time.After(4 * time.Second):
				t.Fatal("notification subscription stopped after a nonretryable relay error")
			}
			first, second := <-attempts, <-attempts
			if delay := second.Sub(first); delay < time.Second {
				t.Fatalf("notification restart omitted backoff: %v", delay)
			}
		})
	}
}
