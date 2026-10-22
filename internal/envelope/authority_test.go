package envelope_test

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// signedPacket bypasses sender validation to test the receiving trust boundary.
func signedPacket(t testing.TB, s party, h protocol.RoutingHeader, m protocol.MessagePayload, o envelope.OpenOptions) []byte {
	t.Helper()
	raw, err := json.Marshal(envelope.SignedMessage{Header: h, Message: m})
	if err != nil {
		t.Fatal(err)
	}
	compact, err := peercrypto.Sign(raw, identity.RootFingerprint(s.cert.SigningKey), peercrypto.MessageType, s.signing)
	if err != nil {
		t.Fatal(err)
	}
	aad, err := h.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	enc, ct, err := peercrypto.Seal(o.RecipientKey.PublicKey(), aad, []byte(compact))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(protocol.EncryptedPacket{Header: h, Enc: enc, Ciphertext: ct})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReceiverRejectsSignedAuthorityMismatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*protocol.RoutingHeader, *protocol.MessagePayload, *envelope.OpenOptions)
		code   fault.Code
	}{
		{"future_creation", func(_ *protocol.RoutingHeader, m *protocol.MessagePayload, _ *envelope.OpenOptions) { m.CreatedAt++ }, fault.NotYetValid},
		{"before_grant", func(_ *protocol.RoutingHeader, m *protocol.MessagePayload, _ *envelope.OpenOptions) {
			m.CreatedAt = now - 6
		}, fault.InvalidGrant},
		{"after_grant", func(h *protocol.RoutingHeader, m *protocol.MessagePayload, _ *envelope.OpenOptions) {
			m.ExpiresAt = now + 601
			h.ExpiresAt = m.ExpiresAt
		}, fault.InvalidGrant},
		{"key_epoch", func(h *protocol.RoutingHeader, _ *protocol.MessagePayload, _ *envelope.OpenOptions) {
			h.RecipientKeyEpoch++
		}, fault.EpochMismatch},
		{"sender_pin", func(_ *protocol.RoutingHeader, _ *protocol.MessagePayload, o *envelope.OpenOptions) {
			o.SenderPin = strings.Repeat("f", 64)
		}, fault.NeedsVerify},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h, m, o := setup(t)
			tc.change(&h, &m, &o)
			a, err := envelope.Open(signedPacket(t, s, h, m, o), o)
			if !fault.IsCode(err, tc.code) || a.Message.MessageID != "" || a.Gap != nil || a.Routing != "" {
				t.Fatalf("got %+v %v, want %s", a, err, tc.code)
			}
		})
	}
}

func TestRecipientKeyMismatchRequiresVerification(t *testing.T) {
	s, h, m, o := setup(t)
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	o.RecipientKey = key
	// Encrypt to the presented key so HPKE succeeds and certificate binding decides.
	if _, err := envelope.Open(signedPacket(t, s, h, m, o), o); !fault.IsCode(err, fault.NeedsVerify) {
		t.Fatalf("unbound recipient key: %v", err)
	}
}

func TestGapDoesNotExposeUnverifiedMetadata(t *testing.T) {
	s, h, m, o := setup(t)
	m.SenderSeq = 2
	m.GrantHash = strings.Repeat("f", 64)
	if a, err := envelope.Open(signedPacket(t, s, h, m, o), o); !fault.IsCode(err, fault.InvalidGrant) || a.Gap != nil {
		t.Fatalf("unauthorized future packet was parkable: %v", err)
	}
}
