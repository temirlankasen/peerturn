package daemon

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func TestTwoDaemonsInviteJoinTransportRecoveryAndRevoke(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	a := newDaemonFixture(t, f, "owner-explicit-test-session", admin)
	b := newDaemonFixture(t, f, "colleague-explicit-test-session", routingKey(t))
	var invitation struct {
		URL string `json:"url"`
	}
	a.call(t, localipc.Request{Method: "invite", Scope: "coauthor"}, &invitation)
	if invitation.URL == "" {
		t.Fatal("no one-time link")
	}
	b.call(t, localipc.Request{Method: "join", Scope: "coauthor", Link: invitation.URL}, nil)
	var ap, bp []Peer
	eventually(t, func() bool {
		a.call(t, localipc.Request{Method: "peers"}, &ap)
		b.call(t, localipc.Request{Method: "peers"}, &bp)
		return len(ap) == 1 && len(bp) == 1 && ap[0].State == "connected" && bp[0].State == "connected"
	})
	eventually(t, func() bool {
		var aStatus, bStatus Status
		a.call(t, localipc.Request{Method: "status"}, &aStatus)
		b.call(t, localipc.Request{Method: "status"}, &bStatus)
		return aStatus.Notifications == "online" && bStatus.Notifications == "online"
	})
	canary := "m25-plaintext-canary-4672 @path https://example.invalid is literal"
	send := func(from, to *daemonFixture, peer, body string) string {
		var receipt protocol.ReceiptPayload
		from.call(t, localipc.Request{Method: "send", Peer: peer, Kind: "coauthor", Body: body}, &receipt)
		eventually(t, func() bool {
			from.call(t, localipc.Request{Method: "receipt", MessageID: receipt.MessageID}, &receipt)
			return receipt.Stage == "receiver_stored"
		})
		var inbox InboxPage
		to.call(t, localipc.Request{Method: "inbox"}, &inbox)
		count := 0
		for _, item := range inbox.Messages {
			if item.MessageID == receipt.MessageID {
				count++
				if item.BodyUTF8 != body {
					t.Fatal("body transformed")
				}
			}
		}
		if count != 1 {
			t.Fatalf("admissions %d want 1", count)
		}
		return receipt.MessageID
	}
	message := send(a, b, ap[0].ID, canary)
	send(b, a, bp[0].ID, "reverse-"+canary)
	// Reply resolves its exact peer and conversation from custody, not caller keys.
	var reply protocol.ReceiptPayload
	b.call(t, localipc.Request{Method: "reply", MessageID: message, Body: "bound reply"}, &reply)
	eventually(t, func() bool {
		b.call(t, localipc.Request{Method: "receipt", MessageID: reply.MessageID}, &reply)
		return reply.Stage == "receiver_stored"
	})
	f.restart(t)
	send(a, b, ap[0].ID, "after-restart-"+canary)
	// Drop established HTTP/WSS connections; enqueue while transport is unavailable.
	streams := f.events.Load()
	f.offline.Store(true)
	f.listener.drop(t)
	var queued protocol.ReceiptPayload
	a.call(t, localipc.Request{Method: "send", Peer: ap[0].ID, Kind: "coauthor", Body: "after-drop-" + canary}, &queued)
	if queued.Stage != "sender_stored" {
		t.Fatalf("network loss claimed %s", queued.Stage)
	}
	a.stop(t)
	a.start(t)
	f.offline.Store(false)
	eventually(t, func() bool {
		a.call(t, localipc.Request{Method: "receipt", MessageID: queued.MessageID}, &queued)
		return queued.Stage == "receiver_stored"
	})
	eventually(t, func() bool { return f.events.Load() > streams })
	var inbox InboxPage
	b.call(t, localipc.Request{Method: "inbox"}, &inbox)
	counts := map[string]int{}
	for _, item := range inbox.Messages {
		counts[item.MessageID]++
	}
	if counts[queued.MessageID] != 1 {
		t.Fatal("duplicate admission after reconnect")
	}
	// Local authoritative revoke precedes relay-route revoke; sender sees denial.
	var grants []store.GrantInfo
	b.call(t, localipc.Request{Method: "grants"}, &grants)
	var status Status
	b.call(t, localipc.Request{Method: "status"}, &status)
	revokeID := ""
	for _, g := range grants {
		if g.Issuer.IdentityID == status.Fingerprint {
			revokeID = g.GrantID
			break
		}
	}
	if revokeID == "" {
		t.Fatal("no recipient-issued grant")
	}
	b.call(t, localipc.Request{Method: "revoke", GrantID: revokeID}, nil)
	eventually(t, func() bool {
		a.call(t, localipc.Request{Method: "peers"}, &ap)
		return len(ap) == 1 && ap[0].State == "revoked"
	})
	_, err := a.client.Call(t.Context(), localipc.Request{Method: "send", Session: a.label, Peer: ap[0].ID, Kind: "coauthor", Body: "must reject"})
	if localipc.ExitCode(err) != 2 || safeCode(err) != "revoked" {
		t.Fatalf("revoked send: %v", err)
	}
	_, err = b.client.Call(t.Context(), localipc.Request{Method: "send", Session: b.label, Peer: bp[0].ID, Kind: "coauthor", Body: "revoker must reject"})
	if safeCode(err) != "revoked" {
		t.Fatalf("revoker's outgoing send: %v", err)
	}
	for _, daemon := range []*daemonFixture{a, b} {
		var after InboxPage
		daemon.call(t, localipc.Request{Method: "inbox"}, &after)
		for _, message := range after.Messages {
			if message.BodyUTF8 == "must reject" || message.BodyUTF8 == "revoker must reject" {
				t.Fatal("post-revoke data reached inbox")
			}
		}
	}
	assertPrivateState(t, a.dir)
	assertPrivateState(t, b.dir)
	assertRelayEncrypted(t, f.cfg.DataDir, []string{canary, a.label, b.label, "coauthor", invitation.URL})
}

func assertRelayEncrypted(t *testing.T, dir string, needles []string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "relay.sqlite*"))
	if err != nil || len(files) == 0 {
		t.Fatal("no relay DB scan")
	}
	scan := func(path string) bool {
		root, openErr := os.OpenRoot(filepath.Dir(path))
		if openErr != nil {
			t.Fatal(openErr)
		}
		raw, readErr := root.ReadFile(filepath.Base(path))
		if closeErr := root.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, needle := range needles {
			if bytes.Contains(raw, []byte(needle)) {
				return true
			}
		}
		return false
	}
	for _, path := range files {
		if scan(path) {
			t.Fatalf("plaintext in relay custody: %s", filepath.Base(path))
		}
	}
	control := filepath.Join(t.TempDir(), "positive.sqlite")
	db, err := sql.Open("sqlite", control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), "CREATE TABLE positive(body TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), "INSERT INTO positive VALUES(?)", needles[0]); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if !scan(control) {
		t.Fatal("positive plaintext control was not detected")
	}
}
