package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestReplyResolvesDurableOutgoingRoot(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	o := installReturnGrant(t, s, f)
	if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
		t.Fatal(err)
	}
	// Another handle must resolve the committed root without caller memory.
	var seq int
	var name, path string
	if err := s.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path, Options{Sealer: s.sealer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	r := returnReply(t, f, o)
	r.Open.Conversation = &envelope.Conversation{ID: "forged"}
	if _, err = reopened.InboxCommit(t.Context(), r); err != nil {
		t.Fatalf("reply to outgoing root: %v", err)
	}
	item, err := reopened.Inbox(t.Context(), strings.Repeat("c", 32))
	if err != nil || item.Routing != envelope.InboxNoWake || string(item.Body) != "reply to retained outgoing root" {
		t.Fatalf("reply routing/body: %+v %v", item, err)
	}
	// The next incoming message must continue the newer outgoing hop, not the
	// older inbox parent, even though sender sequence counters are directional.
	m := f.message
	m.MessageID, m.SenderSeq, m.ParentID, m.Hop = strings.Repeat("d", 32), 2, strings.Repeat("c", 32), 2
	h := f.header
	h.MessageID = m.MessageID
	raw := sealChainMessage(t, h, m, f.sender, f.recipient)
	if _, err = reopened.OutboxEnqueue(t.Context(), OutboxRequest{Packet: raw, Message: m, GrantID: m.GrantID, Now: testNow}); err != nil {
		t.Fatal(err)
	}
	r = returnReply(t, f, o, func(reply *protocol.MessagePayload) {
		reply.MessageID, reply.SenderSeq = strings.Repeat("e", 32), 2
		reply.ParentID, reply.ReplyTo, reply.Hop = m.MessageID, m.MessageID, 3
	})
	r.MailboxSeq = 2
	if _, err = reopened.InboxCommit(t.Context(), r); err != nil {
		t.Fatalf("alternating chain: %v", err)
	}
}

func TestOutgoingRootRejectsIncomingRootCollision(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	o := installReturnGrant(t, s, f)
	if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
		t.Fatal(err)
	}
	r := returnReply(t, f, o, func(m *protocol.MessagePayload) {
		m.Kind, m.ParentID, m.ReplyTo, m.Hop = protocol.Coauthor, "", "", 0
	})
	if got, err := s.InboxCommit(t.Context(), r); err != nil || got.Stage != "rejected" || got.Reason != fault.ScopeDenied {
		t.Fatalf("incoming root reset outgoing conversation: %+v %v", got, err)
	}
}

func TestOutgoingRootCannotBeUsedByDifferentBinding(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
		t.Fatal(err)
	}
	// Identical public session/device names and epochs still have different
	// roots and signing keys; the old outgoing root belongs to the old binding.
	f.recipient = newTestParty(t, f.recipient.cert.SessionID)
	g := grant.Grant{Version: 1, GrantID: "grant_reverse", Issuer: f.sender.cert.Binding, Subject: f.recipient.cert.Binding, Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: testNow - 5, ExpiresAt: testNow + 600, Limits: grant.DefaultLimits(), ReplyRoute: &f.recipient.cert.Binding}
	var err error
	f.reverse, err = grant.Issue(g, f.sender.key)
	if err != nil {
		t.Fatal(err)
	}
	o := installReturnGrant(t, s, f)
	r := returnReply(t, f, o)
	forged := &envelope.Conversation{ID: f.message.ConversationID, ParentID: f.message.MessageID, BudgetID: f.message.BudgetID, Peer: f.recipient.cert.Binding}
	r.Open.Conversation = forged
	r.Open.LookupConversation = func(string) (*envelope.Conversation, error) { return forged, nil }
	if got, checkErr := s.InboxCommit(t.Context(), r); checkErr != nil || got.Stage != "rejected" || got.Reason != fault.ScopeDenied {
		t.Fatalf("different binding inherited outgoing root: %+v %v", got, checkErr)
	}
}

