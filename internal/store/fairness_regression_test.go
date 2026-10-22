package store

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/grant"
)

func TestRejectedGrantCannotBlockAnotherPeer(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	malicious := InboxRequest{Packet: f.packet(t, 66), Mailbox: "mailbox_b", MailboxSeq: 1, GrantID: "grant_one", Open: f.options, Receipt: f.receipt}
	if got, err := s.InboxCommit(t.Context(), malicious); err != nil || got.Frontier != 1 || got.Stage != "rejected" {
		t.Fatalf("malicious grant held custody: %+v %v", got, err)
	}
	honest := *f
	honest.sender = newTestParty(t, "carol")
	g := grant.Grant{Version: 1, GrantID: "grant_carol", Issuer: f.recipient.cert.Binding, Subject: honest.sender.cert.Binding, Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: testNow - 5, ExpiresAt: testNow + 600, Limits: grant.DefaultLimits(), ReplyRoute: &honest.sender.cert.Binding}
	signed, err := grant.Issue(g, f.recipient.key)
	if err != nil {
		t.Fatal(err)
	}
	honest.options.SenderChain, honest.options.SenderPin, honest.options.SignedGrant = honest.sender.chain, honest.sender.cert.IdentityID, signed
	honest.message.FromSession, honest.message.GrantID, honest.message.GrantHash = "carol", g.GrantID, grant.Hash(signed)
	honest.header.MessageID, honest.message.MessageID = "cccccccccccccccccccccccccccccccc", "cccccccccccccccccccccccccccccccc"
	if err = s.InstallGrant(t.Context(), honest.options); err != nil {
		t.Fatal(err)
	}
	request := InboxRequest{Packet: honest.packet(t, 1), Mailbox: "mailbox_b", MailboxSeq: 2, GrantID: g.GrantID, Open: honest.options, Receipt: f.receipt}
	if got, commitErr := s.InboxCommit(t.Context(), request); commitErr != nil || got.Frontier != 2 || got.Stage != "receiver_stored" {
		t.Fatalf("honest grant blocked: %+v %v", got, commitErr)
	}
	malicious.Open.Now += 120
	if got, retryErr := s.InboxCommit(t.Context(), malicious); retryErr != nil || got.Frontier != 2 || !got.Duplicate {
		t.Fatalf("rejected grant retry: %+v %v", got, retryErr)
	}
}
