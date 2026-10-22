package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// Attempt reserves at most one native attempt for the transaction winner. A
// not_sent attempt can be replaced only with a fresh identifier after recovery.
type Attempt struct {
	ID              string
	MessageID       string
	State           string
	Start           bool
	Epoch           uint64
	LeaseGeneration uint64
}

// DeliveryIntent rechecks local authority, TTL and explicit parent binding before
// recording the fence that must precede a native call. It does not make that call.
func (s *Store) DeliveryIntent(ctx context.Context, id, attemptID string, now int64) (Attempt, error) {
	if !protocol.ValidID(id) || !protocol.ValidID(attemptID) || now <= 0 || now > protocol.MaxSafeInteger {
		return Attempt{}, failure(fault.InvalidEncoding)
	}
	result := Attempt{ID: attemptID, MessageID: id, State: "intent"}
	err := s.write(ctx, func(tx *sql.Tx) error {
		var revoked bool
		var currentEpoch, storedEpoch, generation uint64
		if err := tx.QueryRowContext(ctx, `SELECT g.revoked,g.epoch,i.epoch,g.lease_generation FROM inbox i JOIN grants g ON g.id=i.grant_id WHERE i.id=?`, id).Scan(&revoked, &currentEpoch, &storedEpoch, &generation); err != nil {
			return lookupError(err)
		}
		if revoked || currentEpoch != storedEpoch {
			return failure(fault.Revoked)
		}
		var oldID string
		recycle := false
		err := tx.QueryRowContext(ctx, `SELECT id,state,grant_epoch,lease_generation FROM delivery_attempts WHERE message_id=?`, id).Scan(&oldID, &result.State, &result.Epoch, &result.LeaseGeneration)
		if err == nil {
			if result.Epoch != currentEpoch || result.LeaseGeneration != generation {
				return failure(fault.Revoked)
			}
			switch {
			case oldID == attemptID:
				return nil
			case result.State == "not_sent":
				recycle = true
			default:
				return failure(fault.Conflict)
			}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return failure(StorageUnavailable)
		}
		var grantID, state string
		var route envelope.Routing
		var expiry int64
		if err = tx.QueryRowContext(ctx, `SELECT grant_id,epoch,state,route,expires_at FROM inbox WHERE id=?`, id).Scan(&grantID, &result.Epoch, &state, &route, &expiry); err != nil {
			return lookupError(err)
		}
		if state != "pending" {
			return failure(InvalidState)
		}
		if now >= expiry {
			return failure(fault.Expired)
		}
		if route != envelope.Live && route != envelope.Responder {
			return failure(fault.ScopeDenied)
		}
		g, err := s.authorization(ctx, tx, grantID, now)
		if err != nil {
			return err
		}
		if err = grantEpoch(g.GrantEpoch, result.Epoch); err != nil {
			return err
		}
		var bindingState string
		if err = tx.QueryRowContext(ctx, `SELECT state FROM bindings WHERE session_id=?`, bindingKey(g.Issuer)).Scan(&bindingState); err != nil {
			return lookupError(err)
		}
		if bindingState != "bound" {
			return failure(InvalidState)
		}
		if err = tx.QueryRowContext(ctx, `SELECT lease_generation FROM grants WHERE id=?`, grantID).Scan(&result.LeaseGeneration); err != nil {
			return lookupError(err)
		}
		var used bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit WHERE action='delivery_intent' AND reason=?)`, attemptID).Scan(&used); err != nil {
			return failure(StorageUnavailable)
		}
		if used {
			return failure(fault.Conflict)
		}
		if recycle {
			if err = s.audit(ctx, tx, now, "delivery_attempt_retired", grantID, result.Epoch, id, "", oldID+":not_sent"); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE delivery_attempts SET id=?,grant_epoch=?,lease_generation=?,state='intent',native_result=NULL,created_at=? WHERE message_id=? AND state='not_sent'`, attemptID, result.Epoch, result.LeaseGeneration, now, id)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO delivery_attempts(id,message_id,grant_epoch,lease_generation,state,created_at) VALUES(?,?,?,?,'intent',?)`, attemptID, id, result.Epoch, result.LeaseGeneration, now)
		}
		if err != nil {
			return failure(StorageUnavailable)
		}
		if err = s.step("delivery_intent"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE inbox SET state='intent' WHERE id=?`, id)
		if err != nil {
			return failure(StorageUnavailable)
		}
		result.Start = true
		result.State = "intent"
		return s.audit(ctx, tx, now, "delivery_intent", grantID, result.Epoch, id, "", attemptID)
	})
	if err != nil {
		return Attempt{}, err
	}
	return result, nil
}

