package codex

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

// Probe checks exact live binding and queue service without submitting input.
// A passing probe does not establish queue/add or queue/delete semantics.
func (t *Transport) Probe(ctx context.Context, threadID string) (Binding, Capabilities, error) {
	return t.probeBinding(ctx, threadID)
}

func (t *Transport) probeBinding(ctx context.Context, threadID string) (Binding, Capabilities, error) {
	b, err := t.bindThread(ctx, threadID)
	if err != nil {
		return Binding{}, Capabilities{}, err
	}
	caps := Capabilities{AttachExisting: true, Version: t.version,
		SupportedScopes: []string{}, ReceiptStages: []string{}}
	initializeStatus := "not_observed"
	if t.socketPath != "" {
		initializeStatus = "passed"
	}
	caps.Contracts = []runtimepolicy.Contract{
		{ID: "codex.initialize", Phase: "connect", Status: initializeStatus, Capabilities: []string{"binding", "coauthor", "owner_invitation", "inform_boundary"}},
		{ID: "codex.loaded_list", Phase: "bind", Status: "passed", Capabilities: []string{"binding", "coauthor", "owner_invitation", "inform_boundary"}},
		{ID: "codex.thread_read", Phase: "bind", Status: "passed", Capabilities: []string{"binding", "coauthor", "owner_invitation", "inform_boundary"}},
	}
	err = t.checkQueueContract(ctx, b)
	observation := runtimepolicy.Contract{ID: "codex.queue_list", Phase: "bind", Status: "passed", Capabilities: []string{"coauthor"}}
	if err != nil {
		observation.Status = "failed"
		observation.Action = t.contractFailure("codex.queue_list", "bind", "coauthor").Action
		if failure, ok := errors.AsType[*runtimepolicy.Error](err); ok {
			observation.ID, observation.Phase = failure.ContractID, failure.Phase
			observation.Code = failure.Code
			observation.Capabilities, observation.Action = failure.AffectedCapabilities, failure.Action
		} else if !bindingFailure(err) {
			observation.Status = "unavailable"
		}
		caps.Contracts = append(caps.Contracts, observation)
		if bindingFailure(err) {
			caps.AttachExisting = false
			return Binding{}, caps, err
		}
		return b, caps, err
	}
	caps.Contracts = append(caps.Contracts, observation)
	caps.WakeIdle = true
	caps.SupportedScopes = []string{"coauthor"}
	caps.ReceiptStages = []string{"adapter_accepted"}
	return b, caps, nil
}

func (t *Transport) checkQueueContract(ctx context.Context, b Binding) error {
	if failure := t.coauthorFence(); failure != nil {
		return failure
	}
	var response struct {
		Data []struct {
			ID                  string            `json:"id"`
			Input               []json.RawMessage `json:"input"`
			ClientUserMessageID *string           `json:"clientUserMessageId"`
		} `json:"data"`
		NextCursor string `json:"nextCursor"`
	}
	params := struct {
		ThreadID string `json:"threadId"`
		Limit    int    `json:"limit"`
	}{ThreadID: b.ThreadID, Limit: 1}
	if err := t.rpc.Call(ctx, "thread/queue/list", params, &response); err != nil {
		return t.contractCallError(err, "codex.queue_list", "bind", "coauthor")
	}
	if response.Data == nil || len(response.Data) > 1 || len(response.NextCursor) > 1024 {
		return t.contractFailure("codex.queue_list", "bind", "coauthor")
	}
	for _, item := range response.Data {
		if !protocol.ValidToken(item.ID) || item.Input == nil || len(item.Input) > 1024 || item.ClientUserMessageID == nil || len(*item.ClientUserMessageID) > 4096 {
			return t.contractFailure("codex.queue_list", "bind", "coauthor")
		}
		for _, input := range item.Input {
			var shape struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(input, &shape) != nil || shape.Type == "" || len(shape.Type) > 64 {
				return t.contractFailure("codex.queue_list", "bind", "coauthor")
			}
		}
	}
	return nil
}
