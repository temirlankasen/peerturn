package protocol

import "testing"

func TestAskStagesRemainDistinctTypedReceipts(t *testing.T) {
	for _, stage := range []string{"responder_started", "model_input", "turn_completed", "reply_redacted", "reply", "responder_failed"} {
		r := ReceiptPayload{MessageID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AttemptID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Stage: stage, EvidenceKind: "fork_event", At: 1}
		if err := r.Validate(); err != nil {
			t.Errorf("typed ask evidence %s rejected: %v", stage, err)
		}
	}
}
