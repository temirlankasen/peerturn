package envelope_test

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

const now int64 = 1800000000

type party struct {
	chain      identity.Chain
	cert       identity.SessionCertificate
	signing    ed25519.PrivateKey
	encryption *ecdh.PrivateKey
}

func newParty(t testing.TB, id string) party {
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
	dc, err := identity.IssueDevice(identity.DeviceCertificate{Version: 1, IdentityID: b.IdentityID, DeviceID: b.DeviceID, DeviceEpoch: 1, SigningKey: devicePub, NotBefore: now - 10, ExpiresAt: now + 7200}, root)
	if err != nil {
		t.Fatal(err)
	}
	s := identity.SessionCertificate{Binding: b, Version: 1, EncryptionKey: ek.PublicKey().Bytes(), NotBefore: now - 10, ExpiresAt: now + 3600}
	sc, err := identity.IssueSession(s, device)
	if err != nil {
		t.Fatal(err)
	}
	return party{identity.Chain{RootPublicKey: rootPub, Device: dc, Session: sc}, s, key, ek}
}

func setup(t testing.TB) (party, protocol.RoutingHeader, protocol.MessagePayload, envelope.OpenOptions) {
	return setupIDs(t, "sender", "recipient", "grant_one")
}

func setupIDs(t testing.TB, senderID, recipientID, grantID string) (party, protocol.RoutingHeader, protocol.MessagePayload, envelope.OpenOptions) {
	t.Helper()
	s, r := newParty(t, senderID), newParty(t, recipientID)
	g, err := grant.Issue(grant.Grant{Version: 1, GrantID: grantID, Issuer: r.cert.Binding, Subject: s.cert.Binding, Scope: grant.Coauthor, GrantEpoch: 1, NotBefore: now - 5, ExpiresAt: now + 600, Limits: grant.DefaultLimits(), ReplyRoute: &s.cert.Binding}, r.signing)
	if err != nil {
		t.Fatal(err)
	}
	h := protocol.RoutingHeader{WireVersion: 1, Suite: protocol.Suite, DstMailbox: "mailbox_b", RoutePermitID: "permit_a", MessageID: strings.Repeat("a", 32), RecipientKeyEpoch: 1, ExpiresAt: now + 300}
	m := protocol.MessagePayload{MessageID: h.MessageID, FromSession: senderID, FromSessionEpoch: 1, ToSession: recipientID, ToSessionEpoch: 1, SenderSeq: 1, GrantID: grantID, GrantEpoch: 1, GrantHash: grant.Hash(g), Kind: protocol.Coauthor, BodyUTF8: "ctrl:despawn @path is data", CreatedAt: now, ExpiresAt: h.ExpiresAt, ConversationID: strings.Repeat("b", 32), BudgetID: "budget_one"}
	o := envelope.OpenOptions{RecipientKey: r.encryption, RecipientChain: r.chain, RecipientPin: r.cert.IdentityID, SenderChain: s.chain, SenderPin: s.cert.IdentityID, SignedGrant: g, CurrentGrantEpoch: 1, Now: now, ExpectedMailbox: h.DstMailbox, ExpectedPermit: h.RoutePermitID, Replay: envelope.ReplaySnapshot{NextSeq: 1}}
	return s, h, m, o
}

func TestEnvelopeAdmissionAndDuplicate(t *testing.T) {
	s, h, m, o := setup(t)
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	a, err := envelope.Open(raw, o)
	if err != nil {
		t.Fatal(err)
	}
	if a.Duplicate || a.Message.BodyUTF8 != m.BodyUTF8 {
		t.Fatal("wrong initial admission")
	}
	o.Replay = envelope.ReplaySnapshot{NextSeq: 2, Records: []envelope.ReplayRecord{a.Record}}
	a, err = envelope.Open(raw, o)
	if err != nil || !a.Duplicate {
		t.Fatalf("retry: %v", err)
	}
	o.CurrentGrantEpoch = 2
	if _, err := envelope.Open(raw, o); !fault.IsCode(err, fault.Revoked) {
		t.Fatalf("duplicate bypassed revoke: %v", err)
	}
}

