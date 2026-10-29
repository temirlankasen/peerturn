package codex

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

const rpcTimeout = 5 * time.Second

type socketRPC struct {
	socketPath      string
	daemonID        string
	mu              sync.Mutex
	next            uint64
	closed          bool
	version         string
	observedVersion string
}

// Open qualifies an existing shared daemon over WebSocket on a private UDS.
// Each RPC uses a fresh connection, so no idle thread subscriptions accumulate.
// Empty paths select DefaultSocketPath; this never starts or rebinds a daemon.
func Open(ctx context.Context, socketPath string) (*Transport, error) {
	if socketPath == "" {
		var err error
		socketPath, err = DefaultSocketPath()
		if err != nil {
			return nil, err
		}
	}
	_, daemonID, err := probeSocket(socketPath)
	if err != nil {
		return nil, err
	}
	rpc := &socketRPC{socketPath: socketPath, daemonID: daemonID}
	t := &Transport{rpc: rpc, daemonID: daemonID, socketPath: socketPath}
	if err = t.initialize(ctx); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Transport) initialize(ctx context.Context) error {
	var response initializeResponse
	if err := t.rpc.Call(ctx, "initialize", initializeParams(), &response); err != nil {
		return err
	}
	version, err := agentVersion(response)
	if err == nil {
		t.version = version
	}
	return err
}

type initializeResponse struct {
	UserAgent string `json:"userAgent"`
}

func initializeParams() map[string]any {
	return map[string]any{
		"clientInfo": map[string]string{"name": "peerturn", "title": "PeerTurn", "version": "1"},
		"capabilities": map[string]any{
			"experimentalApi": true,
			"optOutNotificationMethods": []string{
				"item/agentMessage/delta", "item/reasoning/textDelta",
				"item/reasoning/summaryTextDelta", "item/commandExecution/outputDelta",
				"item/fileChange/outputDelta", "item/mcpToolCall/progress",
			},
		},
	}
}

func agentVersion(response initializeResponse) (string, error) {
	ua, _, _ := strings.Cut(response.UserAgent, " (")
	pos := strings.LastIndexByte(ua, '/')
	version := ""
	if len(response.UserAgent) <= 1024 && pos >= 1 {
		version = ua[pos+1:]
	}
	return runtimepolicy.Evaluate(runtimepolicy.Codex, version).Version, runtimepolicy.Check(runtimepolicy.Codex, version)
}

func allowedRPCMethod(method string) bool {
	switch method {
	case "initialize", "thread/loaded/list", "thread/read", "thread/turns/list", "thread/queue/list", "thread/queue/add", "thread/queue/delete":
		return true
	default:
		return false
	}
}

func (r *socketRPC) Call(ctx context.Context, method string, params, result any) error {
	if !allowedRPCMethod(method) {
		return failure("unsupported")
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return failure("unavailable")
	}
	timeout := rpcTimeout
	if method == "thread/turns/list" {
		timeout = ownerTurnHistoryTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeSocket(conn)
	data, err := r.exchange(ctx, conn, "initialize", initializeParams())
	if err != nil {
		return err
	}
	var initialized initializeResponse
	if err = json.Unmarshal(data, &initialized); err != nil {
		return r.contractFailure("initialize", "connect")
	}
	version, err := agentVersion(initialized)
	r.mu.Lock()
	r.observedVersion = version
	r.mu.Unlock()
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.version == "" {
		r.version = version
	}
	changed := r.version != version
	r.mu.Unlock()
	if changed {
		return failure("needs_rebind")
	}
	if err = conn.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","method":"initialized"}`)); err != nil {
		return failure("unavailable")
	}
	if method != "initialize" {
		data, err = r.exchange(ctx, conn, method, params)
		if err != nil {
			return err
		}
	}
	if err = json.Unmarshal(data, result); err != nil {
		return r.contractFailure(method, "response")
	}
	return nil
}

// RuntimeVersion reports the last initialized server without changing the bound
// incarnation version. A different report still requires an explicit rebind.
func (t *Transport) RuntimeVersion() string {
	if rpc, ok := t.rpc.(*socketRPC); ok {
		rpc.mu.Lock()
		defer rpc.mu.Unlock()
		return rpc.observedVersion
	}
	return t.version
}

func (r *socketRPC) connect(ctx context.Context) (*websocket.Conn, error) {
	physical, daemonID, err := probeSocket(r.socketPath)
	if err != nil || daemonID != r.daemonID {
		return nil, failure("needs_rebind")
	}
	dialer := &net.Dialer{}
	httpTransport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", physical)
	}}
	defer httpTransport.CloseIdleConnections()
	client := &http.Client{Transport: httpTransport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	conn, response, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		if response != nil && response.Body != nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				return nil, failure("unavailable")
			}
		}
		return nil, failure("unavailable")
	}
	_, currentID, err := probeSocket(r.socketPath)
	if err != nil || currentID != r.daemonID {
		closeSocket(conn)
		return nil, failure("needs_rebind")
	}
	conn.SetReadLimit(1024 * 1024)
	return conn, nil
}

func (r *socketRPC) exchange(ctx context.Context, conn *websocket.Conn, method string, params any) (json.RawMessage, error) {
	r.mu.Lock()
	r.next++
	requestID := r.next
	r.mu.Unlock()
	request := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{JSONRPC: "2.0", ID: requestID, Method: method, Params: params}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, failure("invalid_input")
	}
	if err = conn.Write(ctx, websocket.MessageText, data); err != nil {
		return nil, failure("unavailable")
	}
	for {
		kind, message, readErr := conn.Read(ctx)
		if readErr != nil || kind != websocket.MessageText {
			return nil, failure("unavailable")
		}
		var response struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *RPCError       `json:"error"`
		}
		if err = json.Unmarshal(message, &response); err != nil {
			return nil, r.contractFailure(method, "response")
		}
		if response.Method != "" {
			if err = refuseServerRequest(ctx, conn, response.ID); err != nil {
				return nil, err
			}
			continue
		}
		var responseID uint64
		if err = json.Unmarshal(response.ID, &responseID); err != nil || responseID != request.ID {
			continue
		}
		if response.Error != nil {
			return nil, response.Error
		}
		if len(response.Result) == 0 || string(response.Result) == "null" {
			return nil, r.contractFailure(method, "response")
		}
		return response.Result, nil
	}
}

func refuseServerRequest(ctx context.Context, conn *websocket.Conn, id json.RawMessage) error {
	if len(id) == 0 || string(id) == "null" {
		return nil
	}
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": -32601, "message": "PeerTurn does not handle server requests"}})
	if err != nil {
		return failure("unavailable")
	}
	if err = conn.Write(ctx, websocket.MessageText, data); err != nil {
		return failure("unavailable")
	}
	return nil
}

func closeSocket(conn *websocket.Conn) {
	// A cleanup error cannot invalidate an acknowledged native response.
	if err := conn.CloseNow(); err != nil {
		return
	}
}

func (r *socketRPC) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}
