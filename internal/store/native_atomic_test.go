package store

import (
	"bytes"
	"errors"
	"testing"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func nativeNoticeFixture(t *testing.T, s *Store, f *testFixture, r protocol.ReceiptPayload) NativeNotification {
	t.Helper()
	o := envelope.OpenOptions{RecipientChain: f.sender.chain, RecipientPin: f.sender.cert.IdentityID, SenderChain: f.recipient.chain, SenderPin: f.recipient.cert.IdentityID, SignedGrant: f.reverse, CurrentGrantEpoch: 1, Now: testNow}
	if err := s.InstallGrant(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	wire, err := f.receipt(r)
	if err != nil {
		t.Fatal(err)
	}
	return NativeNotification{GrantID: "grant_reverse", Packet: wire}
}

func TestNativeResultAndSignedNoticeCommitAtomically(t *testing.T) {
	for _, kind := range []protocol.Kind{protocol.Coauthor, protocol.Inform} {
		t.Run(string(kind), func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			f.message.Kind = kind
			commitOne(t, s, f, 1)
			r := protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "adapter_accepted", EvidenceKind: "native_queue", NativeInputID: "local-only", At: testNow}
			if kind == protocol.Inform {
				r.Stage, r.Outcome, r.EvidenceKind = "offered", "offered", "hook_notice"
			} else if _, err := s.DeliveryIntent(t.Context(), r.MessageID, r.AttemptID, testNow); err != nil {
				t.Fatal(err)
			} else if err := s.AuthorizeDelivery(t.Context(), r.MessageID, r.AttemptID, testNow); err != nil {
				t.Fatal(err)
			}
			notice := nativeNoticeFixture(t, s, f, r)
			packet, err := protocol.DecodePacket(notice.Packet)
			if err != nil {
				t.Fatal(err)
			}
			commit := func() error {
				if kind == protocol.Inform {
					return s.InformResults(t.Context(), []InformResult{{Receipt: r, Notification: &notice}})
				}
				return s.DeliveryResult(t.Context(), r.AttemptID, r, []byte("queue-evidence"), notice)
			}
			injected := errors.New("crash before atomic native notice commit")
			s.abort = func(step string) error {
				if step == "before_commit" {
					return injected
				}
				return nil
			}
			if err = commit(); !errors.Is(err, injected) {
				t.Fatalf("native commit: %v", err)
			}
			s.abort = nil
			if exists, existsErr := s.OutboxExists(t.Context(), packet.Header.MessageID); existsErr != nil || exists {
				t.Fatalf("partial outbox: %v %v", exists, existsErr)
			}
			before, err := s.Receipt(t.Context(), r.MessageID)
			if err != nil || before.Stage != "receiver_stored" {
				t.Fatalf("partial receipt: %+v %v", before, err)
			}
			if err = commit(); err != nil {
				t.Fatal(err)
			}
			wire, err := s.OutboxPacket(t.Context(), packet.Header.MessageID)
			if err != nil || !bytes.Equal(wire, notice.Packet) {
				t.Fatalf("receipt wire lost: %v", err)
			}
			after, err := s.Receipt(t.Context(), r.MessageID)
			if err != nil || after.Stage != r.Stage || after.NativeInputID != "local-only" {
				t.Fatalf("native receipt lost: %+v %v", after, err)
			}
		})
	}
}
