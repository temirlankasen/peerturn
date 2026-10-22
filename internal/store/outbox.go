package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// Queue ceilings are per grant and lane; a noisy peer cannot consume control space.
const (
	MaxQueuedMessages   = 256
	MaxQueuedBytes      = 8 * 1024 * 1024
	MaxControlMessages  = 128
	MaxControlBytes     = 1024 * 1024
	MaxRetainedMessages = 65536
	MaxRetainedBytes    = 64 * 1024 * 1024
)

// OutboxRequest retains the already sealed immutable wire packet.
type OutboxRequest struct {
	Packet []byte
	// Message is the trusted caller's exact payload already signed into Packet.
	// The store validates its authority and retains chain metadata, never its body.
	Message protocol.MessagePayload
	GrantID string
	Now     int64
}

// Custody is returned only after the transaction's durability boundary.
type Custody struct {
	MessageID string
	GrantID   string
	Stage     string
	Reason    fault.Code
	Duplicate bool
	Frontier  uint64
}

// OutboxEnqueue commits sender_stored; it makes no assertion about network delivery.
func (s *Store) OutboxEnqueue(ctx context.Context, r OutboxRequest) (Custody, error) {
	p, err := protocol.DecodePacket(r.Packet)
	if err != nil {
		return Custody{}, err
	}
	raw, err := codec.Canonical(p)
	if err != nil {
		return Custody{}, err
	}
	if err = r.Message.Validate(); err != nil {
		return Custody{}, err
	}
	if r.Message.MessageID != p.Header.MessageID || r.Message.ExpiresAt != p.Header.ExpiresAt {
		return Custody{}, failure(fault.AudienceMismatch)
	}
	if r.Message.GrantID != r.GrantID {
		return Custody{}, failure(fault.InvalidGrant)
	}
	lane := "data"
	switch r.Message.Kind {
	case protocol.Inform, protocol.Ask, protocol.Coauthor, protocol.Reply:
	case protocol.Receipt:
		lane = "control"
		if _, err = protocol.DecodeReceipt([]byte(r.Message.BodyUTF8)); err != nil {
			return Custody{}, err
		}
	case protocol.KeyUpdate, protocol.GrantUpdate, protocol.Revoke:
		return Custody{}, failure(fault.ScopeDenied)
	default:
		return Custody{}, failure(fault.ScopeDenied)
	}
	result := Custody{MessageID: p.Header.MessageID, Stage: "sender_stored"}
	err = s.write(ctx, func(tx *sql.Tx) error {
		duplicate, insertErr := s.putOutbox(ctx, tx, p.Header.MessageID, r.GrantID, raw, lane, p.Header.ExpiresAt, r.Now)
		if insertErr != nil {
			return insertErr
		}
		result.Duplicate = duplicate
		if duplicate {
			var conversation, budget string
			var hop uint64
			if err = tx.QueryRowContext(ctx, `SELECT stage,conversation_id,budget_id,hop FROM outbox WHERE id=?`, p.Header.MessageID).Scan(&result.Stage, &conversation, &budget, &hop); err != nil {
				return failure(StorageUnavailable)
			}
			if lane == "data" && (conversation != r.Message.ConversationID || budget != r.Message.BudgetID || hop != r.Message.Hop) {
				return failure(fault.Conflict)
			}
			return nil
		}
		g, authErr := s.authorization(ctx, tx, r.GrantID, r.Now)
		if authErr != nil {
			return authErr
		}
		if err = validateOutgoing(ctx, tx, r, p.Header, g); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE outbox SET channel=?,conversation_id=?,budget_id=?,hop=? WHERE id=?`, channelKey(g.Subject, g.Issuer), r.Message.ConversationID, r.Message.BudgetID, r.Message.Hop, p.Header.MessageID)
		if err != nil {
			return failure(StorageUnavailable)
		}
		if err = s.step("outbox_insert"); err != nil {
			return err
		}
		return s.recordReceipt(ctx, tx, OutboundReceipt, r.GrantID, protocol.ReceiptPayload{MessageID: p.Header.MessageID, AttemptID: p.Header.MessageID, Stage: "sender_stored", EvidenceKind: "local_commit", At: r.Now})
	})
	if err != nil {
		return Custody{}, err
	}
	return result, nil
}

func validateOutgoing(ctx context.Context, tx *sql.Tx, r OutboxRequest, h protocol.RoutingHeader, g grant.Grant) error {
	m := r.Message
	if m.FromSession != g.Subject.SessionID || m.ToSession != g.Issuer.SessionID {
		return failure(fault.AudienceMismatch)
	}
	if m.FromSessionEpoch != g.Subject.SessionEpoch || m.ToSessionEpoch != g.Issuer.SessionEpoch || h.RecipientKeyEpoch != g.Issuer.KeyEpoch {
		return failure(fault.EpochMismatch)
	}
	if err := grant.CompareEpoch(m.GrantEpoch, g.GrantEpoch); err != nil {
		return err
	}
	var hash string
	if err := tx.QueryRowContext(ctx, `SELECT hash FROM grants WHERE id=?`, r.GrantID).Scan(&hash); err != nil {
		return failure(StorageUnavailable)
	}
	if m.GrantHash != hash || m.CreatedAt < g.NotBefore || m.ExpiresAt > g.ExpiresAt {
		return failure(fault.InvalidGrant)
	}
	if m.CreatedAt > r.Now {
		return failure(fault.NotYetValid)
	}
	if r.Now >= m.ExpiresAt {
		return failure(fault.Expired)
	}
	if m.Hop > g.Limits.MaxHops {
		return failure(fault.BudgetExceeded)
	}
	return grant.CheckScope(g.Scope, string(m.Kind))
}

func (s *Store) putOutbox(ctx context.Context, tx *sql.Tx, id, grantID string, raw []byte, lane string, expiry, now int64) (bool, error) {
	var collision int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox WHERE id=?`, id).Scan(&collision); err != nil {
		return false, failure(StorageUnavailable)
	}
	if collision != 0 {
		return false, failure(fault.Conflict)
	}
	digest := peercrypto.Digest(raw)
	var old, oldGrant, oldLane string
	err := tx.QueryRowContext(ctx, `SELECT digest,grant_id,lane FROM outbox WHERE id=?`, id).Scan(&old, &oldGrant, &oldLane)
	if err == nil {
		if old != digest || oldGrant != grantID || oldLane != lane {
			return false, failure(fault.Conflict)
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, failure(StorageUnavailable)
	}
	if err = queueCapacity(ctx, tx, grantID, lane, len(raw), false); err != nil {
		return false, err
	}
	sealed, err := s.seal("outbox", id, raw)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO outbox(id,grant_id,digest,packet,lane,stage,expires_at,bytes,created_at,channel) VALUES(?,?,?,?,?,'sender_stored',?,?,?,(SELECT subject||':'||issuer FROM grants WHERE id=?))`, id, grantID, digest, sealed, lane, expiry, len(raw), now, grantID)
	return false, databaseError(err)
}

func queueCapacity(ctx context.Context, tx *sql.Tx, grantID, lane string, size int, inbox bool) error {
	var count, used int
	query := `SELECT COUNT(*),COALESCE(SUM(bytes),0) FROM outbox WHERE grant_id=? AND lane=? AND stage='sender_stored' AND priority=0`
	args := []any{grantID, lane}
	if inbox {
		query = `SELECT COUNT(*),COALESCE(SUM(bytes),0) FROM inbox WHERE grant_id=? AND state IN ('pending','offered','parked','intent','authorized','in_doubt') AND lane=?`
		args = []any{grantID, lane}
	}
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count, &used); err != nil {
		return failure(StorageUnavailable)
	}
	maxCount, maxBytes := MaxQueuedMessages, MaxQueuedBytes
	if lane == "control" {
		maxCount, maxBytes = MaxControlMessages, MaxControlBytes
	}
	if count >= maxCount || size > maxBytes-used {
		return failure(Backpressure)
	}
	return retentionCapacity(ctx, tx, grantID, lane, size, inbox)
}

func retentionCapacity(ctx context.Context, tx *sql.Tx, grantID, lane string, size int, inbox bool) error {
	// Retention consumes disk even after a queue drains. Refuse growth explicitly
	// rather than evicting replay evidence or silently dropping another peer's data.
	var count, used int
	retainedQuery := `SELECT COUNT(*),COALESCE(SUM(bytes),0) FROM outbox WHERE grant_id=? AND lane=? AND priority=0`
	retainedArgs := []any{grantID, lane}
	if inbox {
		retainedQuery = `SELECT COUNT(*),COALESCE(SUM(bytes),0) FROM inbox WHERE grant_id=? AND lane=?`
		retainedArgs = []any{grantID, lane}
	}
	if err := tx.QueryRowContext(ctx, retainedQuery, retainedArgs...).Scan(&count, &used); err != nil {
		return failure(StorageUnavailable)
	}
	maxCount, maxBytes := MaxRetainedMessages, MaxRetainedBytes
	if lane == "control" {
		maxCount, maxBytes = MaxControlMessages*64, MaxControlBytes*8
	}
	if count >= maxCount || size > maxBytes-used {
		return failure(Backpressure)
	}
	return nil
}

// OutboxPacket returns only the original wire bytes; retries never reseal HPKE.
func (s *Store) OutboxPacket(ctx context.Context, id string) ([]byte, error) {
	var sealed []byte
	if err := s.db.QueryRowContext(ctx, `SELECT packet FROM outbox WHERE id=? AND stage='sender_stored'`, id).Scan(&sealed); err != nil {
		return nil, lookupError(err)
	}
	return s.unseal("outbox", id, sealed)
}
