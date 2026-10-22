package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/invite"
)

// CreateInvite records only the public slot; preview never consumes it.
func (s *Store) CreateInvite(ctx context.Context, id string, expiresAt int64) error {
	if _, err := invite.NewState(id, expiresAt); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var old int64
		err := tx.QueryRowContext(ctx, `SELECT expires_at FROM invites WHERE id=?`, id).Scan(&old)
		if err == nil {
			if old != expiresAt {
				return failure(fault.Conflict)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return failure(StorageUnavailable)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO invites(id,expires_at) VALUES(?,?)`, id, expiresAt)
		if err != nil {
			return failure(StorageUnavailable)
		}
		return s.audit(ctx, tx, 0, "invite_create", "", 0, id, "", "")
	})
}

// ConsumeInvite is the inviter-local authority after M1a OpenClaim verification.
// It serializes the M1a single-use decision, retaining the original encrypted result
// even after expiry/revoke so a lost response cannot cause a second grant.
func (s *Store) ConsumeInvite(ctx context.Context, id, claimID, digest string, result []byte, now int64) ([]byte, error) {
	var output []byte
	err := s.write(ctx, func(tx *sql.Tx) error {
		var expiry int64
		var revoked bool
		if err := tx.QueryRowContext(ctx, `SELECT expires_at,revoked FROM invites WHERE id=?`, id).Scan(&expiry, &revoked); err != nil {
			return lookupError(err)
		}
		state, err := invite.NewState(id, expiry)
		if err != nil {
			return err
		}
		var oldClaim, oldDigest string
		var sealed []byte
		err = tx.QueryRowContext(ctx, `SELECT claim_id,digest,result FROM invite_claims WHERE invite_id=?`, id).Scan(&oldClaim, &oldDigest, &sealed)
		exists := err == nil
		if exists {
			plain, openErr := s.unseal("invite_claims", id, sealed)
			if openErr != nil {
				return openErr
			}
			state, _, err = state.Consume(id, oldClaim, oldDigest, plain, 0)
			if err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return failure(StorageUnavailable)
		}
		if revoked {
			state = state.Revoke()
		}
		_, output, err = state.Consume(id, claimID, digest, result, now)
		if err != nil || exists {
			return err
		}
		sealed, err = s.seal("invite_claims", id, output)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO invite_claims VALUES(?,?,?,?,?)`, id, claimID, digest, sealed, now); err != nil {
			return failure(StorageUnavailable)
		}
		if err = s.step("invite_claim"); err != nil {
			return err
		}
		return s.audit(ctx, tx, now, "invite_consume", "", 0, id, digest, "")
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}

// RevokeInvite closes new joins without changing any previously issued grant.
func (s *Store) RevokeInvite(ctx context.Context, id string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var revoked bool
		if err := tx.QueryRowContext(ctx, `SELECT revoked FROM invites WHERE id=?`, id).Scan(&revoked); err != nil {
			return lookupError(err)
		}
		if revoked {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invites SET revoked=1 WHERE id=?`, id); err != nil {
			return failure(StorageUnavailable)
		}
		return s.audit(ctx, tx, 0, "invite_revoke", "", 0, id, "", "revoked")
	})
}

func lookupError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return failure(NotFound)
	}
	return databaseError(err)
}

func (s *Store) audit(ctx context.Context, tx *sql.Tx, _ int64, action, grantID string, epoch uint64, messageID, digest, reason string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit(at,action,grant_id,epoch,message_id,digest,reason) VALUES(?,?,?,?,?,?,?)`, s.clock().Unix(), action, grantID, epoch, messageID, digest, reason)
	return databaseError(err)
}
