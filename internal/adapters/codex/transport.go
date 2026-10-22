package codex

import (
	"context"
	"errors"
	"reflect"

	"github.com/temirlankasen/peerturn/internal/protocol"
)

// Queue makes one submit attempt to the bound live thread's next-turn queue.
// Stable client IDs correlate evidence; the native queue does not promise dedup.
func (t *Transport) Queue(ctx context.Context, b Binding, messageID, framedText string) (QueueResult, error) {
	if !protocol.ValidID(messageID) {
		return QueueResult{}, failure("invalid_input")
	}
	if err := validateFrame(framedText, messageID); err != nil {
		return QueueResult{}, err
	}
	if err := t.Validate(ctx, b); err != nil {
		return QueueResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return QueueResult{}, failure("unavailable")
	}
	clientID := "peerturn:" + messageID
	params := queueParams{ThreadID: b.ThreadID, ClientUserMessageID: clientID,
		Input: []nativeInput{{Type: "text", Text: framedText, TextElements: []any{}}}}
	var response struct {
		QueuedSubmission queuedSubmission `json:"queuedSubmission"`
	}
	if err := t.rpc.Call(ctx, "thread/queue/add", params, &response); err != nil {
		var native *RPCError
		if errors.As(err, &native) && native != nil {
			switch native.Code {
			case -32601:
				return QueueResult{}, failure("unsupported")
			case -32600, -32602:
				return QueueResult{}, failure("rejected")
			}
		}
		return QueueResult{}, failure("in_doubt")
	}
	item := response.QueuedSubmission
	if !protocol.ValidToken(item.ID) || item.ClientUserMessageID != clientID || !reflect.DeepEqual(item.Input, params.Input) {
		return QueueResult{}, failure("in_doubt")
	}
	return QueueResult{ItemIDs: []string{item.ID}, ClientUserMessageID: clientID}, nil
}

// Delete removes a still-queued item, including from an unloaded bound thread.
// False or error does not prove whether dispatch already consumed the item.
func (t *Transport) Delete(ctx context.Context, b Binding, itemID string) (bool, error) {
	if !protocol.ValidToken(itemID) {
		return false, failure("invalid_input")
	}
	if err := t.validateIdentity(b); err != nil {
		return false, err
	}
	if ctx.Err() != nil {
		return false, failure("unavailable")
	}
	params := struct {
		ThreadID           string `json:"threadId"`
		QueuedSubmissionID string `json:"queuedSubmissionId"`
	}{ThreadID: b.ThreadID, QueuedSubmissionID: itemID}
	var response struct {
		Deleted *bool `json:"deleted"`
	}
	if err := t.rpc.Call(ctx, "thread/queue/delete", params, &response); err != nil {
		return false, failure("in_doubt")
	}
	if response.Deleted == nil {
		return false, failure("in_doubt")
	}
	return *response.Deleted, nil
}

// Close prevents further calls without touching native sessions or the daemon.
func (t *Transport) Close() error {
	if t == nil {
		return nil
	}
	if closer, ok := t.rpc.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