func TestEnvelopeAADTamperEveryHeaderField(t *testing.T) {
	s, h, m, o := setup(t)
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*protocol.RoutingHeader){
		"wire_version": func(h *protocol.RoutingHeader) { h.WireVersion++ }, "suite": func(h *protocol.RoutingHeader) { h.Suite = "unknown" },
		"feature_bits": func(h *protocol.RoutingHeader) { h.FeatureBits = 1 }, "dst_mailbox": func(h *protocol.RoutingHeader) { h.DstMailbox = "other" },
		"permit": func(h *protocol.RoutingHeader) { h.RoutePermitID = "other" }, "id": func(h *protocol.RoutingHeader) { h.MessageID = strings.Repeat("c", 32) },
		"epoch": func(h *protocol.RoutingHeader) { h.RecipientKeyEpoch++ }, "expiry": func(h *protocol.RoutingHeader) { h.ExpiresAt++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			bad := p
			mutate(&bad.Header)
			// Exercise HPKE even for unsupported version/suite/feature values that
			// the public decoder correctly rejects before doing cryptography.
			aad, canonicalErr := codec.Canonical(bad.Header)
			if canonicalErr != nil {
				t.Fatal(canonicalErr)
			}
			if _, openErr := peercrypto.Open(o.RecipientKey, bad.Enc, aad, bad.Ciphertext); !fault.IsCode(openErr, fault.DecryptionFailed) {
				t.Fatalf("field is not AAD-bound: %v", openErr)
			}
			raw, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			expected := o
			expected.ExpectedMailbox, expected.ExpectedPermit = bad.Header.DstMailbox, bad.Header.RoutePermitID
			code := fault.DecryptionFailed
			if name == "wire_version" || name == "suite" || name == "feature_bits" {
				code = fault.UnsupportedVersion
			}
			if _, err := envelope.Open(raw, expected); !fault.IsCode(err, code) {
				t.Fatalf("tampered header: %v, want %s", err, code)
			}
		})
	}
	for _, part := range []string{"enc", "ciphertext"} {
		t.Run(part, func(t *testing.T) {
			bad := p
			bad.Enc = append([]byte(nil), p.Enc...)
			bad.Ciphertext = append([]byte(nil), p.Ciphertext...)
			if part == "enc" {
				bad.Enc[0] ^= 1
			} else {
				bad.Ciphertext[0] ^= 1
			}
			raw, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := envelope.Open(raw, o); !fault.IsCode(err, fault.DecryptionFailed) {
				t.Fatalf("tamper: %v", err)
			}
		})
	}
}

func TestEnvelopeRejectsAudienceGrantAndControls(t *testing.T) {
	s, h, m, o := setup(t)
	for _, tc := range []struct {
		change func(*protocol.MessagePayload)
		code   fault.Code
	}{
		{func(m *protocol.MessagePayload) { m.ToSession = "other" }, fault.AudienceMismatch},
		{func(m *protocol.MessagePayload) { m.ToSessionEpoch++ }, fault.EpochMismatch},
		{func(m *protocol.MessagePayload) { m.FromSession = "other" }, fault.AudienceMismatch},
		{func(m *protocol.MessagePayload) { m.GrantHash = strings.Repeat("f", 64) }, fault.InvalidGrant},
		{func(m *protocol.MessagePayload) { m.GrantEpoch++ }, fault.EpochMismatch},
		{func(m *protocol.MessagePayload) { m.Kind = protocol.GrantUpdate }, fault.ScopeDenied},
		{func(m *protocol.MessagePayload) { m.Kind = protocol.KeyUpdate }, fault.ScopeDenied},
		{func(m *protocol.MessagePayload) { m.Kind = protocol.Revoke }, fault.ScopeDenied},
		{func(m *protocol.MessagePayload) {
			m.Kind = protocol.Reply
			m.ParentID = strings.Repeat("c", 32)
			m.ReplyTo = m.ParentID
		}, fault.ScopeDenied},
	} {
		bad := m
		tc.change(&bad)
		p, err := envelope.Seal(h, bad, s.signing, o.RecipientKey.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := envelope.Open(raw, o); !fault.IsCode(err, tc.code) {
			t.Fatalf("unbound authority: %v, want %s", err, tc.code)
		}
	}
}

func TestSignatureVerifiedBeforeMessageJSON(t *testing.T) {
	s, h, _, o := setup(t)
	compact, err := peercrypto.Sign([]byte(`{"role":"owner","role":"owner"}`), identity.RootFingerprint(s.cert.SigningKey), peercrypto.MessageType, s.signing)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(compact, ".")
	parts[2] = strings.Repeat("A", 86)
	aad, err := h.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	enc, ct, err := peercrypto.Seal(o.RecipientKey.PublicKey(), aad, []byte(strings.Join(parts, ".")))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(protocol.EncryptedPacket{Header: h, Enc: enc, Ciphertext: ct})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := envelope.Open(raw, o); !fault.IsCode(err, fault.InvalidSignature) {
		t.Fatalf("JSON parsed before verification: %v", err)
	}
}
