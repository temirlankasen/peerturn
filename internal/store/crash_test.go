package store

import (
	"errors"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestCustodyAbortsNeverAcknowledgePartialWrites(t *testing.T) {
	for _, point := range []string{"inbox_insert", "inbox_decision", "inbox_receipt", "before_commit"} {
		t.Run(point, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			packet := f.packet(t, 1)
			injected := errors.New("simulated crash")
			s.abort = func(step string) error {
				if step == point {
					return injected
				}
				return nil
			}
			request := InboxRequest{Packet: packet, Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
			got, err := s.InboxCommit(t.Context(), request)
			if !errors.Is(err, injected) || got.Frontier != 0 {
				t.Fatalf("partial ACK: %+v %v", got, err)
			}
			s.abort = nil
			if _, err = s.Inbox(t.Context(), f.message.MessageID); !fault.IsCode(err, NotFound) {
				t.Fatalf("partial inbox: %v", err)
			}
			if _, err = s.Receipt(t.Context(), f.message.MessageID); !fault.IsCode(err, NotFound) {
				t.Fatalf("partial receipt: %v", err)
			}
			got, err = s.InboxCommit(t.Context(), request)
			if err != nil || got.Duplicate || got.Frontier != 1 {
				t.Fatalf("retry after crash: %+v %v", got, err)
			}
		})
	}
}

func TestInviteAndRevokeAbortTogether(t *testing.T) {
	for _, point := range []string{"revoke_epoch", "revoke_quarantine", "revoke_leases", "before_commit"} {
		t.Run(point, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			commitOne(t, s, f, 1)
			injected := errors.New("simulated crash")
			s.abort = func(step string) error {
				if step == point {
					return injected
				}
				return nil
			}
			if err := s.GrantRevoke(t.Context(), "grant_one", 1, testNow, f.receipt); !errors.Is(err, injected) {
				t.Fatalf("wrong revoke abort: %v", err)
			}
			s.abort = nil
			item, err := s.Inbox(t.Context(), f.message.MessageID)
			if err != nil || item.State != "pending" {
				t.Fatalf("partial quarantine: %+v %v", item, err)
			}
			if _, err = s.DeliveryIntent(t.Context(), f.message.MessageID, "11111111111111111111111111111111", testNow); err != nil {
				t.Fatalf("partial epoch: %v", err)
			}
		})
	}
	s := testStore(t)
	const inviteID = "AAAAAAAAAAAAAAAAAAAAAA"
	if err := s.CreateInvite(t.Context(), inviteID, 100); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("simulated crash")
	s.abort = func(step string) error {
		if step == "invite_claim" {
			return injected
		}
		return nil
	}
	if _, err := s.ConsumeInvite(t.Context(), inviteID, "AAAAAAAAAAAAAAAAAAAAAQ", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []byte("grant result"), 10); !errors.Is(err, injected) {
		t.Fatalf("wrong invite abort: %v", err)
	}
	s.abort = nil
	if _, err := s.ConsumeInvite(t.Context(), inviteID, "AAAAAAAAAAAAAAAAAAAAAg", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", []byte("second winner"), 10); err != nil {
		t.Fatalf("partial invite winner: %v", err)
	}
}

func TestDeliveryResultRollbackRemainsAmbiguous(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	commitOne(t, s, f, 1)
	id, attempt := f.message.MessageID, "11111111111111111111111111111111"
	if _, err := s.DeliveryIntent(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeDelivery(t.Context(), id, attempt, testNow); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("lost result")
	s.abort = func(step string) error {
		if step == "delivery_result" {
			return injected
		}
		return nil
	}
	r := protocol.ReceiptPayload{MessageID: id, AttemptID: attempt, Stage: "adapter_accepted", EvidenceKind: "native_queue", At: testNow}
	if err := s.DeliveryResult(t.Context(), attempt, r, []byte("queue-id")); !errors.Is(err, injected) {
		t.Fatalf("wrong delivery abort: %v", err)
	}
	s.abort = nil
	if err := s.RecoverInDoubt(t.Context(), testNow+1); err != nil {
		t.Fatal(err)
	}
	got, err := s.Receipt(t.Context(), id)
	if err != nil || got.Stage != "in_doubt" {
		t.Fatalf("false acceptance: %+v %v", got, err)
	}
}

func TestReceiptFailureIsTypedAndRollsBack(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	request := InboxRequest{Packet: f.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: func(protocol.ReceiptPayload) ([]byte, error) { return nil, errors.New("sensitive signer diagnostic") }}
	got, err := s.InboxCommit(t.Context(), request)
	if !fault.IsCode(err, ReceiptUnavailable) || got.Frontier != 0 {
		t.Fatalf("receipt failure: %+v %v", got, err)
	}
	if _, err = s.Inbox(t.Context(), f.message.MessageID); !fault.IsCode(err, NotFound) {
		t.Fatalf("partial inbox: %v", err)
	}
}
