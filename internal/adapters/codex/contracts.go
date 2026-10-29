package codex

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

func (t *Transport) contractFailure(id, phase string, capabilities ...string) *runtimepolicy.Error {
	return runtimepolicy.ContractFailure(runtimepolicy.Codex, t.version, id, phase, capabilities...)
}

func (t *Transport) contractCallError(err error, id, phase string, capabilities ...string) error {
	if runtimepolicy.ErrorCode(err) != "" {
		return err
	}
	var native *RPCError
	var malformed *json.UnmarshalTypeError
	var syntax *json.SyntaxError
	if errors.As(err, &malformed) || errors.As(err, &syntax) ||
		(errors.As(err, &native) && native != nil && (native.Code == -32601 || native.Code == -32600 || native.Code == -32602)) {
		return t.contractFailure(id, phase, capabilities...)
	}
	if IsCode(err, "needs_rebind") {
		return err
	}
	return failure("unavailable")
}

func bindingFailure(err error) bool {
	if failure, ok := errors.AsType[*runtimepolicy.Error](err); ok && slices.Contains(failure.AffectedCapabilities, "binding") {
		return true
	}
	return IsCode(err, "needs_rebind") || IsCode(err, "unsupported_version") || IsCode(err, "runtime_version_unknown")
}

func (t *Transport) fenceCoauthor(failure *runtimepolicy.Error) *runtimepolicy.Error {
	if failure.ContractID == "codex.queue_add" {
		t.contractMu.Lock()
		t.coauthorFailure = failure
		t.contractMu.Unlock()
	}
	return failure
}

func (t *Transport) coauthorFence() *runtimepolicy.Error {
	t.contractMu.Lock()
	defer t.contractMu.Unlock()
	return t.coauthorFailure
}

func (r *socketRPC) contractFailure(method, phase string) *runtimepolicy.Error {
	r.mu.Lock()
	version := r.version
	r.mu.Unlock()
	id, capabilities := "codex.initialize", []string{"binding", "coauthor", "owner_invitation", "inform_boundary"}
	switch method {
	case "thread/loaded/list":
		id = "codex.loaded_list"
	case "thread/read":
		id = "codex.thread_read"
	case "thread/turns/list":
		id, capabilities = "codex.owner_turn", []string{"owner_invitation", "inform_boundary"}
	case "thread/queue/list":
		id, capabilities = "codex.queue_list", []string{"coauthor"}
	case "thread/queue/add":
		id, capabilities = "codex.queue_add", []string{"coauthor"}
	case "thread/queue/delete":
		id, capabilities = "codex.queue_delete", []string{"local_revoke"}
	}
	return runtimepolicy.ContractFailure(runtimepolicy.Codex, version, id, phase, capabilities...)
}
