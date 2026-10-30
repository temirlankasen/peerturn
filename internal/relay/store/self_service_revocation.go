package store

import (
	"context"
	"database/sql"
	"errors"
)

// CheckInviteOwner checks retained authorship, including expired and revoked
// invitations. Authentication belongs to the caller; proof keys confer no ownership.
func (s *Store) CheckInviteOwner(ctx context.Context, inviteID, routingKey string) error {
	if !validID(inviteID) {
		return invalid()
	}
	if _, err := routingMailbox(routingKey); err != nil {
		return err
	}
	return inviteOwner(ctx, s.db, inviteID, routingKey)
}

func inviteOwner(ctx context.Context, tx queryer, id, key string) error {
	var owned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM invites WHERE id=? AND inviter_key=?)`, id, key).Scan(&owned); err != nil {
		return unavailable()
	}
	if !owned {
		return forbidden()
	}
	return nil
}

// CheckInviteRevoker is a bounded admission check, including retained terminal
// rows. The caller supplies verified operator authority, never request data.
// Unlike CheckInviteOwner, an unknown ID is gone for both authors and operators.
func (s *Store) CheckInviteRevoker(ctx context.Context, inviteID, routingKey string, admin bool) error {
	if !validID(inviteID) {
		return invalid()
	}
	if _, err := routingMailbox(routingKey); err != nil {
		return err
	}
	return inviteRevoker(ctx, s.db, inviteID, routingKey, admin)
}

func inviteRevoker(ctx context.Context, tx queryer, id, key string, admin bool) error {
	var owner sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT inviter_key FROM invites WHERE id=?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return gone()
	}
	if err != nil {
		return unavailable()
	}
	if !admin && (!owner.Valid || owner.String != key) {
		return forbidden()
	}
	return nil
}

// RevokeExistingInvite is the public revoke seam. It rechecks authority inside
// the mutation transaction and cannot reserve an unknown ID as RevokeInvite can.
func (s *Store) RevokeExistingInvite(ctx context.Context, inviteID, routingKey string, admin bool) error {
	if !validID(inviteID) {
		return invalid()
	}
	if _, err := routingMailbox(routingKey); err != nil {
		return err
	}
	return s.transact(ctx, s.clockNow(), func(tx *sql.Tx) error {
		if err := inviteRevoker(ctx, tx, inviteID, routingKey, admin); err != nil {
			return err
		}
		var size int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(LENGTH(packet),0)+COALESCE(LENGTH(claim_packet),0)
		 FROM invites WHERE id=?`, inviteID).Scan(&size); err != nil {
			return unavailable()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invites SET revoked=1,packet=NULL,claim_packet=NULL
		 WHERE id=? AND (? OR inviter_key=?)`, inviteID, admin, routingKey); err != nil {
			return unavailable()
		}
		s.pending.bytes -= size
		return nil
	})
}

// RevokeOwnedInvite rechecks authorship in the mutation transaction. History
// and voucher lineage remain; an unknown ID cannot allocate a tombstone.
func (s *Store) RevokeOwnedInvite(ctx context.Context, inviteID, routingKey string) error {
	if !validID(inviteID) {
		return invalid()
	}
	if _, err := routingMailbox(routingKey); err != nil {
		return err
	}
	return s.transact(ctx, s.clockNow(), func(tx *sql.Tx) error {
		if err := inviteOwner(ctx, tx, inviteID, routingKey); err != nil {
			return err
		}
		var size int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(LENGTH(packet),0)+COALESCE(LENGTH(claim_packet),0)
		 FROM invites WHERE id=? AND inviter_key=?`, inviteID, routingKey).Scan(&size); err != nil {
			return unavailable()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invites SET revoked=1,packet=NULL,claim_packet=NULL WHERE id=? AND inviter_key=?`, inviteID, routingKey); err != nil {
			return unavailable()
		}
		s.pending.bytes -= size
		return nil
	})
}

// BlockInviter durably fences publication, claim and voucher enrollment. The
// caller must authorize this administrative action. Unknown keys allocate nothing.
func (s *Store) BlockInviter(ctx context.Context, routingKey string) error {
	if _, err := routingMailbox(routingKey); err != nil {
		return err
	}
	return s.transact(ctx, s.clockNow(), func(tx *sql.Tx) error {
		var blocked bool
		err := tx.QueryRowContext(ctx, `SELECT revoked FROM relay_inviters WHERE routing_key=?`, routingKey).Scan(&blocked)
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "not_found", Status: 404}
		}
		if err != nil {
			return unavailable()
		}
		var size int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(COALESCE(LENGTH(packet),0)+COALESCE(LENGTH(claim_packet),0)),0)
		 FROM invites WHERE inviter_key=?`, routingKey).Scan(&size); err != nil {
			return unavailable()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE relay_inviters SET revoked=1 WHERE routing_key=?`, routingKey); err != nil {
			return unavailable()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invites SET revoked=1,packet=NULL,claim_packet=NULL WHERE inviter_key=?`, routingKey); err != nil {
			return unavailable()
		}
		s.pending.bytes -= size
		return nil
	})
}
