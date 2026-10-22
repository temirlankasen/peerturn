package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

const askControlSlots = 6

// AskAdmission contains only cheap, locally obtained admission metadata.
// Empty AttemptID checks pending input before DeliveryIntent. Older callers may
// omit NotificationGrantID only when exactly one reciprocal grant is available.
type AskAdmission struct {
	MessageID, AttemptID, NotificationGrantID string
	Parent                                    identity.Binding
	ParentContext                             []byte
	Now                                       int64
}

func (a AskAdmission) validate() error {
	if !protocol.ValidID(a.MessageID) || (a.AttemptID != "" && !protocol.ValidID(a.AttemptID)) ||
		(a.NotificationGrantID != "" && !protocol.ValidToken(a.NotificationGrantID)) ||
		len(a.ParentContext) == 0 || len(a.ParentContext) > 4096 || a.Now <= 0 || a.Now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return identity.ValidateBinding(a.Parent)
}

// CheckAskAdmission checks authority, concurrency, turn budget and reverse-lane
// headroom without reserving a turn, run, delivery attempt or notification.
// ReserveAsk repeats these checks atomically with its durable reservation.
func (s *Store) CheckAskAdmission(ctx context.Context, a AskAdmission) error {
	if err := a.validate(); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		_, _, err := s.checkAskAdmission(ctx, tx, a)
		return err
	})
}

func (s *Store) checkAskAdmission(ctx context.Context, tx *sql.Tx, r AskAdmission) (askRecord, envelope.BudgetSnapshot, error) {
	var a askRecord
	a.MessageID, a.AttemptID, a.NotificationGrantID = r.MessageID, r.AttemptID, r.NotificationGrantID
	a.Parent, a.ParentContext = r.Parent, r.ParentContext
	var b envelope.BudgetSnapshot
	var route envelope.Routing
	var inboxState string
	err := tx.QueryRowContext(ctx, `SELECT i.grant_id,i.conversation_id,i.budget_id,i.hop,i.expires_at,i.route,i.state,i.epoch,g.lease_generation FROM inbox i JOIN grants g ON g.id=i.grant_id WHERE i.id=?`, r.MessageID).Scan(&a.grantID, &a.conversation, &a.chainBudget, &a.hop, &a.expiry, &route, &inboxState, &a.epoch, &a.currentGeneration)
	if err != nil {
		return a, b, lookupError(err)
	}
	a.generation = a.currentGeneration
	if route != envelope.Responder || (r.AttemptID == "" && inboxState != "pending") {
		return a, b, failure(InvalidState)
	}
	if r.AttemptID != "" {
		var epoch uint64
		if err = tx.QueryRowContext(ctx, `SELECT state,grant_epoch,lease_generation FROM delivery_attempts WHERE id=? AND message_id=?`, r.AttemptID, r.MessageID).Scan(&a.attemptState, &epoch, &a.generation); err != nil {
			return a, b, lookupError(err)
		}
		if inboxState != "intent" || a.attemptState != "intent" {
			return a, b, failure(InvalidState)
		}
		if epoch != a.epoch {
			return a, b, failure(fault.Revoked)
		}
	}
	g, err := s.authorizeAsk(ctx, tx, a, r.Now)
	if err != nil {
		return a, b, err
	}
	var sameParent, sameConversation, active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(r.parent_binding=?),0),COALESCE(SUM(i.conversation_id=? AND i.channel=?),0) FROM responder_runs r JOIN inbox i ON i.id=r.message_id WHERE r.state NOT IN ('reply_committed','failed','cancelled')`, bindingKey(r.Parent), a.conversation, channelKey(g.Subject, g.Issuer)).Scan(&active, &sameParent, &sameConversation); err != nil {
		return a, b, failure(StorageUnavailable)
	}
	if active >= 2 || sameParent != 0 || sameConversation != 0 {
		return a, b, failure(fault.Conflict)
	}
	if b, err = budgetSnapshot(ctx, tx, g, r.Now); err != nil {
		return a, b, err
	}
	if b.TurnWindowStart > r.Now || b.TurnsInTenMinutes >= g.Limits.MaxTurnsPerTenMinutes {
		return a, b, failure(fault.BudgetExceeded)
	}
	return a, b, s.askControlCapacity(ctx, tx, g, r.NotificationGrantID, r.Now)
}

func (s *Store) askControlCapacity(ctx context.Context, tx *sql.Tx, inbound grant.Grant, exactID string, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM grants WHERE issuer=? AND subject=? AND revoked=0 AND expires_at>? AND (?='' OR id=?)`, bindingKey(inbound.Subject), bindingKey(inbound.Issuer), now, exactID, exactID)
	if err != nil {
		return failure(StorageUnavailable)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return failure(StorageUnavailable)
	}
	if len(ids) != 1 {
		return failure(fault.ScopeDenied)
	}
	outbound, err := s.authorization(ctx, tx, ids[0], now)
	if err != nil {
		return err
	}
	if !identity.EqualBinding(outbound.Subject, inbound.Issuer) || !identity.EqualBinding(outbound.Issuer, inbound.Subject) ||
		outbound.ReplyRoute == nil || !identity.EqualBinding(*outbound.ReplyRoute, inbound.Issuer) {
		return failure(fault.ScopeDenied)
	}
	if err = grant.CheckScope(outbound.Scope, "reply"); err != nil {
		return err
	}
	// Six maximum-sized packets leave room for all progress and terminal notices.
	// Other control traffic can still race later; settlement never depends on it.
	for _, limit := range []struct {
		query           string
		messages, bytes int
	}{
		{`SELECT COUNT(*),COALESCE(SUM(bytes),0) FROM outbox WHERE grant_id=? AND lane='control' AND stage='sender_stored' AND priority=0`, MaxControlMessages, MaxControlBytes},
		{`SELECT COUNT(*),COALESCE(SUM(bytes),0) FROM outbox WHERE grant_id=? AND lane='control' AND priority=0`, MaxControlMessages * 64, MaxControlBytes * 8},
	} {
		var count, used int
		if err = tx.QueryRowContext(ctx, limit.query, ids[0]).Scan(&count, &used); err != nil {
			return failure(StorageUnavailable)
		}
		if count > limit.messages-askControlSlots || used > limit.bytes-askControlSlots*protocol.MaxPacket {
			return failure(Backpressure)
		}
	}
	return nil
}
