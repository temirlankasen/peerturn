package codex

import "context"

// Probe checks exact live binding and queue service without submitting input.
// Native queue acceptance is the only receipt stage established by this API.
func (t *Transport) Probe(ctx context.Context, threadID string) (Binding, Capabilities, error) {
	b, err := t.Bind(ctx, threadID)
	if err != nil {
		return Binding{}, Capabilities{}, err
	}
	caps := Capabilities{AttachExisting: true, Version: t.version,
		SupportedScopes: []string{}, ReceiptStages: []string{}}
	var response struct {
		Data []struct{} `json:"data"`
	}
	params := struct {
		ThreadID string `json:"threadId"`
		Limit    int    `json:"limit"`
	}{ThreadID: threadID, Limit: 1}
	if err = t.rpc.Call(ctx, "thread/queue/list", params, &response); err != nil {
		return b, caps, failure("unsupported")
	}
	if response.Data == nil || len(response.Data) > 1 {
		return b, caps, failure("unsupported")
	}
	caps.WakeIdle = true
	caps.SupportedScopes = []string{"coauthor"}
	caps.ReceiptStages = []string{"adapter_accepted"}
	return b, caps, nil
}
