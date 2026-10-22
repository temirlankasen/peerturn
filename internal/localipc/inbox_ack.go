package localipc

import (
	"encoding/json"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// InformDeliveryIDs validates one bounded inbox page before a caller can confirm
// that its no-wake messages were written to the selected CLI or MCP output.
func InformDeliveryIDs(raw json.RawMessage) ([]string, error) {
	var page struct {
		Messages []struct {
			MessageID string `json:"message_id"`
			Peer      string `json:"peer"`
			BodyUTF8  string `json:"body_utf8"`
			Routing   string `json:"routing"`
			State     string `json:"state"`
			Stage     string `json:"stage"`
		} `json:"messages"`
		NextCursor string `json:"next_cursor,omitempty"`
	}
	if codec.Decode(raw, MaxFrame, &page) != nil || page.Messages == nil || len(page.Messages) > 24 || (page.NextCursor != "" && !protocol.ValidID(page.NextCursor)) {
		return nil, NewError("invalid_response", "pending")
	}
	ids := make([]string, 0, len(page.Messages))
	seen := make(map[string]bool, len(page.Messages))
	for _, message := range page.Messages {
		if !protocol.ValidID(message.MessageID) || message.Routing == "" || len(message.Routing) > 64 || seen[message.MessageID] {
			return nil, NewError("invalid_response", "pending")
		}
		seen[message.MessageID] = true
		// This is the durable inbox no-wake lane, including stored replies. The
		// daemon rechecks the route and current authority before changing state.
		if message.Routing == "inbox_no_wake" {
			ids = append(ids, message.MessageID)
		}
	}
	return ids, nil
}
