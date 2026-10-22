package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNonceAndTicketPersistence(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	path := filepath.Join(t.TempDir(), "data", "relay.db")
	s, err := Open(path, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	seed(t, s, now)
	if operationErr := s.UseNonce(ctx, "writer", "request-nonce", now.Add(time.Second), now); operationErr != nil {
		t.Fatal(operationErr)
	}
	ticket := Ticket{ID: "notify-ticket", MailboxID: "box", ReaderKey: "reader", ExpiresAt: now.Add(time.Minute)}
	if operationErr := s.PutTicket(ctx, ticket, now); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := s.Close(); operationErr != nil {
		t.Fatal(operationErr)
	}
	s, err = Open(path, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if operationErr := s.Close(); operationErr != nil {
			t.Error(operationErr)
		}
	}()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, consumeErr := s.ConsumeTicket(ctx, ticket.ID, now.Add(time.Second))
			results <- consumeErr
		})
	}
	wg.Wait()
	one, two := <-results, <-results
	if (one == nil) == (two == nil) {
		t.Fatalf("want one ticket admission: %v / %v", one, two)
	}
	requireStatus(t, s.UseNonce(ctx, "writer", "request-nonce", now.Add(time.Second), now.Add(119*time.Second)), 409)
	if operationErr := s.UseNonce(ctx, "writer", "request-nonce", now.Add(3*time.Minute), now.Add(121*time.Second)); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := s.RevokePermit(ctx, "box", "reader", "permit", 2); operationErr != nil {
		t.Fatal(operationErr)
	}
	if err := s.CheckStream(ctx, ticket, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, s.CheckReader(ctx, "box", ticket.ID), 403)
	requireStatus(t, s.PutTicket(ctx, Ticket{ID: "too-long", MailboxID: "box", ReaderKey: "reader", ExpiresAt: now.Add(61 * time.Second)}, now), 400)
}

func TestVoucherBoundSingleEnrollmentAndSecretAbsence(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	path := filepath.Join(t.TempDir(), "relay.db")
	s, err := Open(path, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	invite := Invite{ID: "invite", ProofKey: "public-proof", Packet: []byte("opaque-descriptor"), ExpiresAt: now.Add(time.Minute), Voucher: "synthetic-enrollment-capability-unique"}
	if operationErr := s.PutInvite(ctx, invite); operationErr != nil {
		t.Fatal(operationErr)
	}
	claim := Claim{ID: "claim", ReaderKey: "new-reader", Packet: []byte("opaque-claim")}
	if _, operationErr := s.ClaimInvite(ctx, invite.ID, claim, now); operationErr != nil {
		t.Fatal(operationErr)
	}
	if _, operationErr := s.ClaimInvite(ctx, invite.ID, claim, now); operationErr != nil {
		t.Fatal(operationErr)
	}
	claim.Packet = []byte("different-claim")
	_, err = s.ClaimInvite(ctx, invite.ID, claim, now)
	requireStatus(t, err, 409)
	requireStatus(t, s.CreateMailbox(ctx, Mailbox{ID: "wrong", ReadKey: "other-reader"}, invite.Voucher), 403)
	if operationErr := s.CreateMailbox(ctx, Mailbox{ID: "new", ReadKey: "new-reader"}, invite.Voucher); operationErr != nil {
		t.Fatal(operationErr)
	}
	requireStatus(t, s.CreateMailbox(ctx, Mailbox{ID: "second", ReadKey: "new-reader"}, invite.Voucher), 403)
	got, err := s.GetInvite(ctx, invite.ID, now)
	if err != nil || got.Voucher != "" {
		t.Fatalf("public invite leaked voucher: %v", err)
	}
	if operationErr := s.Close(); operationErr != nil {
		t.Fatal(operationErr)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	data, err := root.ReadFile(filepath.Base(path))
	if closeErr := root.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(invite.Voucher)) {
		t.Fatal("enrollment capability persisted in plaintext")
	}
	if !bytes.Contains(data, invite.Packet) {
		t.Fatal("scan positive control did not find stored opaque packet")
	}
}

func TestStreamTicketRechecksMailboxBeforeUse(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	s := newStore(t, Limits{})
	seed(t, s, now)
	ticket := Ticket{ID: "ticket", MailboxID: "box", ReaderKey: "reader", ExpiresAt: now.Add(time.Minute)}
	if operationErr := s.PutTicket(ctx, ticket, now); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := s.DeleteMailbox(ctx, "box", "reader"); operationErr != nil {
		t.Fatal(operationErr)
	}
	_, err := s.ConsumeTicket(ctx, ticket.ID, now)
	requireStatus(t, err, 403)
	requireStatus(t, s.CheckStream(ctx, Ticket{MailboxID: "box", ReaderKey: "reader"}, now), 403)
}
