package envelope_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func TestSignedPayloadRejectsSingleByteTampering(t *testing.T) {
	s, h, m, o := setup(t)
	m.ParentID = strings.Repeat("c", 32)
	m.ReplyTo = m.ParentID
	m.Hop = 1
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	aad, err := h.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	compact, err := peercrypto.Open(o.RecipientKey, p.Enc, aad, p.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(compact), ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peercrypto.Verify(string(compact), identity.RootFingerprint(s.cert.SigningKey), peercrypto.MessageType, s.cert.SigningKey); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"message_id", "from_session", "from_session_epoch", "to_session", "to_session_epoch",
		"sender_seq", "grant_id", "grant_epoch", "grant_hash", "kind", "body_utf8", "created_at", "expires_at",
		"conversation_id", "parent_id", "reply_to", "hop", "budget_id"} {
		t.Run(field, func(t *testing.T) {
			marker := []byte(`"` + field + `":`)
			messageStart := bytes.Index(raw, []byte(`"message":`))
			at := bytes.Index(raw[messageStart:], marker)
			if at < 0 {
				t.Fatalf("missing signed field %s", field)
			}
			at += messageStart + len(marker)
			if raw[at] == '"' {
				at++
			}
			changedPayload := bytes.Clone(raw)
			changedPayload[at] ^= 1
			if bytes.Equal(raw, changedPayload) || !bytes.Equal(raw[:at], changedPayload[:at]) || !bytes.Equal(raw[at+1:], changedPayload[at+1:]) {
				t.Fatal("fixture must change exactly one signed byte")
			}
			forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString(changedPayload) + "." + parts[2]
			enc, ct, err := peercrypto.Seal(o.RecipientKey.PublicKey(), aad, []byte(forged))
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(protocol.EncryptedPacket{Header: h, Enc: enc, Ciphertext: ct})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := envelope.Open(data, o); !fault.IsCode(err, fault.InvalidSignature) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestValidSignatureDoesNotPermitAuthorityExtensions(t *testing.T) {
	s, h, m, o := setup(t)
	for _, field := range []string{"role", "argv", "settings", "approval", "toolCall", "socket_path"} {
		t.Run(field, func(t *testing.T) {
			raw, err := json.Marshal(envelope.SignedMessage{Header: h, Message: m})
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw[:len(raw)-1], []byte(`,"`+field+`":"owner"}`)...)
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
			if _, err := envelope.Open(data, o); !fault.IsCode(err, fault.InvalidEncoding) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestReplyBudgetAndReceiptAdmission(t *testing.T) {
	s, h, m, o := setup(t)
	m.Kind = protocol.Reply
	m.ParentID = strings.Repeat("c", 32)
	m.ReplyTo = m.ParentID
	m.Hop = 1
	o.Conversation = &envelope.Conversation{ID: m.ConversationID, ParentID: m.ParentID, BudgetID: m.BudgetID, Peer: s.cert.Binding}
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if a, checkErr := envelope.Open(raw, o); checkErr != nil || a.WantsModel || a.Routing != envelope.InboxNoWake {
		t.Fatalf("reply: %v", checkErr)
	}
	first, err := envelope.Open(raw, o)
	if err != nil {
		t.Fatal(err)
	}
	retryOptions := o
	retryOptions.Conversation = nil
	retryOptions.Replay = envelope.ReplaySnapshot{NextSeq: 2, Records: []envelope.ReplayRecord{first.Record}}
	if a, openErr := envelope.Open(raw, retryOptions); openErr != nil || !a.Duplicate || a.WantsModel {
		t.Fatalf("duplicate required a live conversation: %v", openErr)
	}
	m.Kind = protocol.Coauthor
	raw = sealed(t, s, h, m, o)
	o.Budget = envelope.BudgetSnapshot{MinuteStart: now, MessagesInMinute: 10}
	if _, checkErr := envelope.Open(raw, o); !fault.IsCode(checkErr, fault.BudgetExceeded) {
		t.Fatalf("rate: %v", checkErr)
	}
	o.Budget = envelope.BudgetSnapshot{TurnWindowStart: now, TurnsInTenMinutes: 20}
	if _, checkErr := envelope.Open(raw, o); !fault.IsCode(checkErr, fault.BudgetExceeded) {
		t.Fatalf("turns: %v", checkErr)
	}
	o.Budget = envelope.BudgetSnapshot{BurstUsed: 5}
	if _, checkErr := envelope.Open(raw, o); !fault.IsCode(checkErr, fault.BudgetExceeded) {
		t.Fatalf("burst: %v", checkErr)
	}
	o.Budget = envelope.BudgetSnapshot{}
	m.Kind = protocol.Receipt
	m.BodyUTF8 = `{"message_id":"dddddddddddddddddddddddddddddddd","attempt_id":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","stage":"in_doubt","evidence_kind":"unknown_submit","at":1800000000}`
	p, err = envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if a, checkErr := envelope.Open(raw, o); checkErr != nil || a.WantsModel {
		t.Fatalf("receipt woke model: %v", checkErr)
	}
}

func TestEnvelopeBoundsAndExpiry(t *testing.T) {
	s, h, m, o := setup(t)
	m.BodyUTF8 = strings.Repeat("a", protocol.MaxPlaintext)
	if _, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey()); !fault.IsCode(err, fault.TooLarge) {
		t.Fatalf("signed plaintext bound: %v", err)
	}
	m.BodyUTF8 = "hello"
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	o.Now = h.ExpiresAt
	if _, openErr := envelope.Open(raw, o); !fault.IsCode(openErr, fault.Expired) {
		t.Fatalf("expiry boundary: %v", openErr)
	}
}

func FuzzOpen(f *testing.F) {
	s, h, m, o := setup(f)
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		f.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	m.SenderSeq = 2
	f.Add(sealed(f, s, h, m, o))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"header":{"role":"owner"}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		a, err := envelope.Open(data, o)
		if err != nil {
			if a.Message.MessageID != "" {
				t.Fatal("plaintext returned on failure")
			}
			if a.Gap != nil && (!fault.IsCode(err, fault.SequenceGap) || a.Routing != "" || a.WantsModel || a.Gap.Record.SenderSeq <= o.Replay.NextSeq) {
				t.Fatal("invalid gap evidence or dispatch on failure")
			}
			return
		}
		if a.Message.ToSession != "recipient" || a.Message.Kind != protocol.Coauthor {
			t.Fatal("wrong audience or scope")
		}
	})
}
