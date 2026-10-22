package daemon

import (
	"context"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/protocol"
	"github.com/temirlankasen/peerturn/internal/store"
)

func terminalClaudeError(err error) bool {
	switch localipc.PublicError(err).Code {
	case "revoked", "expired", "invalid_encoding", "invalid_grant", "invalid_certificate", "scope_denied", "too_large":
		return true
	default:
		return false
	}
}

func (d *Daemon) rejectClaudeItem(ctx context.Context, id string, reason error) error {
	if !terminalClaudeError(reason) {
		return reason
	}
	r, err := d.db.RejectPendingDelivery(ctx, id, localipc.PublicError(reason).Code, unixNow())
	if err != nil {
		return err
	}
	return d.queueClaudeReceipt(ctx, r)
}

// Losing a return grant cannot undo local custody or starve another sender.
func (d *Daemon) queueClaudeReceipt(ctx context.Context, r protocol.ReceiptPayload) error {
	err := d.queueNativeReceipt(ctx, r)
	if err != nil && terminalClaudeError(err) {
		return nil
	}
	return err
}

func (d *Daemon) claudeEligible(ctx context.Context, route envelope.Routing) ([]store.InboxEntry, error) {
	items, err := d.db.PendingRoute(ctx, route, 100)
	if err != nil {
		return nil, err
	}
	var eligible []store.InboxEntry
	for _, item := range items {
		if route == envelope.Live {
			_, err = d.framedEntry(item, "coauthor")
		} else {
			_, err = d.claudeSender(item)
		}
		if err != nil {
			if err = d.rejectClaudeItem(ctx, item.MessageID, err); err != nil {
				return nil, err
			}
			continue
		}
		eligible = append(eligible, item)
	}
	return eligible, nil
}

func (d *Daemon) claudeAttemptClosed(ctx context.Context, r localipc.Request) bool {
	previous, err := d.db.Receipt(ctx, r.MessageID)
	if err != nil || previous.AttemptID != r.AttemptID {
		return false
	}
	switch previous.Stage {
	case "rejected", "revoked", "expired", "cancelled", "adapter_accepted", "in_doubt", "model_input", "turn_completed", "reply":
		return true
	case "receiver_stored":
		return previous.Outcome == "not_sent"
	default:
		return false
	}
}
