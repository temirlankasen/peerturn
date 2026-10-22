package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

type responderEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Usage    *struct {
		InputTokens           *int64 `json:"input_tokens"`
		CachedInputTokens     *int64 `json:"cached_input_tokens"`
		OutputTokens          *int64 `json:"output_tokens"`
		CacheWriteInputTokens int64  `json:"cache_write_input_tokens"`
		ReasoningOutputTokens int64  `json:"reasoning_output_tokens"`
	} `json:"usage"`
	Item struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
}

func parseResponderOutput(raw []byte, parent string, limits ResponderLimits) (ResponderResult, error) {
	fail := func(reason string) (ResponderResult, error) {
		return ResponderResult{}, &ResponderError{Reason: reason}
	}
	if len(raw) == 0 || len(raw) > 256<<10 || !utf8.Valid(raw) || raw[len(raw)-1] != '\n' {
		return fail("responder_failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var result ResponderResult
	started, completed := false, false
	tools := make(map[string]bool)
	for {
		var event responderEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fail("responder_failed")
		}
		if completed {
			return fail("responder_failed")
		}
		switch event.Type {
		case "thread.started":
			if started || result.ChildThreadID != "" || !nativeThreadID(event.ThreadID) || event.ThreadID == parent {
				return fail("responder_failed")
			}
			result.ChildThreadID = event.ThreadID
		case "turn.started":
			if started || result.ChildThreadID == "" {
				return fail("responder_failed")
			}
			started = true
		case "turn.completed":
			if !started || event.Usage == nil || strings.TrimSpace(result.Answer) == "" {
				return fail("responder_failed")
			}
			if event.Usage.InputTokens == nil || event.Usage.CachedInputTokens == nil || event.Usage.OutputTokens == nil {
				return fail("budget_unavailable")
			}
			usage := ResponderUsage{InputTokens: *event.Usage.InputTokens, CachedInputTokens: *event.Usage.CachedInputTokens, OutputTokens: *event.Usage.OutputTokens,
				CacheWriteInputTokens: event.Usage.CacheWriteInputTokens, ReasoningOutputTokens: event.Usage.ReasoningOutputTokens}
			if usage.InputTokens < 0 || usage.CachedInputTokens < 0 || usage.OutputTokens < 0 || usage.CachedInputTokens > usage.InputTokens || usage.CacheWriteInputTokens < 0 || usage.ReasoningOutputTokens < 0 {
				return fail("budget_unavailable")
			}
			result.Usage = usage
			completed = true
		case "item.started", "item.updated", "item.completed":
			if !started || event.Item.ID == "" {
				return fail("responder_failed")
			}
			switch event.Item.Type {
			case "agent_message":
				if event.Type == "item.completed" {
					result.Answer = event.Item.Text
				}
			case "reasoning", "todo_list":
			case "command_execution":
				tools[event.Item.ID] = true
				if len(tools) > limits.ToolCalls {
					return fail("budget_exceeded")
				}
			case "mcp_tool_call", "collab_tool_call", "web_search", "file_change":
				return fail("responder_unavailable")
			default:
				return fail("responder_failed")
			}
		default:
			return fail("responder_failed")
		}
	}
	if !completed {
		return fail("responder_failed")
	}
	if len(result.Answer) > limits.OutputBytes {
		return fail("budget_exceeded")
	}
	return result, nil
}
