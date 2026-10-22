package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/localipc"
)

func TestMCPInboxAck(t *testing.T) {
	for _, mode := range []string{"ok", "failed", "short", "live", "malformed", "ack-failed"} {
		t.Run(mode, func(t *testing.T) {
			writer := &mcpInboxWriter{mode: mode}
			caller := &inboxAckCaller{mode: mode, writer: writer}
			input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"peer_inbox","arguments":{}}}` + "\n"
			err := Serve(t.Context(), strings.NewReader(input), writer, Options{Session: "chosen", Caller: caller})
			wantAck := mode == "ok" || mode == "ack-failed"
			if (caller.acks == 1) != wantAck || caller.early {
				t.Fatalf("acks=%d early=%v mode=%s", caller.acks, caller.early, mode)
			}
			wantError := mode == "failed" || mode == "short" || mode == "ack-failed"
			if (err != nil) != wantError {
				t.Fatalf("error=%v mode=%s", err, mode)
			}
			if mode == "malformed" && !strings.Contains(writer.String(), `"isError":true`) {
				t.Fatal("malformed inbox did not return a tool error")
			}
			if mode == "ack-failed" && strings.Count(writer.String(), "\n") != 2 {
				t.Fatal("ack failure emitted a duplicate tool result")
			}
		})
	}
}

type inboxAckCaller struct {
	mode   string
	writer *mcpInboxWriter
	acks   int
	early  bool
}

func (c *inboxAckCaller) Call(_ context.Context, r localipc.Request) (json.RawMessage, error) {
	switch r.Method {
	case "hello":
		return json.RawMessage(`{"capability":"bound-token"}`), nil
	case "inbox":
		if c.mode == "malformed" {
			return json.RawMessage(`{"messages":"invalid"}`), nil
		}
		route := "inbox_no_wake"
		if c.mode == "live" {
			route = "live"
		}
		return json.RawMessage(`{"messages":[{"message_id":"` + strings.Repeat("a", 32) + `","routing":"` + route + `"}]}`), nil
	case "inbox.delivered":
		c.acks++
		c.early = c.writer.writes < 2 || r.Session != "chosen" || r.Capability != "bound-token" || r.MessageID != strings.Repeat("a", 32)
		if c.mode == "ack-failed" {
			return nil, localipc.NewError("unavailable", "pending")
		}
	}
	return json.RawMessage(`{"delivered":true}`), nil
}

type mcpInboxWriter struct {
	bytes.Buffer
	mode   string
	writes int
}

func (w *mcpInboxWriter) Write(data []byte) (int, error) {
	if w.writes == 1 && w.mode == "failed" {
		return 0, io.ErrClosedPipe
	}
	if w.writes == 1 && w.mode == "short" {
		return len(data) - 1, nil
	}
	n, err := w.Buffer.Write(data)
	if err == nil && n == len(data) {
		w.writes++
	}
	return n, err
}
