package envelope_test

import (
	"encoding/json"
	"testing"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestExpiredCustodyNeverReturnsAuthorityOrPlaintext(t *testing.T) {
	s, h, m, o := setup(t)
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	o.Now = h.ExpiresAt + 1
	a, err := envelope.OpenForCustody(raw, o)
	if !fault.IsCode(err, fault.Expired) || a.Expired == nil || a.Expired.Record.MessageID != m.MessageID || a.Message.BodyUTF8 != "" || a.Routing != "" || a.WantsModel {
		t.Fatalf("expired authority leak: %+v %v", a, err)
	}
	if _, err = envelope.Open(raw, o); !fault.IsCode(err, fault.Expired) {
		t.Fatalf("live expired: %v", err)
	}
	p.Ciphertext[0] ^= 1
	raw, err = json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	a, err = envelope.OpenForCustody(raw, o)
	if err == nil || a.Expired != nil || a.Gap != nil {
		t.Fatalf("unauthenticated tombstone: %+v %v", a, err)
	}
	o.CurrentGrantEpoch = 2
	a, err = envelope.OpenForCustody(raw, o)
	if err == nil || a.Expired != nil {
		t.Fatalf("revoked tombstone authority: %+v %v", a, err)
	}
}

func TestGapPreservesAuthenticatedControlKind(t *testing.T) {
	s, h, m, o := setup(t)
	m.Kind = protocol.Receipt
	m.SenderSeq = 2
	r := protocol.ReceiptPayload{MessageID: m.MessageID, AttemptID: m.MessageID, Stage: "receiver_stored", EvidenceKind: "peer_commit", At: now}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	m.BodyUTF8 = string(body)
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	a, err := envelope.OpenForCustody(raw, o)
	if !fault.IsCode(err, fault.SequenceGap) || a.Gap == nil || a.Gap.Kind != protocol.Receipt || a.Routing != "" || a.Message.BodyUTF8 != "" {
		t.Fatalf("gap control provenance: %+v %v", a, err)
	}
}
