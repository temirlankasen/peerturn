package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

func TestSyncFlushFailureStillDeliversAndFlushesNativeOutcome(t *testing.T) {
	for _, ambiguity := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "in_doubt"}[ambiguity], func(t *testing.T) {
			a, b, runtime := nativePair(t)
			r := nativeReceipt(t, a, localipc.Request{Method: "send", Peer: a.peers()[0].ID, Kind: "coauthor", Body: "live peer"})
			p := b.state.Peers[peerID(a.state.Chain)]
			body, err := codec.Canonical(protocol.ReceiptPayload{MessageID: r.MessageID, AttemptID: r.MessageID, Stage: "receiver_stored", EvidenceKind: "local_commit", At: unixNow()})
			if err != nil {
				t.Fatal(err)
			}
			reserved, err := b.makePacket(peerID(a.state.Chain), p, protocol.Receipt, string(body), nil)
			if err != nil {
				t.Fatal(err)
			}
			b.state.Reserved = &reserved
			b.state.Sequences[reserved.PeerID]++
			if err = b.save(); err != nil {
				t.Fatal(err)
			}
			if err = b.recoverReserved(t.Context()); err != nil {
				t.Fatal(err)
			}
			frontier, err := b.db.AckFrontier(t.Context(), b.mailbox())
			if err != nil {
				t.Fatal(err)
			}
			after, err := wireFrontier(frontier)
			if err != nil {
				t.Fatal(err)
			}
			messages, err := b.relay.Fetch(t.Context(), b.mailbox(), relayclient.FetchOptions{After: after})
			if err != nil || len(messages) != 1 {
				t.Fatalf("encrypted inbound fixture: %d %v", len(messages), err)
			}
			var attempts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/messages") {
					attempts.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					if _, writeErr := w.Write([]byte(`{"error":"forbidden"}`)); writeErr != nil {
						t.Error(writeErr)
					}
					return
				}
				var response any
				switch {
				case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/messages"):
					response = map[string]any{"messages": messages}
				case strings.HasSuffix(req.URL.Path, "/ack"):
					var ack struct{ Through int64 }
					if decodeErr := json.NewDecoder(req.Body).Decode(&ack); decodeErr != nil {
						t.Error(decodeErr)
					}
					response = map[string]int64{"acked": ack.Through}
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if writeErr := json.NewEncoder(w).Encode(response); writeErr != nil {
					t.Error(writeErr)
				}
			}))
			defer server.Close()
			b.relay, err = relayclient.New(relayclient.Options{URL: server.URL, Key: routePrivate(b.state.RoutingSeed), HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			if ambiguity {
				runtime.failure = "in_doubt"
			}
			err = b.Sync(t.Context())
			if err == nil || !strings.Contains(err.Error(), "forbidden") || ambiguity && !strings.Contains(err.Error(), "in_doubt") {
				t.Fatalf("independent errors lost: %v", err)
			}
			if len(runtime.ids) != 1 || runtime.ids[0] != r.MessageID || attempts.Load() != 2 {
				t.Fatalf("flush failure gated native work: queues=%v flush attempts=%d", runtime.ids, attempts.Load())
			}
			if p.Revoked || p.SequenceBlocked || len(b.state.Pending) == 0 {
				t.Fatal("live peer denial lost retry or changed authority")
			}
		})
	}
}