func grantEpoch(current, present uint64) error {
	if current != present {
		return failure(fault.Revoked)
	}
	return nil
}

// DeliveryResult saves native evidence separately from the preceding intent.
// Unknown outcomes become in_doubt; exit status alone must never be model_input.
func (s *Store) DeliveryResult(ctx context.Context, attemptID string, r protocol.ReceiptPayload, nativeResult []byte, notification ...NativeNotification) error {
	if r.AttemptID != attemptID || len(nativeResult) > 4096 || len(notification) > 1 {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var id, state, grantID string
		var previous []byte
		var epoch, generation, currentGeneration uint64
		err := tx.QueryRowContext(ctx, `SELECT a.message_id,a.state,a.grant_epoch,a.lease_generation,i.grant_id,a.native_result FROM delivery_attempts a JOIN inbox i ON i.id=a.message_id WHERE a.id=?`, attemptID).Scan(&id, &state, &epoch, &generation, &grantID, &previous)
		if err != nil {
			return lookupError(err)
		}
		if id != r.MessageID {
			return failure(fault.Conflict)
		}
		g, err := s.authorization(ctx, tx, grantID, r.At)
		if err != nil {
			return err
		}
		if err = grantEpoch(g.GrantEpoch, epoch); err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, `SELECT lease_generation FROM grants WHERE id=?`, grantID).Scan(&currentGeneration); err != nil {
			return lookupError(err)
		}
		if generation != currentGeneration {
			return failure(fault.Revoked)
		}
		canSettle := state == "authorized" || (state == "intent" && r.Stage == "rejected")
		if !canSettle {
			if state != r.Stage || len(previous) == 0 {
				return failure(fault.Conflict)
			}
			plain, openErr := s.unseal("delivery_attempts", attemptID, previous)
			if openErr != nil {
				return openErr
			}
			if !bytes.Equal(plain, nativeResult) {
				return failure(fault.Conflict)
			}
			return nil
		}
		if r.Stage != "adapter_accepted" && r.Stage != "responder_started" && r.Stage != "in_doubt" && r.Stage != "rejected" {
			return failure(InvalidState)
		}
		sealed, err := s.seal("delivery_attempts", attemptID, nativeResult)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE delivery_attempts SET state=?,native_result=? WHERE id=?`, r.Stage, sealed, attemptID)
		if err != nil {
			return failure(StorageUnavailable)
		}
		if err = s.step("delivery_result"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE inbox SET state=? WHERE id=?`, r.Stage, id)
		if err != nil {
			return failure(StorageUnavailable)
		}
		if err = s.recordReceipt(ctx, tx, InboundReceipt, grantID, r); err != nil {
			return err
		}
		if len(notification) == 1 {
			if err = s.nativeNotification(ctx, tx, grantID, r, notification[0]); err != nil {
				return err
			}
		}
		var digest string
		if err = tx.QueryRowContext(ctx, `SELECT digest FROM inbox WHERE id=?`, id).Scan(&digest); err != nil {
			return lookupError(err)
		}
		return s.audit(ctx, tx, r.At, "delivery_result", grantID, epoch, id, digest, r.Stage)
	})
}
