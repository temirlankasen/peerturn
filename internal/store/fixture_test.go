package store

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

const testNow int64 = 1800000000

type testParty struct {
	chain      identity.Chain
	cert       identity.SessionCertificate
	key        ed25519.PrivateKey
	encryption *ecdh.PrivateKey
}

func newTestParty(t *testing.T, id string) testParty {
	t.Helper()
	rootPub, root, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	devicePub, device, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ek, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b := identity.Binding{IdentityID: identity.RootFingerprint(rootPub), DeviceID: id + "_device", DeviceEpoch: 1, SessionID: id, SessionEpoch: 1, KeyEpoch: 1, SigningKey: pub}
	dc, err := identity.IssueDevice(identity.DeviceCertificate{Version: 1, IdentityID: b.IdentityID, DeviceID: b.DeviceID, DeviceEpoch: 1, SigningKey: devicePub, NotBefore: testNow - 10, ExpiresAt: testNow + 7200}, root)
	if err != nil {
		t.Fatal(err)
	}
	cert := identity.SessionCertificate{Binding: b, Version: 1, EncryptionKey: ek.PublicKey().Bytes(), NotBefore: testNow - 10, ExpiresAt: testNow + 3600}
	sc, err := identity.IssueSession(cert, device)
	if err != nil {
		t.Fatal(err)
	}
	return testParty{identity.Chain{RootPublicKey: rootPub, Device: dc, Session: sc}, cert, key, ek}
}

type testFixture struct {
	sender, recipient testParty
	header            protocol.RoutingHeader
	message           protocol.MessagePayload
	options           envelope.OpenOptions
	reverse           string
}

func fixture(t *testing.T, s *Store) *testFixture {
	t.Helper()
	f := &testFixture{sender: newTestParty(t, "sender"), recipient: newTestParty(t, "recipient")}
	g := grant.Grant{Version: 1, GrantID: "grant_one", Issuer: f.recipient.cert.Binding, Subject: f.sender.cert.Binding, Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: testNow - 5, ExpiresAt: testNow + 600, Limits: grant.DefaultLimits(), ReplyRoute: &f.sender.cert.Binding}
	signed, err := grant.Issue(g, f.recipient.key)
	if err != nil {
		t.Fatal(err)
	}
	g.GrantID = "grant_reverse"
	g.Issuer, g.Subject = f.sender.cert.Binding, f.recipient.cert.Binding
	g.ReplyRoute = &f.recipient.cert.Binding
	f.reverse, err = grant.Issue(g, f.sender.key)
	if err != nil {
		t.Fatal(err)
	}
	f.header = protocol.RoutingHeader{WireVersion: 1, Suite: protocol.Suite, DstMailbox: "mailbox_b", RoutePermitID: "permit_a", MessageID: strings.Repeat("a", 32), RecipientKeyEpoch: 1, ExpiresAt: testNow + 300}
	f.message = protocol.MessagePayload{MessageID: f.header.MessageID, FromSession: "sender", FromSessionEpoch: 1, ToSession: "recipient", ToSessionEpoch: 1, SenderSeq: 1, GrantID: "grant_one", GrantEpoch: 1, GrantHash: grant.Hash(signed), Kind: protocol.Coauthor, BodyUTF8: "unique-store-plaintext-canary @path https://example.invalid", CreatedAt: testNow, ExpiresAt: f.header.ExpiresAt, ConversationID: strings.Repeat("b", 32), BudgetID: "budget_one"}
	f.options = envelope.OpenOptions{RecipientKey: f.recipient.encryption, RecipientChain: f.recipient.chain, RecipientPin: f.recipient.cert.IdentityID, SenderChain: f.sender.chain, SenderPin: f.sender.cert.IdentityID, SignedGrant: signed, CurrentGrantEpoch: 1, Now: testNow, ExpectedMailbox: "mailbox_b", ExpectedPermit: "permit_a"}
	if err = s.InstallGrant(t.Context(), f.options); err != nil {
		t.Fatal(err)
	}
	if err = s.BindSession(t.Context(), f.recipient.cert.Binding, []byte("verified-native-context")); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *testFixture) packet(t *testing.T, seq uint64) []byte {
	t.Helper()
	h, m := f.header, f.message
	m.SenderSeq = seq
	if seq != 1 {
		h.MessageID = fmt.Sprintf("%032x", seq)
		m.MessageID = h.MessageID
		m.ConversationID = fmt.Sprintf("%032x", seq+1000)
	}
	p, err := envelope.Seal(h, m, f.sender.key, f.recipient.encryption.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *testFixture) receipt(r protocol.ReceiptPayload) ([]byte, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	h := f.header
	h.DstMailbox = "mailbox_a"
	h.RoutePermitID = "permit_b"
	h.MessageID = peercrypto.Digest([]byte("receipt/" + r.MessageID + "/" + r.Stage))[:32]
	m := f.message
	m.MessageID = h.MessageID
	m.FromSession, m.ToSession = "recipient", "sender"
	m.GrantID = "grant_reverse"
	m.GrantHash = grant.Hash(f.reverse)
	m.Kind = protocol.Receipt
	m.BodyUTF8 = string(body)
	p, err := envelope.Seal(h, m, f.recipient.key, f.sender.encryption.PublicKey())
	if err != nil {
		return nil, err
	}
	return json.Marshal(p)
}
