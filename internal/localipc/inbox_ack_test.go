package localipc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInformDeliveryIDs(t *testing.T) {
	id := strings.Repeat("a", 32)
	live := strings.Repeat("b", 32)
	message := `{"message_id":"` + id + `","routing":"inbox_no_wake"}`
	page := `{"messages":[` + message + `,{"message_id":"` + live + `","routing":"live"}]}`
	ids, err := InformDeliveryIDs(json.RawMessage(page))
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("ids=%v error=%v", ids, err)
	}
	for _, raw := range []string{
		`{}`, `{"messages":null}`, `{"messages":"invalid"}`,
		`{"messages":[{"message_id":"invalid","routing":"inbox_no_wake"}]}`,
		`{"messages":[{"message_id":"` + strings.ToUpper(id) + `","routing":"inbox_no_wake"}]}`,
		`{"messages":[` + message + `,` + message + `]}`,
		`{"messages":[` + message + `],"messages":[]}`,
		`{"messages":[` + message + `],"next_cursor":"invalid"}`,
		`{"messages":[` + strings.Repeat(message+",", 24) + message + `]}`,
		`{"messages":[],"unknown":"` + strings.Repeat("x", MaxFrame) + `"}`,
	} {
		if got, parseErr := InformDeliveryIDs(json.RawMessage(raw)); parseErr == nil || len(got) != 0 {
			t.Fatal("malformed inbox could be acknowledged")
		}
	}
	if ids, err = InformDeliveryIDs(json.RawMessage(`{"messages":[]}`)); err != nil || len(ids) != 0 {
		t.Fatal("empty inbox rejected")
	}
}
