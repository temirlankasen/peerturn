package mcp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

// Host turn metadata includes workspace/tracing context, unlike initialize.
// Keep its own bound below the overall request cap without relaxing decoding.
const maxToolMeta = 64 * 1024

type sessionBinding struct {
	options      Options
	bound        *capabilitySession
	nativeThread string
	codexClient  bool
	codexVersion string
	codexFailure *runtimepolicy.Error
}

func newSessionBinding(ctx context.Context, options Options) (*sessionBinding, error) {
	mixedCurrent := options.ResolveCodex != nil || options.CodexThread != "" || options.Session != "" ||
		options.Caller != nil || options.ExpectedBindingEpoch != "" || options.ExpectedRuntimeVersion != ""
	if options.ResolveCurrent != nil && mixedCurrent {
		return nil, localipc.NewError("binding_mismatch", "needs_rebind")
	}
	deferred := options.ResolveCodex != nil || options.ResolveCurrent != nil
	if !deferred && (options.Session == "" || options.Caller == nil) {
		return nil, localipc.NewError("unbound", "unbound")
	}
	s := &sessionBinding{options: options, nativeThread: options.CodexThread}
	if !deferred && options.CodexThread == "" {
		var err error
		s.bound, err = bindCapability(ctx, options)
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *sessionBinding) Close() error {
	if s.bound == nil {
		return nil
	}
	return s.bound.Close()
}

func (s *sessionBinding) Call(ctx context.Context, command localipc.Request, meta json.RawMessage) (json.RawMessage, error) {
	if s.options.ResolveCurrent != nil {
		return s.callCurrent(ctx, command, meta)
	}
	if s.codexFailure != nil {
		return nil, localipc.RuntimeError(s.codexFailure)
	}
	if s.options.ResolveCodex != nil || s.options.CodexThread != "" {
		if s.options.ResolveCodex != nil && !s.codexClient {
			return nil, localipc.NewError("binding_error", "needs_rebind")
		}
		thread, err := codexToolThread(meta)
		if err != nil {
			return nil, err
		}
		if s.nativeThread != "" && thread != s.nativeThread {
			return nil, localipc.NewError("binding_mismatch", "needs_rebind")
		}
		// Pin even a refused handshake: one MCP connection cannot try another
		// native session after failure or silently follow a host thread switch.
		s.nativeThread = thread
		if s.bound == nil {
			options := s.options
			options.ExpectedRuntimeVersion = s.codexVersion
			if options.ResolveCodex != nil {
				options.Caller, err = options.ResolveCodex(thread)
				if err != nil {
					return nil, err
				}
				options.Session = thread
			}
			if options.Session != thread || options.Caller == nil {
				return nil, localipc.NewError("binding_mismatch", "needs_rebind")
			}
			s.bound, err = bindCapability(ctx, options)
			if err != nil {
				s.rememberCodexFailure(err)
				return nil, err
			}
		}
	} else if len(meta) != 0 {
		var informational map[string]json.RawMessage
		if codec.Decode(meta, maxToolMeta, &informational) != nil || informational == nil {
			return nil, localipc.NewError("invalid_encoding", "rejected")
		}
	}
	if s.codexClient {
		if err := s.bound.checkRuntimeVersion(s.codexVersion); err != nil {
			s.rememberCodexFailure(err)
			return nil, errors.Join(err, s.bound.Close())
		}
		s.bound.options.ExpectedRuntimeVersion = s.codexVersion
	}
	result, err := s.bound.Call(ctx, command)
	s.rememberCodexFailure(err)
	if err != nil || command.Method != "status" || !s.codexClient {
		return result, err
	}
	return s.codexStatus(result)
}

func (s *sessionBinding) rememberCodexFailure(err error) {
	if failure, ok := errors.AsType[*runtimepolicy.Error](err); ok && s.codexClient {
		s.codexFailure = failure
	}
}

func (s *sessionBinding) qualifyCodexClient(params json.RawMessage) {
	if s.options.ResolveCurrent != nil {
		return
	}
	s.codexClient = qualifiedCodexClient(params)
	var input map[string]json.RawMessage
	var name, version string
	if codec.Decode(params, maxInitialize, &input) != nil ||
		!informationalFields(input["clientInfo"], map[string]any{"name": &name, "version": &version}) || name != "codex-mcp-client" {
		return
	}
	s.codexVersion = runtimepolicy.Evaluate(runtimepolicy.Codex, version).Version
	if failure, ok := errors.AsType[*runtimepolicy.Error](runtimepolicy.Check(runtimepolicy.Codex, version)); ok {
		s.codexFailure = failure
	}
}

func (s *sessionBinding) codexStatus(result json.RawMessage) (json.RawMessage, error) {
	var status map[string]json.RawMessage
	if err := json.Unmarshal(result, &status); err != nil || status == nil {
		return nil, localipc.RuntimeError(runtimepolicy.ContractFailure(runtimepolicy.Codex, s.codexVersion, "codex.mcp_status", "mcp", "mcp"))
	}
	if raw, exists := status["runtime_version"]; exists {
		var actual string
		if json.Unmarshal(raw, &actual) != nil || runtimepolicy.Evaluate(runtimepolicy.Codex, actual).Version != s.codexVersion {
			s.codexFailure = runtimepolicy.ContractFailure(runtimepolicy.Codex, s.codexVersion, "codex.mcp_version", "mcp", "mcp")
			return nil, errors.Join(localipc.RuntimeError(s.codexFailure), s.bound.Close())
		}
	}
	policy, err := json.Marshal(runtimepolicy.Evaluate(runtimepolicy.Codex, s.codexVersion))
	if err != nil {
		return nil, errors.Join(localipc.NewError("unavailable", "unavailable"), err)
	}
	status["mcp_client_policy"] = policy
	return json.Marshal(status)
}

// Version admission cannot grant authority;
// the exact host thread selector must still pass the daemon's native handshake.
func qualifiedCodexClient(params json.RawMessage) bool {
	var input map[string]json.RawMessage
	if codec.Decode(params, maxInitialize, &input) != nil {
		return false
	}
	var name, version string
	return informationalFields(input["clientInfo"], map[string]any{"name": &name, "version": &version}) && name == "codex-mcp-client" && runtimepolicy.Evaluate(runtimepolicy.Codex, version).Allowed()
}

func codexToolThread(raw json.RawMessage) (string, error) {
	var meta map[string]json.RawMessage
	if codec.Decode(raw, maxToolMeta, &meta) != nil || meta == nil {
		return "", localipc.NewError("binding_error", "needs_rebind")
	}
	var thread string
	if codec.Decode(meta["threadId"], 128, &thread) != nil || !canonicalThreadID(thread) {
		return "", localipc.NewError("binding_error", "needs_rebind")
	}
	var session string
	if value, ok := meta["sessionId"]; ok && (codec.Decode(value, 128, &session) != nil || !canonicalThreadID(session)) {
		return "", localipc.NewError("binding_error", "needs_rebind")
	}
	if nested, ok := meta["x-codex-turn-metadata"]; ok {
		var turn map[string]json.RawMessage
		if codec.Decode(nested, maxToolMeta, &turn) != nil || turn == nil {
			return "", localipc.NewError("binding_error", "needs_rebind")
		}
		for key, expected := range map[string]string{"thread_id": thread, "session_id": session} {
			if value, exists := turn[key]; exists {
				var id string
				if codec.Decode(value, 128, &id) != nil || !canonicalThreadID(id) || (expected != "" && id != expected) {
					return "", localipc.NewError("binding_mismatch", "needs_rebind")
				}
			}
		}
	}
	return thread, nil
}

func canonicalThreadID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' || strings.ToLower(id) != id {
		return false
	}
	compact := strings.ReplaceAll(id, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}
