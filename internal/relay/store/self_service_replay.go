package store

import (
	"context"
	"database/sql"
	"errors"
)

// PutClosedInvite replays existing self-service custody without admitting an
// inviter or mailbox. Only a verified operator may enable legacy provisioning;
// requiring an own-mailbox receipt must leave allowLegacy false, even for admins.
func (s *Store) PutClosedInvite(ctx context.Context, routingKey string, invite Invite, allowLegacy bool) (Mailbox, error) {
	mailbox, err := routingMailbox(routingKey)
	if err != nil {
		return Mailbox{}, err
	}
	if !validID(invite.ID) || !validID(invite.ProofKey) || !validPacket(invite.Packet) || invite.ExpiresAt.IsZero() || len(invite.Voucher) > 256 {
		return Mailbox{}, invalid()
	}
	var voucherHash []byte
	if invite.Voucher != "" {
		voucherHash = digest([]byte(invite.Voucher))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	var receipt Mailbox
	err = s.transactLocked(ctx, now, func(tx *sql.Tx) error {
		var owner sql.NullString
		lookupErr := tx.QueryRowContext(ctx, `SELECT inviter_key FROM invites WHERE id=?`, invite.ID).Scan(&owner)
		if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
			return unavailable()
		}
		if owner.Valid {
			// Admin provisioning cannot bypass retained self-service authorship,
			// block, expiry, revoke, immutable descriptor or mailbox deletion.
			if owner.String != routingKey {
				return forbidden()
			}
			var blocked bool
			if stateErr := tx.QueryRowContext(ctx, `SELECT revoked FROM relay_inviters WHERE routing_key=?`, routingKey).Scan(&blocked); stateErr != nil {
				return unavailable()
			}
			if blocked {
				return forbidden()
			}
			found, retryErr := retryOwnInvite(ctx, tx, mailbox, invite, voucherHash, now)
			if retryErr != nil {
				return retryErr
			}
			if !found {
				return forbidden()
			}
			receipt = mailbox
			return nil
		}
		if !allowLegacy {
			return forbidden()
		}
		if !invite.ExpiresAt.After(now) {
			return invalid()
		}
		return s.putInvite(ctx, tx, invite, voucherHash)
	})
	if err != nil {
		return Mailbox{}, err
	}
	return receipt, nil
}
