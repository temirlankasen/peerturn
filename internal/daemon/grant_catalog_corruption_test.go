package daemon

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

func TestLocalRevokeSurvivesForeignReceiptCorruption(t *testing.T) {
	parallelDaemonTest(t)
	a, b := pairedStores(t)
	c, _ := pairedStores(t)
	attachPair(t, a, c)
	input := incomingData(t, a, c)
	raw, err := codec.Canonical(input.Packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.OutboxEnqueue(t.Context(), store.OutboxRequest{Packet: raw, Message: input.Message, GrantID: input.Message.GrantID, Now: unixNow()}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(a.options.StateDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(t.Context(), `UPDATE receipts SET evidence=X'00' WHERE direction='outbound' AND grant_id=?`, input.Message.GrantID)
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("corrupt receipt fixture: %v %v", err, closeErr)
	}
	// A previously authenticated revoke can leave this peer's receipts unread.
	a.state.Peers[peerID(c.state.Chain)].Revoked = true
	recoveryRelayOffline(t, a)
	g, err := a.incomingGrant(a.state.Peers[peerID(b.state.Chain)])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Dispatch(t.Context(), localipc.Request{Method: "revoke", Session: "explicit", GrantID: g.GrantID}); err != nil {
		t.Fatalf("foreign receipt corruption blocked local revoke: %v", err)
	}
	if !a.state.Peers[peerID(b.state.Chain)].IncomingRevoked {
		t.Fatal("local revoke was not committed")
	}
	if _, err = a.Dispatch(t.Context(), localipc.Request{Method: "grants", Session: "explicit"}); !fault.IsCode(err, store.SealingFailed) {
		t.Fatalf("effective grant catalog hid corrupt evidence: %v", err)
	}
}
