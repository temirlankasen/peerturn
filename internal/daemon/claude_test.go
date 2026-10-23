package daemon

import (
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"strings"
	"testing"
)

func bindClaude(t *testing.T, d *Daemon) string {
	t.Helper()
	r := localipc.Request{Method: "adapter.open", NativeSession: "11111111-1111-4111-8111-111111111111", NativeSocket: "/tmp/claude.sock", RuntimeVersion: "2.1.306", Provider: "gateway"}
	value, err := d.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	opened, ok := value.(map[string]string)
	if !ok {
		t.Fatal("invalid open result", value)
	}
	instance := opened["instance"]
	value, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.challenge", NativeSocket: r.NativeSocket})
	if err != nil {
		t.Fatal(err)
	}
	challenged, ok := value.(map[string]string)
	if !ok {
		t.Fatal("invalid challenge result", value)
	}
	nonce := challenged["nonce"]
	_, err = d.Dispatch(t.Context(), localipc.Request{Method: "adapter.confirm", NativeSession: r.NativeSession, Instance: instance, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestClaudeDeliveryIntentAndDropAreNotRetried(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	a, b := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t))
	attachPair(t, a, b)
	instance := bindClaude(t, a)
	p := incomingData(t, b, a)
	if err := a.receive(t.Context(), p.Packet, 1); err != nil {
		t.Fatal(err)
	}
	r := localipc.Request{Method: "adapter.poll", Instance: instance}
	v, err := a.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	live, ok := v.(adapterEvent)
	if !ok {
		t.Fatal("invalid poll result", v)
	}
	if live.Type != "live" || live.MessageID != p.Message.MessageID {
		t.Fatalf("not live: %+v", live)
	}
	r.Method, r.MessageID, r.AttemptID, r.Outcome = "adapter.result", live.MessageID, live.AttemptID, "drop"
	if _, err = a.Dispatch(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	r.Method = "adapter.poll"
	v, err = a.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	event, ok := v.(adapterEvent)
	if !ok || event.Type == "live" {
		t.Fatalf("drop retried: %v %v", v, err)
	}
	receipt, err := a.db.Receipt(t.Context(), live.MessageID)
	if err != nil || receipt.Stage != "rejected" || receipt.ReasonCode != "interrupted" {
		t.Fatalf("receipt %+v %v", receipt, err)
	}
}

func TestClaudeInformNoticeOnlyAndInboxBodyDelivery(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	a, b := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t))
	attachPair(t, a, b)
	instance := bindClaude(t, a)
	p, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], protocol.Inform, "notice", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), p.Packet, 1); err != nil {
		t.Fatal(err)
	}
	v, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	if err != nil {
		t.Fatal(err)
	}
	event, ok := v.(adapterEvent)
	if !ok || event.Type != "status" || event.InformPending != 1 {
		t.Fatalf("inform woke: %v %v", v, err)
	}
	r := boundaryRequest(a, instance)
	v, err = a.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	notice, ok := v.(map[string]string)
	if !ok || notice["context"] == "" || strings.Contains(notice["context"], "untrusted_peer_text") || !strings.Contains(notice["context"], "peerturn inbox") {
		t.Fatalf("missing context: %v %v", v, err)
	}
	receipt, err := a.db.Receipt(t.Context(), p.Message.MessageID)
	if err != nil || receipt.Stage != "receiver_stored" {
		t.Fatalf("offered before attachment: %+v %v", receipt, err)
	}
	r.Method, r.Nonce = "adapter.boundary-attached", notice["notice_id"]
	if _, err = a.Dispatch(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	receipt, err = a.db.Receipt(t.Context(), p.Message.MessageID)
	if err != nil || receipt.Outcome != "offered" {
		t.Fatalf("not offered: %+v %v", receipt, err)
	}
	r.Method = "adapter.boundary"
	v, err = a.Dispatch(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	notice, ok = v.(map[string]string)
	if !ok || notice["context"] != "" {
		t.Fatalf("boundary duplicate: %v %v", v, err)
	}
	v, err = a.Dispatch(t.Context(), localipc.Request{Method: "inbox", Session: a.state.Label})
	page, ok := v.(InboxPage)
	if err != nil || !ok || len(page.Messages) != 1 || page.Messages[0].BodyUTF8 != "notice" {
		t.Fatalf("inbox lost body: %v %v", v, err)
	}
	receipt, err = a.db.Receipt(t.Context(), p.Message.MessageID)
	if err != nil || receipt.Outcome != "offered" {
		t.Fatalf("delivered before output acknowledgement: %+v %v", receipt, err)
	}
	if _, err = a.Dispatch(t.Context(), localipc.Request{Method: "inbox.delivered", Session: a.state.Label, MessageID: p.Message.MessageID}); err != nil {
		t.Fatal(err)
	}
	receipt, err = a.db.Receipt(t.Context(), p.Message.MessageID)
	if err != nil || receipt.Outcome != "delivered" || receipt.EvidenceKind != "inbox_body_returned" {
		t.Fatalf("not delivered: %+v %v", receipt, err)
	}
}

func TestClaudeAskFailsClosedAndBindingDoesNotMove(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	a, b := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t))
	attachPair(t, a, b)
	instance := bindClaude(t, a)
	p, err := b.makePacket(peerID(a.state.Chain), b.state.Peers[peerID(a.state.Chain)], protocol.Ask, "analyze", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.receive(t.Context(), p.Packet, 1); err != nil {
		t.Fatal(err)
	}
	v, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	if err != nil {
		t.Fatal(err)
	}
	event, ok := v.(adapterEvent)
	if !ok || event.Type != "status" {
		t.Fatal("ask became live", v)
	}
	receipt, err := a.db.Receipt(t.Context(), p.Message.MessageID)
	if err != nil || receipt.Stage != "rejected" || receipt.ReasonCode != "authority_file_key" {
		t.Fatal(receipt, err)
	}
	if a.state.AskBudget != nil || len(a.askActive) != 0 {
		t.Fatal("rejected file-backed ask reserved a model run")
	}
	_, err = a.Dispatch(t.Context(), localipc.Request{Method: "adapter.open", NativeSession: "22222222-2222-4222-8222-222222222222", NativeSocket: "/tmp/other.sock", RuntimeVersion: "2.1.306", Provider: "gateway"})
	if localipc.PublicError(err).Code != "needs_rebind" {
		t.Fatal("existing grants moved to new UUID", err)
	}
}

