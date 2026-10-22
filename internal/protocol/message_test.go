package protocol_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func validMessage() protocol.MessagePayload {
	return protocol.MessagePayload{
		MessageID: "00112233445566778899aabbccddeeff", FromSession: "sender", FromSessionEpoch: 1,
		ToSession: "recipient", ToSessionEpoch: 2, SenderSeq: 1, GrantID: "grant_one", GrantEpoch: 1,
		GrantHash: strings.Repeat("a", 64), Kind: protocol.Coauthor, BodyUTF8: "ctrl:despawn @file https://example.test",
		CreatedAt: 1800000000, ExpiresAt: 1800000300, ConversationID: strings.Repeat("b", 32), BudgetID: "budget_one",
	}
}

func TestMessagePolicyAndBodyData(t *testing.T) {
	m := validMessage()
	for _, kind := range []protocol.Kind{protocol.Inform, protocol.Ask} {
		m.Kind = kind
		if err := m.Validate(); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	m.Kind = protocol.Coauthor
	m.SenderSeq = math.MaxUint64
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := protocol.DecodeMessage(b)
	if err != nil || got.BodyUTF8 != m.BodyUTF8 || got.SenderSeq != math.MaxUint64 {
		t.Fatalf("body or counter changed: %v", err)
	}
}

func TestPacketSizes(t *testing.T) {
	p := protocol.EncryptedPacket{Header: protocol.RoutingHeader{WireVersion: 1, Suite: protocol.Suite, DstMailbox: "m", RoutePermitID: "p", MessageID: strings.Repeat("a", 32), RecipientKeyEpoch: 1, ExpiresAt: 1800000300}, Enc: make([]byte, 32), Ciphertext: make([]byte, protocol.MaxCiphertext)}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Ciphertext = append(p.Ciphertext, 0)
	if err := p.Validate(); !fault.IsCode(err, fault.TooLarge) {
		t.Fatalf("got %v", err)
	}
	if _, err := protocol.DecodePacket(make([]byte, protocol.MaxPacket+1)); !fault.IsCode(err, fault.TooLarge) {
		t.Fatalf("got %v", err)
	}
}

func TestReceiptStagesAreDistinct(t *testing.T) {
	r := protocol.ReceiptPayload{MessageID: strings.Repeat("a", 32), AttemptID: strings.Repeat("b", 32), Stage: "adapter_accepted", EvidenceKind: "native_queue", At: 1800000000}
	for _, stage := range []string{"sender_stored", "relay_stored", "receiver_stored", "offered", "delivered", "adapter_accepted", "model_input", "turn_completed", "reply", "in_doubt", "rejected", "expired", "revoked", "cancelled"} {
		r.Stage = stage
		switch stage {
		case "offered":
			r.EvidenceKind = "hook_notice"
		case "delivered":
			r.EvidenceKind = "inbox_read"
		default:
			r.EvidenceKind = "native_queue"
		}
		if err := r.Validate(); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
	}
	r.Stage = "delivered_exactly_once"
	if err := r.Validate(); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("got %v", err)
	}
}

func FuzzDecodeMessage(f *testing.F) {
	b, err := json.Marshal(validMessage())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	f.Fuzz(func(t *testing.T, data []byte) {
		if m, err := protocol.DecodeMessage(data); err == nil {
			if err := m.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func FuzzDecodePacket(f *testing.F) {
	f.Add([]byte(`{"header":{},"enc":"","ciphertext":""}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if p, err := protocol.DecodePacket(data); err == nil {
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func FuzzDecodeReceipt(f *testing.F) {
	f.Add([]byte(`{"message_id":"00112233445566778899aabbccddeeff","attempt_id":"ffeeddccbbaa99887766554433221100","stage":"in_doubt","evidence_kind":"ambiguous_submit","at":1800000000}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if r, err := protocol.DecodeReceipt(data); err == nil {
			if err := r.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
