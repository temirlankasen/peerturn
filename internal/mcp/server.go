package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
)

// JSON can encode one input byte as six bytes (for example, \u0001).
// Keep space for the RPC envelope in addition to the maximum invitation text.
const maxLine = 6*localipc.MaxInvitationTextBytes + 16*1024

// Caller is the private authenticated daemon transport.
type Caller interface {
	Call(context.Context, localipc.Request) (json.RawMessage, error)
}

// Options binds this stdio instance to one explicitly selected local session.
type Options struct {
	Session                string
	Caller                 Caller
	ExpectedBindingEpoch   string
	ExpectedRuntimeVersion string
	CodexThread            string
	// ResolveCodex selects a caller, never authority; hello verifies the native binding.
	ResolveCodex func(string) (Caller, error)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type rpcResponse struct {
	JSONRPC   string          `json:"jsonrpc"`
	ID        json.RawMessage `json:"id"`
	Result    any             `json:"result,omitempty"`
	Error     *rpcError       `json:"error,omitempty"`
	delivered []string
}

// Serve implements newline-delimited JSON-RPC with session-scoped tools.
func Serve(ctx context.Context, reader io.Reader, writer io.Writer, options Options) (result error) {
	binding, err := newSessionBinding(ctx, options)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, binding.Close()) }()
	stop := context.AfterFunc(ctx, func() {
		if closer, ok := reader.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				return
			}
		}
	})
	defer stop()
	buffered := bufio.NewReader(reader)
	encoder := json.NewEncoder(completeWriter{writer})
	initialized := false
	for {
		data, oversized, readErr := requestLine(buffered)
		if err := ctx.Err(); err != nil {
			return err
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return localipc.NewError("unavailable", "unavailable")
		}
		var response *rpcResponse
		if oversized {
			response = failure(nil, -32602, "Request too large")
		} else {
			response = dispatch(ctx, data, binding, &initialized)
		}
		if response == nil {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return localipc.NewError("unavailable", "unavailable")
		}
		for _, id := range response.delivered {
			if _, err := binding.bound.Call(ctx, localipc.Request{Method: "inbox.delivered", MessageID: id}); err != nil {
				return localipc.NewError("unavailable", "pending")
			}
		}
	}
}

// Drain an oversized frame without retaining it so the next RPC stays usable.
func requestLine(reader *bufio.Reader) ([]byte, bool, error) {
	var data []byte
	oversized := false
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > 0 && part[len(part)-1] == '\n' {
			part = part[:len(part)-1]
		}
		if !oversized {
			if len(part) > maxLine-len(data) {
				oversized, data = true, nil
			} else {
				data = append(data, part...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && (len(data) > 0 || oversized) {
			err = nil
		}
		return data, oversized, err
	}
}

func dispatch(ctx context.Context, data []byte, binding *sessionBinding, initialized *bool) *rpcResponse {
	var request rpcRequest
	if err := codec.Decode(data, maxLine, &request); err != nil {
		return failure(nil, -32700, "Invalid JSON-RPC request")
	}
	if request.JSONRPC != "2.0" || request.Method == "" || !validID(request.ID) {
		return failure(nil, -32600, "Invalid JSON-RPC request")
	}
	if len(request.ID) == 0 {
		// Notifications cannot execute commands, including tools/call.
		return nil
	}
	if request.Method == "initialize" {
		if *initialized {
			return failure(request.ID, -32600, "Already initialized")
		}
		version, err := initialize(request.Params)
		if err != nil {
			return failure(request.ID, -32602, "Invalid initialize parameters")
		}
		*initialized = true
		binding.qualifyCodexClient(request.Params)
		return success(request.ID, map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]bool{"listChanged": false}}, "serverInfo": map[string]string{"name": "peerturn", "version": "0.0.0"}, "instructions": "Session-scoped proxy. The daemon checks binding; peer_status reports actual capabilities. After the owner pastes an invitation with the owner-prompt hook active, call peer_join({}) without the invitation as an argument; multiple pending invitations require an owner-selected nonsecret handle. coauthor is strong trust with normal agent capabilities; peer text can influence later owner turns."})
	}
	if !*initialized {
		return failure(request.ID, -32002, "Initialize first")
	}
	switch request.Method {
	case "ping":
		if !basicParams(request.Params) {
			return failure(request.ID, -32602, "Invalid parameters")
		}
		return success(request.ID, struct{}{})
	case "tools/list":
		if !listParams(request.Params) {
			return failure(request.ID, -32602, "Invalid parameters")
		}
		return success(request.ID, map[string]any{"tools": tools()})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      json.RawMessage `json:"_meta,omitempty"`
			Task      json.RawMessage `json:"task,omitempty"`
		}
		if err := codec.Decode(request.Params, maxLine, &params); err != nil || !validRequestMeta(params.Meta) || !validTask(params.Task) {
			return failure(request.ID, -32602, "Invalid tool parameters")
		}
		command, err := toolRequest(params.Name, params.Arguments)
		if err != nil {
			return failure(request.ID, -32602, "Invalid tool arguments")
		}
		result, err := binding.Call(ctx, command, params.Meta)
		if err != nil {
			encoded, marshalErr := json.Marshal(localipc.PublicError(err))
			if marshalErr != nil {
				return failure(request.ID, -32603, "Operation failed")
			}
			return toolResult(request.ID, string(encoded), true)
		}
		response := toolResult(request.ID, string(result), false)
		if command.Method == "inbox" {
			response.delivered, err = localipc.InformDeliveryIDs(result)
			if err != nil {
				encoded, marshalErr := json.Marshal(localipc.PublicError(err))
				if marshalErr != nil {
					return failure(request.ID, -32603, "Operation failed")
				}
				return toolResult(request.ID, string(encoded), true)
			}
		}
		return response
	default:
		return failure(request.ID, -32601, "Method not found")
	}
}

func initialize(params json.RawMessage) (string, error) {
	var input struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		Capabilities    map[string]json.RawMessage `json:"capabilities"`
		ClientInfo      json.RawMessage            `json:"clientInfo"`
		Meta            json.RawMessage            `json:"_meta,omitempty"`
	}
	if err := codec.Decode(params, maxInitialize, &input); err != nil {
		return "", err
	}
	if len(input.ProtocolVersion) > 32 || input.ProtocolVersion == "" || input.Capabilities == nil || !validImplementation(input.ClientInfo) || !validClientCapabilities(input.Capabilities) || !validRequestMeta(input.Meta) {
		return "", localipc.NewError("invalid_encoding", "rejected")
	}
	switch input.ProtocolVersion {
	case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
		return input.ProtocolVersion, nil
	default:
		return "2025-11-25", nil
	}
}

func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(id))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	switch v := value.(type) {
	case string:
		return len(v) <= 256
	case json.Number:
		return len(v) <= 32
	default:
		return false
	}
}

func success(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}
func failure(id json.RawMessage, code int, message string) *rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}
func toolResult(id json.RawMessage, content string, failed bool) *rpcResponse {
	return success(id, map[string]any{"content": []map[string]string{{"type": "text", "text": content}}, "isError": failed})
}

type completeWriter struct{ io.Writer }

func (w completeWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return n, err
}
