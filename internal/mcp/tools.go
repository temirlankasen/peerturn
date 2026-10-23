// Package mcp exposes a bounded, session-scoped stdio proxy.
package mcp

import (
	"encoding/json"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func tools() []tool {
	return []tool{
		{"peer_list", "List peers of this bound session.", schema(nil)},
		{"peer_join", "Join the single pending invitation carried by the owner-prompt hook in this exact native session within ten minutes. Call with {} without copying any link or secret. If ambiguous, select a non-secret handle from peer_status or the error. Never use peer/inbox/reply links. coauthor is strong trust.", joinSchema()},
		{"peer_send", "Store explicit text for a peer. coauthor is strong trust; ask requires a qualified Claude receiver; storage is distinct from model input.", schema([]string{"peer", "kind", "body"})},
		{"peer_reply", "Reply with explicit text to a verified message.", schema([]string{"message_id", "body"})},
		{"peer_inbox", "Read a bounded page of stored messages; pass next_cursor as after. This does not deliver them to a model.", inboxSchema()},
		{"peer_receipt", "Read evidence for one message; storage is distinct from model input.", schema([]string{"message_id"})},
		{"peer_status", "Read binding, capabilities and non-secret pending owner invitation handles for this session.", schema(nil)},
	}
}

func schema(required []string) map[string]any {
	properties := make(map[string]any, len(required))
	for _, name := range required {
		limit := 512
		if name == "body" {
			limit = 16 * 1024
		}
		if name == "text" {
			limit = localipc.MaxInvitationTextBytes
		}
		property := map[string]any{"type": "string", "minLength": 1, "maxLength": limit}
		if name == "kind" {
			property["enum"] = []string{"coauthor", "ask"}
		}
		properties[name] = property
	}
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func toolRequest(name string, args json.RawMessage) (localipc.Request, error) {
	request := localipc.Request{}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var err error
	switch name {
	case "peer_join":
		var fields struct {
			Handle json.RawMessage `json:"handle,omitempty"`
		}
		err = codec.Decode(args, maxLine, &fields)
		request.Method, request.Scope = "join.owner", "coauthor"
		if len(fields.Handle) > 0 {
			if codec.Decode(fields.Handle, 64, &request.InvitationHandle) != nil || request.InvitationHandle == "" {
				return request, localipc.NewError("invalid_arguments", "rejected")
			}
		}
	case "peer_list", "peer_status":
		err = codec.Decode(args, maxLine, &struct{}{})
		request.Method = map[string]string{"peer_list": "peers", "peer_inbox": "inbox", "peer_status": "status"}[name]
	case "peer_inbox":
		var fields struct {
			After string `json:"after,omitempty"`
		}
		err = codec.Decode(args, maxLine, &fields)
		request.Method = "inbox"
		request.Cursor = fields.After
	case "peer_send":
		var fields struct {
			Peer string `json:"peer"`
			Kind string `json:"kind"`
			Body string `json:"body"`
		}
		err = codec.Decode(args, maxLine, &fields)
		request.Method, request.Peer, request.Kind, request.Body = "send", fields.Peer, fields.Kind, fields.Body
		if fields.Peer == "" || (fields.Kind != "coauthor" && fields.Kind != "ask") || fields.Body == "" {
			return request, localipc.NewError("invalid_arguments", "rejected")
		}
	case "peer_reply":
		var fields struct {
			MessageID string `json:"message_id"`
			Body      string `json:"body"`
		}
		err = codec.Decode(args, maxLine, &fields)
		request.Method, request.MessageID, request.Body = "reply", fields.MessageID, fields.Body
		if fields.MessageID == "" || fields.Body == "" {
			return request, localipc.NewError("invalid_arguments", "rejected")
		}
	case "peer_receipt":
		var fields struct {
			MessageID string `json:"message_id"`
		}
		err = codec.Decode(args, maxLine, &fields)
		request.Method, request.MessageID = "receipt", fields.MessageID
		if fields.MessageID == "" {
			return request, localipc.NewError("invalid_arguments", "rejected")
		}
	default:
		return request, localipc.NewError("unknown_tool", "rejected")
	}
	if err != nil {
		return request, err
	}
	return request, request.Validate()
}

func inboxSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"after": map[string]any{"type": "string", "minLength": 32, "maxLength": 32}}, "additionalProperties": false}
}

func joinSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"handle": map[string]any{"type": "string", "minLength": 12, "maxLength": 12, "pattern": "^[0-9a-f]{12}$"}}, "additionalProperties": false}
}
