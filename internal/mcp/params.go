package mcp

import (
	"encoding/json"

	"github.com/temirlankasen/peerturn/internal/codec"
)

type requestParams struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// Metadata is extensible data, never a selector outside the existing Codex call
// binding. Apply the same size/type checks before any method can have effects.
func validRequestMeta(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var meta map[string]json.RawMessage
	if codec.Decode(raw, maxToolMeta, &meta) != nil || meta == nil {
		return false
	}
	if token, ok := meta["progressToken"]; ok {
		var value any
		if codec.Decode(token, maxToolMeta, &value) != nil {
			return false
		}
		switch value.(type) {
		case string, json.Number:
		default:
			return false
		}
	}
	return true
}

func basicParams(raw json.RawMessage) bool {
	var input requestParams
	return len(raw) == 0 || codec.Decode(raw, maxLine, &input) == nil && validRequestMeta(input.Meta)
}

func listParams(raw json.RawMessage) bool {
	var input struct {
		Meta   json.RawMessage `json:"_meta,omitempty"`
		Cursor string          `json:"cursor,omitempty"`
	}
	// All seven tools fit in one page, so no continuation cursor is ever issued.
	// Accept the initial empty cursor; reject unknown positions rather than replay.
	return len(raw) == 0 || codec.Decode(raw, maxLine, &input) == nil && validRequestMeta(input.Meta) && input.Cursor == ""
}

func validTask(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var input struct {
		TTL json.Number `json:"ttl,omitempty"`
	}
	// Without a tasks capability MCP requires ordinary synchronous execution.
	// Validate the optional task shape but never turn it into execution authority.
	return codec.Decode(raw, maxInitialize, &input) == nil
}
