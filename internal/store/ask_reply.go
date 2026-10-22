package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// CommitAskReply atomically retains a complete redacted encrypted reply and its
// signed notification. Late, cancelled, expired or revoked output is discarded.
func (s *Store) CommitAskReply(ctx context.Context, runID string, reply OutboxRequest, r protocol.ReceiptPayload, notification ...NativeNotification) (Custody, error) {
	if !protocol.ValidID(runID) || len(notification) > 1 || r.Stage != "reply" || r.At != reply.Now {
		return Custody{}, failure(fault.InvalidEncoding)
	}
	if err := r.Validate(); err != nil {
		return Custody{}, err
	}
	p, err := protocol.DecodePacket(reply.Packet)
	if err != nil {
		return Custody{}, err
	}
	if err = reply.Message.Validate(); err != nil {
		return Custody{}, err
	}
	m := reply.Message
	if m.Kind != protocol.Reply || m.MessageID != p.Header.MessageID || m.ExpiresAt != p.Header.ExpiresAt || m.GrantID != reply.GrantID {
		return Custody{}, failure(fault.ScopeDenied)
	}
	raw, err := codec.Canonical(p)
	if err != nil {
		return Custody{}, err
	}
	result := Custody{MessageID: m.MessageID, GrantID: reply.GrantID, Stage: "sender_stored"}
	err = s.write(ctx, func(tx *sql.Tx) error {
		a, loadErr := s.loadAsk(ctx, tx, runID)
		if loadErr != nil {
			return loadErr
		}
		if r.MessageID != a.MessageID || r.AttemptID != a.AttemptID ||
			m.ParentID != a.MessageID || m.ReplyTo != a.MessageID || m.ConversationID != a.conversation ||
			m.BudgetID != a.chainBudget || a.hop >= 8 || m.Hop != a.hop+1 {
			return failure(fault.ScopeDenied)
		}
		if a.state != "redacting" || a.attemptState != "reply_redacted" {
			return failure(fault.Conflict)
		}
		inbound, authErr := s.authorizeAsk(ctx, tx, a, reply.Now)
		if authErr != nil {
			return authErr
		}
		outbound, authErr := s.authorization(ctx, tx, reply.GrantID, reply.Now)
		if authErr != nil {
			return authErr
		}
		if !identity.EqualBinding(outbound.Subject, inbound.Issuer) || !identity.EqualBinding(outbound.Issuer, inbound.Subject) ||
			outbound.ReplyRoute == nil || !identity.EqualBinding(*outbound.ReplyRoute, inbound.Issuer) {
			return failure(fault.ScopeDenied)
		}
		if err = validateOutgoing(ctx, tx, reply, p.Header, outbound); err != nil {
			return err
		}
		duplicate, putErr := s.putOutbox(ctx, tx, m.MessageID, reply.GrantID, raw, "data", m.ExpiresAt, reply.Now)
		if putErr != nil {
			return putErr
		}
		if duplicate {
			return failure(fault.Conflict)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE outbox SET channel=?,conversation_id=?,budget_id=?,hop=? WHERE id=?`, channelKey(outbound.Subject, outbound.Issuer), m.ConversationID, m.BudgetID, m.Hop, m.MessageID); err != nil {
			return failure(StorageUnavailable)
		}
		if err = s.recordReceipt(ctx, tx, OutboundReceipt, reply.GrantID, protocol.ReceiptPayload{MessageID: m.MessageID, AttemptID: m.MessageID, Stage: "sender_stored", EvidenceKind: "local_commit", At: reply.Now}); err != nil {
			return err
		}
		return s.setAskStage(ctx, tx, a, "reply_committed", r, notification)
	})
	if err != nil {
		return Custody{}, err
	}
	return result, nil
}