func TestControlCannotReplaceOutgoingConversationParent(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	o := installReturnGrant(t, s, f)
	if _, err := s.OutboxEnqueue(t.Context(), outgoingRequest(t, f, 1)); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(protocol.ReceiptPayload{MessageID: f.message.MessageID, AttemptID: f.message.MessageID, Stage: "receiver_stored", EvidenceKind: "peer_commit", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	r := returnReply(t, f, o, func(m *protocol.MessagePayload) {
		m.Kind, m.ParentID, m.ReplyTo, m.Hop, m.BodyUTF8 = protocol.Receipt, "", "", 8, string(body)
	})
	if _, err = s.InboxCommit(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	r = returnReply(t, f, o, func(m *protocol.MessagePayload) {
		m.MessageID, m.SenderSeq = strings.Repeat("d", 32), 2
	})
	r.MailboxSeq = 2
	if _, err = s.InboxCommit(t.Context(), r); err != nil {
		t.Fatalf("receipt replaced chain parent: %v", err)
	}
}

func TestOutgoingMetadataValidationRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*OutboxRequest)
		code fault.Code
	}{
		{"missing_message", func(r *OutboxRequest) { r.Message = protocol.MessagePayload{} }, fault.ScopeDenied},
		{"header_id", func(r *OutboxRequest) { r.Message.MessageID = strings.Repeat("d", 32) }, fault.AudienceMismatch},
		{"header_expiry", func(r *OutboxRequest) { r.Message.ExpiresAt-- }, fault.AudienceMismatch},
		{"grant_id", func(r *OutboxRequest) { r.Message.GrantID = "other_grant" }, fault.InvalidGrant},
		{"grant_hash", func(r *OutboxRequest) { r.Message.GrantHash = strings.Repeat("0", 64) }, fault.InvalidGrant},
		{"sender", func(r *OutboxRequest) { r.Message.FromSession = "other_sender" }, fault.AudienceMismatch},
		{"recipient", func(r *OutboxRequest) { r.Message.ToSession = "other_recipient" }, fault.AudienceMismatch},
		{"sender_epoch", func(r *OutboxRequest) { r.Message.FromSessionEpoch++ }, fault.EpochMismatch},
		{"recipient_epoch", func(r *OutboxRequest) { r.Message.ToSessionEpoch++ }, fault.EpochMismatch},
		{"grant_epoch", func(r *OutboxRequest) { r.Message.GrantEpoch++ }, fault.EpochMismatch},
		{"future", func(r *OutboxRequest) { r.Message.CreatedAt++ }, fault.NotYetValid},
		{"before_grant", func(r *OutboxRequest) { r.Message.CreatedAt -= 6 }, fault.InvalidGrant},
		{"expired", func(r *OutboxRequest) { r.Now = r.Message.ExpiresAt }, fault.Expired},
		{"conversation", func(r *OutboxRequest) { r.Message.ConversationID = "unbounded-chain-name" }, fault.InvalidEncoding},
		{"hop", func(r *OutboxRequest) { r.Message.Hop = 9 }, fault.BudgetExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			f := fixture(t, s)
			r := outgoingRequest(t, f, 1)
			tc.edit(&r)
			if _, err := s.OutboxEnqueue(t.Context(), r); !fault.IsCode(err, tc.code) {
				t.Fatalf("outgoing metadata accepted: %v", err)
			}
			var count int
			if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM outbox`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial outgoing custody: %d %v", count, err)
			}
		})
	}
}

func TestOutgoingMetadataCannotChangeOnRetry(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	r := outgoingRequest(t, f, 1)
	if _, err := s.OutboxEnqueue(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*protocol.MessagePayload){
		func(m *protocol.MessagePayload) { m.ConversationID = strings.Repeat("f", 32) },
		func(m *protocol.MessagePayload) { m.BudgetID = "fresh_budget" },
		func(m *protocol.MessagePayload) { m.Hop++ },
	} {
		changed := r
		edit(&changed.Message)
		if _, err := s.OutboxEnqueue(t.Context(), changed); !fault.IsCode(err, fault.Conflict) {
			t.Fatalf("retry changed chain: %v", err)
		}
	}
}

func TestOutgoingBodyIsNotStoredAsPlaintext(t *testing.T) {
	s := testStore(t)
	f := fixture(t, s)
	r := outgoingRequest(t, f, 1)
	if _, err := s.OutboxEnqueue(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	packet, err := s.OutboxPacket(t.Context(), r.Message.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	original, err := protocol.DecodePacket(r.Packet)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := protocol.DecodePacket(packet)
	if err != nil || retained.Header != original.Header || !bytes.Equal(retained.Enc, original.Enc) || !bytes.Equal(retained.Ciphertext, original.Ciphertext) {
		t.Fatalf("original ciphertext changed: %v", err)
	}
	var seq int
	var name, path string
	if err = s.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		file, openErr := openLocalFile(path+suffix, os.O_RDONLY)
		if openErr != nil {
			t.Fatal(openErr)
		}
		raw, readErr := io.ReadAll(file)
		if closeErr := file.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if bytes.Contains(raw, []byte(r.Message.BodyUTF8)) {
			t.Fatalf("plaintext outgoing body in %s", suffix)
		}
	}
}

func outgoingRequest(t *testing.T, f *testFixture, seq uint64) OutboxRequest {
	t.Helper()
	m := f.message
	m.SenderSeq = seq
	if seq != 1 {
		m.MessageID = fmt.Sprintf("%032x", seq)
		m.ConversationID = fmt.Sprintf("%032x", seq+1000)
	}
	return OutboxRequest{Packet: f.packet(t, seq), Message: m, GrantID: m.GrantID, Now: testNow}
}

func installReturnGrant(t *testing.T, s *Store, f *testFixture) envelope.OpenOptions {
	t.Helper()
	o := f.options
	o.RecipientKey, o.RecipientChain, o.RecipientPin = f.sender.encryption, f.sender.chain, f.sender.cert.IdentityID
	o.SenderChain, o.SenderPin = f.recipient.chain, f.recipient.cert.IdentityID
	o.SignedGrant = f.reverse
	o.ExpectedMailbox, o.ExpectedPermit = "mailbox_a", "permit_b"
	if err := s.InstallGrant(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	return o
}

func returnReply(t *testing.T, f *testFixture, o envelope.OpenOptions, edits ...func(*protocol.MessagePayload)) InboxRequest {
	t.Helper()
	h, m := f.header, f.message
	h.MessageID = strings.Repeat("c", 32)
	h.DstMailbox, h.RoutePermitID = o.ExpectedMailbox, o.ExpectedPermit
	m.MessageID, m.FromSession, m.ToSession = h.MessageID, f.recipient.cert.SessionID, f.sender.cert.SessionID
	m.GrantID, m.GrantHash = "grant_reverse", grant.Hash(f.reverse)
	m.Kind, m.ParentID, m.ReplyTo, m.Hop = protocol.Reply, f.message.MessageID, f.message.MessageID, 1
	m.BodyUTF8 = "reply to retained outgoing root"
	for _, edit := range edits {
		edit(&m)
	}
	h.MessageID = m.MessageID
	raw := sealChainMessage(t, h, m, f.recipient, f.sender)
	return InboxRequest{Packet: raw, Mailbox: o.ExpectedMailbox, MailboxSeq: 1, GrantID: m.GrantID, Open: o, Receipt: func(r protocol.ReceiptPayload) ([]byte, error) {
		body, bodyErr := json.Marshal(r)
		if bodyErr != nil {
			return nil, bodyErr
		}
		h, m := f.header, f.message
		h.MessageID = peercrypto.Digest([]byte("outgoing-receipt/" + r.MessageID + "/" + r.Stage))[:32]
		m.MessageID, m.Kind, m.BodyUTF8 = h.MessageID, protocol.Receipt, string(body)
		p, sealErr := envelope.Seal(h, m, f.sender.key, f.recipient.encryption.PublicKey())
		if sealErr != nil {
			return nil, sealErr
		}
		return json.Marshal(p)
	}}
}

func sealChainMessage(t *testing.T, h protocol.RoutingHeader, m protocol.MessagePayload, sender, recipient testParty) []byte {
	t.Helper()
	p, err := envelope.Seal(h, m, sender.key, recipient.encryption.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
