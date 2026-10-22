package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/temirlankasen/peerturn/internal/claude"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

type adapterEvent struct {
	Type          string `json:"type"`
	MessageID     string `json:"message_id,omitempty"`
	AttemptID     string `json:"attempt_id,omitempty"`
	Text          string `json:"text,omitempty"`
	InformPending int    `json:"inform_pending"`
}

func (d *Daemon) framedEntry(item store.InboxEntry, scope string) (string, error) {
	sender, err := d.claudeSender(item)
	if err != nil {
		return "", err
	}
	text, err := claude.Frame(sender, scope, item.MessageID, string(item.Body))
	if err != nil {
		return "", localipc.NewError("invalid_encoding", "rejected")
	}
	if len(text) > 128*1024 {
		return "", localipc.NewError("too_large", "rejected")
	}
	return text, nil
}

func (d *Daemon) claudeSender(item store.InboxEntry) (string, error) {
	for _, p := range d.state.Peers {
		g, err := d.incomingGrant(p)
		if err == nil && g.GrantID == item.GrantID && !p.IncomingRevoked {
			if _, _, err = d.outgoing(p); err != nil {
				return "", err
			}
			return g.Subject.IdentityID + "/" + g.Subject.SessionID, nil
		}
	}
	return "", localipc.NewError("revoked", "revoked")
}

func (d *Daemon) claudePoll(ctx context.Context) (any, error) {
	count, err := d.db.PendingInformCount(ctx)
	if err != nil {
		return nil, err
	}
	status := adapterEvent{Type: "status", InformPending: count}
	if err = d.rejectUnavailableResponders(ctx); err != nil {
		return nil, err
	}
	uncertain, err := d.db.UncertainDeliveries(ctx)
	if err != nil {
		return nil, err
	}
	if uncertain {
		return status, nil
	}
	if d.claudeAttempt != nil {
		item, itemErr := d.db.Inbox(ctx, d.claudeAttempt.MessageID)
		if itemErr != nil {
			return nil, itemErr
		}
		if item.State == "intent" {
			// Replaying a reservation repairs a lost poll response. Authorization
			// remains single-use and no native call is retried by this response.
			return *d.claudeAttempt, nil
		}
		if item.State == "authorized" {
			return status, nil
		}
		d.claudeAttempt = nil
	}
	items, err := d.claudeEligible(ctx, envelope.Live)
	if err != nil || len(items) == 0 {
		return status, err
	}
	item := items[0]
	text, err := d.framedEntry(item, "coauthor")
	if err != nil {
		return status, d.rejectClaudeItem(ctx, item.MessageID, err)
	}
	attempt, err := d.db.DeliveryIntent(ctx, item.MessageID, randomID(), unixNow())
	if err != nil {
		return nil, err
	}
	if !attempt.Start {
		return status, nil
	}
	event := adapterEvent{Type: "live", MessageID: item.MessageID, AttemptID: attempt.ID, Text: text, InformPending: count}
	if err = d.recordPeerEntryInvites(item); err != nil {
		return nil, err
	}
	d.claudeAttempt = &event
	return event, nil
}

func (d *Daemon) claudeAuthorize(ctx context.Context, r localipc.Request) (any, error) {
	a := d.claudeAttempt
	if a == nil || a.MessageID != r.MessageID || a.AttemptID != r.AttemptID {
		if d.claudeAttemptClosed(ctx, r) {
			return map[string]bool{"authorized": false}, nil
		}
		return nil, localipc.NewError("conflict", "pending")
	}
	if err := d.db.AuthorizeDelivery(ctx, r.MessageID, r.AttemptID, unixNow()); err != nil {
		if terminalClaudeError(err) {
			if d.claudeAttemptClosed(ctx, r) {
				d.claudeAttempt = nil
				return map[string]bool{"authorized": false}, nil
			}
			if rejectErr := d.rejectClaudeItem(ctx, r.MessageID, err); rejectErr != nil {
				return nil, rejectErr
			}
			d.claudeAttempt = nil
			return map[string]bool{"authorized": false}, nil
		}
		return nil, err
	}
	return map[string]bool{"authorized": true}, nil
}

func (d *Daemon) claudeResult(ctx context.Context, r localipc.Request) (any, error) {
	a := d.claudeAttempt
	if a == nil || a.MessageID != r.MessageID || a.AttemptID != r.AttemptID {
		previous, err := d.db.Receipt(ctx, r.MessageID)
		if err == nil && previous.AttemptID == r.AttemptID && (previous.Stage == "adapter_accepted" || previous.Stage == "in_doubt" || previous.Stage == "rejected" || previous.Stage == "revoked" || previous.Stage == "receiver_stored" && previous.Outcome == "not_sent") {
			return map[string]string{"state": previous.Stage}, nil
		}
		return nil, localipc.NewError("conflict", "in_doubt")
	}
	receipt := protocol.ReceiptPayload{MessageID: r.MessageID, AttemptID: r.AttemptID, Stage: "in_doubt", EvidenceKind: "claude_prompt_submit", At: unixNow()}
	switch r.Outcome {
	case "accepted":
		digest := sha256.Sum256([]byte(a.Text))
		authorized, err := d.db.IsDeliveryAuthorized(ctx, r.MessageID, r.AttemptID)
		if err != nil {
			return nil, err
		}
		if !authorized || r.Nonce != hex.EncodeToString(digest[:]) {
			return nil, localipc.NewError("invalid_response", "in_doubt")
		}
		receipt.Stage = "adapter_accepted"
	case "drop":
		receipt.Stage, receipt.ReasonCode = "rejected", "interrupted"
	case "in_doubt":
	default:
		return nil, localipc.NewError("invalid_arguments", "rejected")
	}
	err := d.db.DeliveryResult(ctx, r.AttemptID, receipt, []byte(r.Outcome))
	if err == nil {
		d.claudeAttempt = nil
		err = d.queueNativeReceipt(ctx, receipt)
	}
	return map[string]string{"state": receipt.Stage}, err
}

func (d *Daemon) claudeRelease(ctx context.Context, r localipc.Request) (any, error) {
	a := d.claudeAttempt
	if a == nil || a.MessageID != r.MessageID || a.AttemptID != r.AttemptID {
		if d.claudeAttemptClosed(ctx, r) {
			return map[string]bool{"released": true}, nil
		}
		return nil, localipc.NewError("conflict", "pending")
	}
	err := d.db.ReleaseDelivery(ctx, r.MessageID, r.AttemptID, unixNow())
	return map[string]bool{"released": err == nil}, err
}