func TestClaudeRevokeClosesPendingAdmission(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	a, b := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t))
	attachPair(t, a, b)
	instance := bindClaude(t, a)
	p := incomingData(t, b, a)
	if err := a.receive(t.Context(), p.Packet, 1); err != nil {
		t.Fatal(err)
	}
	v, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	if err != nil {
		t.Fatal(err)
	}
	event, ok := v.(adapterEvent)
	if !ok || event.Type != "live" {
		t.Fatal("not live", v)
	}
	peer := a.state.Peers[peerID(b.state.Chain)]
	g, err := a.incomingGrant(peer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Dispatch(t.Context(), localipc.Request{Method: "revoke", Session: a.state.Label, GrantID: g.GrantID}); err != nil {
		t.Fatal(err)
	}
	v, err = a.Dispatch(t.Context(), localipc.Request{Method: "adapter.authorize", Instance: instance, MessageID: event.MessageID, AttemptID: event.AttemptID})
	authorized, ok := v.(map[string]bool)
	if err != nil || !ok || authorized["authorized"] {
		t.Fatal("revoked pending input authorized")
	}
}

func TestClaudeUncertainOutcomeFencesFollowingAdmission(t *testing.T) {
	parallelDaemonTest(t)
	admin := routingKey(t)
	f := newRelayFixture(t, admin)
	a, b := openRecoveryDaemon(t, f, admin), openRecoveryDaemon(t, f, routingKey(t))
	attachPair(t, a, b)
	instance := bindClaude(t, a)
	p := incomingData(t, b, a)
	if err := a.receive(t.Context(), p.Packet, 1); err != nil {
		t.Fatal(err)
	}
	v, err := a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	if err != nil {
		t.Fatal(err)
	}
	event, ok := v.(adapterEvent)
	if !ok || event.Type != "live" {
		t.Fatal(v)
	}
	if _, err = a.Dispatch(t.Context(), localipc.Request{Method: "adapter.authorize", Instance: instance, MessageID: event.MessageID, AttemptID: event.AttemptID}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Dispatch(t.Context(), localipc.Request{Method: "adapter.result", Instance: instance, MessageID: event.MessageID, AttemptID: event.AttemptID, Outcome: "in_doubt"}); err != nil {
		t.Fatal(err)
	}
	b.state.Sequences[peerID(a.state.Chain)]++
	second := incomingData(t, b, a)
	if err = a.receive(t.Context(), second.Packet, 2); err != nil {
		t.Fatal(err)
	}
	v, err = a.Dispatch(t.Context(), localipc.Request{Method: "adapter.poll", Instance: instance})
	if err != nil {
		t.Fatal(err)
	}
	event, ok = v.(adapterEvent)
	if !ok || event.Type != "status" {
		t.Fatal("uncertain outcome admitted next input", v)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), a.options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	uncertain, err := reopened.db.UncertainDeliveries(t.Context())
	if err != nil || !uncertain {
		t.Fatal("restart lost ambiguity fence", err)
	}
}
