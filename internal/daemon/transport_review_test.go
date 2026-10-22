package daemon

import (
	"crypto/ecdh"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/relayclient"
)

func TestRelayStatusDoesNotPoisonPeer(t *testing.T) {
	for _, status := range []int{400, 403, 409, 410, 413, 426, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			a, b := pairedStores(t)
			id := peerID(b.state.Chain)
			r := incomingData(t, a, b)
			a.state.Reserved = &r
			a.state.Sequences[id]++
			if err := a.save(); err != nil {
				t.Fatal(err)
			}
			if err := a.recoverReserved(t.Context()); err != nil {
				t.Fatal(err)
			}
			attempt := 0
			var bodies [][]byte
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var packet protocol.EncryptedPacket
				if err := json.NewDecoder(req.Body).Decode(&packet); err != nil {
					t.Error(err)
					return
				}
				body, err := json.Marshal(packet)
				if err != nil {
					t.Error(err)
				}
				bodies = append(bodies, body)
				w.Header().Set("Content-Type", "application/json")
				attempt++
				if attempt == 1 {
					w.WriteHeader(status)
					_, err = w.Write([]byte(`{"error":"forbidden"}`))
				} else {
					err = json.NewEncoder(w).Encode(relayclient.Receipt{Stage: "relay_stored", Mailbox: packet.Header.DstMailbox, MessageID: packet.Header.MessageID, Seq: 1})
				}
				if err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client, err := relayclient.New(relayclient.Options{URL: server.URL, Key: routePrivate(a.state.RoutingSeed), HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			a.relay = client
			if err = a.sendPacket(t.Context(), r.Packet); err == nil {
				t.Fatal("relay denial hidden")
			}
			if a.state.Peers[id].Revoked || a.state.Peers[id].SequenceBlocked || len(a.state.Pending) != 1 {
				t.Fatal("bare status permanently poisoned peer or lost retry")
			}
			if err = a.sendPacket(t.Context(), r.Packet); err != nil {
				t.Fatal(err)
			}
			if len(bodies) != 2 || string(bodies[0]) != string(bodies[1]) || len(a.state.Pending) != 0 {
				t.Fatal("retry did not retain identical ciphertext")
			}
		})
	}
}

func TestReplyIgnoresCallerPeerWhenParentBindingIsMissing(t *testing.T) {
	a, b := pairedStores(t)
	_, c := pairedStores(t)
	attachPair(t, a, c)
	packet := incomingData(t, b, a)
	if err := a.receive(t.Context(), packet.Packet, 1); err != nil {
		t.Fatal(err)
	}
	delete(a.state.Peers, peerID(b.state.Chain))
	recoveryRelayOffline(t, a)
	if _, err := a.send(t.Context(), localipc.Request{Method: "reply", Peer: peerID(c.state.Chain), MessageID: packet.Message.MessageID, Body: "wrong recipient"}); safeCode(err) != "not_found" {
		t.Fatalf("reply used unverified request peer: %v", err)
	}
}

func TestLocalRevokeBlocksNewOutgoingData(t *testing.T) {
	a, b := pairedStores(t)
	id := peerID(b.state.Chain)
	g, err := a.incomingGrant(a.state.Peers[id])
	if err != nil {
		t.Fatal(err)
	}
	recoveryRelayOffline(t, a)
	if _, err = a.revoke(t.Context(), g.GrantID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.send(t.Context(), localipc.Request{Method: "send", Peer: id, Kind: "coauthor", Body: "disconnected"}); safeCode(err) != "revoked" {
		t.Fatalf("local revoke still sends: %v", err)
	}
}

func TestExpiredRelaySubmitUsesLocalExpiryWithoutRevokingPeer(t *testing.T) {
	a, b := pairedStores(t)
	id := peerID(b.state.Chain)
	r := incomingData(t, a, b)
	expires := unixNow() + 2
	r.Message.ExpiresAt, r.Packet.Header.ExpiresAt = expires, expires
	key, err := ecdh.X25519().NewPublicKey(b.cert.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	r.Packet, err = envelope.Seal(r.Packet.Header, r.Message, a.signingKey(), key)
	if err != nil {
		t.Fatal(err)
	}
	a.state.Reserved = &r
	a.state.Sequences[id]++
	if err = a.save(); err != nil {
		t.Fatal(err)
	}
	if err = a.recoverReserved(t.Context()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		timer := time.NewTimer(time.Until(time.Unix(expires, 0)))
		defer timer.Stop()
		<-timer.C
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		if _, writeErr := w.Write([]byte(`{"error":"expired_message"}`)); writeErr != nil {
			t.Error(writeErr)
		}
	}))
	defer server.Close()
	a.relay, err = relayclient.New(relayclient.Options{URL: server.URL, Key: routePrivate(a.state.RoutingSeed), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.sendPacket(t.Context(), r.Packet); safeCode(err) != "expired_message" {
		t.Fatalf("lost typed expiry: %v", err)
	}
	receipt, err := a.db.Receipt(t.Context(), r.Message.MessageID)
	if err != nil || receipt.Stage != "expired" || receipt.EvidenceKind == "relay_denied" || a.state.Peers[id].Revoked || !a.state.Peers[id].SequenceBlocked {
		t.Fatalf("expiry misclassified: %+v %v", receipt, err)
	}
}
