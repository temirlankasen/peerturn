package envelope_test

import (
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
)

func TestPureReplayDecision(t *testing.T) {
	r := envelope.ReplayRecord{MessageID: strings.Repeat("a", 32), Digest: strings.Repeat("b", 64), SenderSeq: 1}
	s := envelope.ReplaySnapshot{NextSeq: 1}
	duplicate, err := envelope.CheckReplay(s, r)
	if err != nil || duplicate {
		t.Fatalf("first: %t %v", duplicate, err)
	}
	s.NextSeq = 2
	s.Records = []envelope.ReplayRecord{r}
	duplicate, err = envelope.CheckReplay(s, r)
	if err != nil || !duplicate {
		t.Fatalf("retry: %t %v", duplicate, err)
	}
	r.Digest = strings.Repeat("c", 64)
	if _, err := envelope.CheckReplay(s, r); !fault.IsCode(err, fault.Conflict) {
		t.Fatalf("conflict: %v", err)
	}
	r.MessageID = strings.Repeat("d", 32)
	if _, err := envelope.CheckReplay(s, r); !fault.IsCode(err, fault.Replay) {
		t.Fatalf("replay: %v", err)
	}
	r.SenderSeq = 3
	if _, err := envelope.CheckReplay(s, r); !fault.IsCode(err, fault.Code("sequence_gap")) {
		t.Fatalf("gap: %v", err)
	}
}

func TestReplayRecordRejectsNonDigest(t *testing.T) {
	r := envelope.ReplayRecord{MessageID: strings.Repeat("a", 32), Digest: strings.Repeat("z", 64), SenderSeq: 1}
	if _, err := envelope.CheckReplay(envelope.ReplaySnapshot{NextSeq: 1}, r); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("invalid digest accepted: %v", err)
	}
}
