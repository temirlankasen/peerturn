package envelope_test

import (
	"encoding/json"
	"strings"
	"testing"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestIssuedDottedSessionAndGrantIDsCompose(t *testing.T) {
	s, h, m, o := setupIDs(t, "sender.v1", "recipient.v1", "grant.v1")
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	a, err := envelope.Open(raw, o)
	if err != nil || !a.WantsModel || a.Message.GrantID != "grant.v1" {
		t.Fatalf("issued identifiers failed composition: %v", err)
	}
}

func TestReencryptedSignedPayloadCannotReadmit(t *testing.T) {
	s, h, m, o := setup(t)
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := envelope.Open(raw, o)
	if err != nil {
		t.Fatal(err)
	}
	o.Replay = envelope.ReplaySnapshot{NextSeq: 2, Records: []envelope.ReplayRecord{accepted.Record}}
	aad, err := h.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	compact, err := peercrypto.Open(o.RecipientKey, p.Enc, aad, p.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	enc, ct, err := peercrypto.Seal(o.RecipientKey.PublicKey(), aad, compact)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(protocol.EncryptedPacket{Header: h, Enc: enc, Ciphertext: ct})
	if err != nil {
		t.Fatal(err)
	}
	if _, openErr := envelope.Open(raw, o); !fault.IsCode(openErr, fault.Conflict) {
		t.Fatalf("reencryption reused admitted ID: %v", openErr)
	}
	h.MessageID = strings.Repeat("c", 32)
	aad, err = h.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	enc, ct, err = peercrypto.Seal(o.RecipientKey.PublicKey(), aad, compact)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(protocol.EncryptedPacket{Header: h, Enc: enc, Ciphertext: ct})
	if err != nil {
		t.Fatal(err)
	}
	if _, openErr := envelope.Open(raw, o); !fault.IsCode(openErr, fault.AudienceMismatch) {
		t.Fatalf("fresh outer ID replayed old JWS: %v", openErr)
	}
}
