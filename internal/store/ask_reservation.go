package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/identity"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// AskReservation binds one paid child to an immutable parent snapshot and policy.
// Snapshot and ParentContext contain only locally obtained native metadata.
type AskReservation struct {
	RunID, MessageID, AttemptID string
	Parent                      identity.Binding
	ParentContext, Snapshot     []byte
	Model, Effort, ProfileHash  string
	BudgetID                    string
	NotificationGrantID         string
	Now                         int64
}

// AskRun permits spawn only to the caller that durably created the reservation.
type AskRun struct {
	RunID, MessageID, AttemptID, State string
	Start                              bool
}

type askRecord struct {
	AskReservation
	state, attemptState, grantID, conversation, chainBudget string
	hop                                                     uint64
	expiry                                                  int64
	epoch, generation, currentGeneration                    uint64
}

func (r AskReservation) validate() error {
	if !protocol.ValidID(r.RunID) || !protocol.ValidID(r.MessageID) || !protocol.ValidID(r.AttemptID) ||
		!protocol.ValidToken(r.BudgetID) || r.Now <= 0 || r.Now > protocol.MaxSafeInteger ||
		len(r.ParentContext) == 0 || len(r.ParentContext) > 4096 || len(r.Snapshot) == 0 || len(r.Snapshot) > 4096 ||
		len(r.Model) == 0 || len(r.Model) > 256 || len(r.Effort) == 0 || len(r.Effort) > 64 ||
		!protocol.ValidToken(r.ProfileHash) {
		return failure(fault.InvalidEncoding)
	}
	return r.admission().validate()
}

func (r AskReservation) admission() AskAdmission {
	return AskAdmission{MessageID: r.MessageID, AttemptID: r.AttemptID, NotificationGrantID: r.NotificationGrantID,
		Parent: r.Parent, ParentContext: r.ParentContext, Now: r.Now}
}

// ReserveAsk follows DeliveryIntent and precedes AuthorizeAsk and spawn.
// Grant message-rate accounting was charged at custody; only the session model
// turn is charged here, once, in the same transaction as the durable run.
func (s *Store) ReserveAsk(ctx context.Context, r AskReservation) (AskRun, error) {
	if err := r.validate(); err != nil {
		return AskRun{}, err
	}
	result := AskRun{RunID: r.RunID, MessageID: r.MessageID, AttemptID: r.AttemptID}
	err := s.write(ctx, func(tx *sql.Tx) error {
		old, err := s.loadAsk(ctx, tx, r.RunID)
		if err == nil {
			left, leftErr := codec.Canonical(old.AskReservation)
			right, rightErr := codec.Canonical(r)
			if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
				return failure(fault.Conflict)
			}
			result.State = old.state
			return nil
		}
		if !fault.IsCode(err, NotFound) {
			return err
		}
		a, b, err := s.checkAskAdmission(ctx, tx, r.admission())
		if err != nil {
			return err
		}
		var existing int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM responder_runs WHERE message_id=? OR attempt_id=?`, r.MessageID, r.AttemptID).Scan(&existing); err != nil {
			return failure(StorageUnavailable)
		}
		if existing != 0 {
			return failure(fault.Conflict)
		}
		raw, err := codec.Canonical(r)
		if err != nil {
			return err
		}
		sealed, err := s.seal("responder_snapshot", r.RunID, raw)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO responder_runs(run_id,message_id,attempt_id,parent_binding,snapshot_handle,model,effort,profile_hash,budget_id,state) VALUES(?,?,?,?,?,?,?,?,?,'reserved')`, r.RunID, r.MessageID, r.AttemptID, bindingKey(r.Parent), sealed, r.Model, r.Effort, r.ProfileHash, r.BudgetID); err != nil {
			return failure(StorageUnavailable)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sessions SET turn_window=?,turns=? WHERE id=?`, b.TurnWindowStart, b.TurnsInTenMinutes+1, bindingKey(r.Parent)); err != nil {
			return failure(StorageUnavailable)
		}
		result.State, result.Start = "reserved", true
		return s.audit(ctx, tx, r.Now, "ask_reserved", a.grantID, a.epoch, r.MessageID, "", r.RunID)
	})
	if err != nil {
		return AskRun{}, err
	}
	return result, nil
}

func (s *Store) loadAsk(ctx context.Context, tx *sql.Tx, runID string) (askRecord, error) {
	var a askRecord
	var sealed []byte
	var message, attempt, parent, model, effort, profile, budget string
	err := tx.QueryRowContext(ctx, `SELECT r.snapshot_handle,r.message_id,r.attempt_id,r.parent_binding,r.model,r.effort,r.profile_hash,r.budget_id,r.state,a.state,i.grant_id,i.conversation_id,i.budget_id,i.hop,i.expires_at,a.grant_epoch,a.lease_generation,g.lease_generation FROM responder_runs r JOIN delivery_attempts a ON a.id=r.attempt_id JOIN inbox i ON i.id=r.message_id JOIN grants g ON g.id=i.grant_id WHERE r.run_id=?`, runID).Scan(&sealed, &message, &attempt, &parent, &model, &effort, &profile, &budget, &a.state, &a.attemptState, &a.grantID, &a.conversation, &a.chainBudget, &a.hop, &a.expiry, &a.epoch, &a.generation, &a.currentGeneration)
	if err != nil {
		return a, lookupError(err)
	}
	raw, err := s.unseal("responder_snapshot", runID, sealed)
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal(raw, &a.AskReservation); err != nil {
		return a, failure(InvalidState)
	}
	if a.RunID != runID || a.MessageID != message || a.AttemptID != attempt || bindingKey(a.Parent) != parent ||
		a.Model != model || a.Effort != effort || a.ProfileHash != profile || a.BudgetID != budget {
		return a, failure(InvalidState)
	}
	return a, nil
}

func (s *Store) authorizeAsk(ctx context.Context, tx *sql.Tx, a askRecord, now int64) (grant.Grant, error) {
	if now >= a.expiry {
		return grant.Grant{}, failure(fault.Expired)
	}
	if a.generation != a.currentGeneration {
		return grant.Grant{}, failure(fault.Revoked)
	}
	g, err := s.authorization(ctx, tx, a.grantID, now)
	if err != nil {
		return g, err
	}
	if g.GrantEpoch != a.epoch || !identity.EqualBinding(g.Issuer, a.Parent) {
		return g, failure(fault.Revoked)
	}
	if err = grant.CheckScope(g.Scope, "ask"); err != nil {
		return g, err
	}
	var state string
	var sealed []byte
	if err = tx.QueryRowContext(ctx, `SELECT state,context FROM bindings WHERE session_id=?`, bindingKey(a.Parent)).Scan(&state, &sealed); err != nil {
		return g, lookupError(err)
	}
	if state != "bound" {
		return g, failure(InvalidState)
	}
	current, err := s.unseal("bindings", bindingKey(a.Parent), sealed)
	if err != nil {
		return g, err
	}
	if !bytes.Equal(current, a.ParentContext) {
		return g, failure(fault.Conflict)
	}
	return g, nil
}
