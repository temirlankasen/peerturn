package envelope_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func sealed(t testing.TB, s party, h protocol.RoutingHeader, m protocol.MessagePayload, o envelope.OpenOptions) []byte {
	t.Helper()
	p, err := envelope.Seal(h, m, s.signing, o.RecipientKey.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestVerifiedGapCanBeParkedThenFilled(t *testing.T) {
	s, h, m, o := setup(t)
	m.SenderSeq = 2
	data := sealed(t, s, h, m, o)
	a, err := envelope.Open(data, o)
	if !fault.IsCode(err, fault.SequenceGap) || a.Gap == nil {
		t.Fatalf("gap lacks verified parking evidence: %+v %v", a, err)
	}
	if a.Message.MessageID != "" || a.WantsModel || a.Duplicate || a.Gap.Record.SenderSeq != 2 ||
		a.Gap.Record.MessageID != m.MessageID || a.Gap.ExpiresAt != m.ExpiresAt ||
		a.Gap.Sender.SessionID != m.FromSession || a.Gap.Recipient.SessionID != m.ToSession {
		t.Fatal("gap exposed admission or lost verified metadata")
	}
	o.Replay.Pending = []envelope.ReplayRecord{a.Gap.Record}
	if retry, retryErr := envelope.Open(data, o); !fault.IsCode(retryErr, fault.SequenceGap) || retry.Gap == nil {
		t.Fatalf("parked retry: %v", retryErr)
	}
	first := m
	first.SenderSeq = 1
	first.MessageID = strings.Repeat("d", 32)
	first.ConversationID = strings.Repeat("e", 32)
	firstHeader := h
	firstHeader.MessageID = first.MessageID
	accepted, err := envelope.Open(sealed(t, s, firstHeader, first, o), o)
	if err != nil {
		t.Fatal(err)
	}
	o.Replay.NextSeq = 2
	o.Replay.Records = []envelope.ReplayRecord{accepted.Record}
	if a, err = envelope.Open(data, o); err != nil || a.Duplicate || !a.WantsModel || a.Gap != nil {
		t.Fatalf("filled gap not released: %+v %v", a, err)
	}
	o.Replay.NextSeq = 3
	o.Replay.Pending = nil
	if a, err = envelope.Open(data, o); !fault.IsCode(err, fault.Replay) || a.Gap != nil {
		t.Fatalf("stale traffic parkable: %v", err)
	}
}

func TestGapWindowAndPendingConflictsAreBounded(t *testing.T) {
	s, h, m, o := setup(t)
	m.SenderSeq = envelope.MaxPendingSequences + 2
	if a, err := envelope.Open(sealed(t, s, h, m, o), o); !fault.IsCode(err, fault.SequenceWindowExceeded) || a.Gap != nil {
		t.Fatalf("distant sequence was parkable: %v", err)
	}
	m.SenderSeq = 2
	data := sealed(t, s, h, m, o)
	a, err := envelope.Open(data, o)
	if !fault.IsCode(err, fault.SequenceGap) || a.Gap == nil {
		t.Fatal(err)
	}
	o.Replay.Pending = []envelope.ReplayRecord{a.Gap.Record}
	m.BodyUTF8 = "changed packet under parked ID"
	if a, err = envelope.Open(sealed(t, s, h, m, o), o); !fault.IsCode(err, fault.Conflict) || a.Gap != nil {
		t.Fatalf("pending conflict: %v", err)
	}
	m.MessageID = strings.Repeat("c", 32)
	h.MessageID = m.MessageID
	if a, err = envelope.Open(sealed(t, s, h, m, o), o); !fault.IsCode(err, fault.Replay) || a.Gap != nil {
		t.Fatalf("pending sequence reused: %v", err)
	}
	o.Replay.Pending = make([]envelope.ReplayRecord, envelope.MaxPendingSequences)
	if a, err = envelope.Open(sealed(t, s, h, m, o), o); !fault.IsCode(err, fault.SequenceWindowExceeded) || a.Gap != nil {
		t.Fatalf("pending quota ignored: %v", err)
	}
}

func TestGapPreservesContinuationBeforeItsParentArrives(t *testing.T) {
	s, h, m, o := setup(t)
	m.SenderSeq = 2
	m.ParentID = strings.Repeat("c", 32)
	m.Hop = 1
	if a, err := envelope.Open(sealed(t, s, h, m, o), o); !fault.IsCode(err, fault.SequenceGap) || a.Gap == nil {
		t.Fatalf("future parent must be checked after gap fill: %v", err)
	}
	o.Replay.NextSeq = 2
	if a, err := envelope.Open(sealed(t, s, h, m, o), o); !fault.IsCode(err, fault.ScopeDenied) || a.Gap != nil {
		t.Fatalf("gap fill bypassed conversation verification: %v", err)
	}
}
