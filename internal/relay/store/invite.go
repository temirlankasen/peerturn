package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"
)

// PutInvite registers a ciphertext descriptor; exact retries are idempotent.
func (s *Store) PutInvite(ctx context.Context, invite Invite) error {
	if !validID(invite.ID) || !validID(invite.ProofKey) || !validPacket(invite.Packet) || invite.ExpiresAt.IsZero() || len(invite.Voucher) > 256 {
		return invalid()
	}
	var voucherHash []byte
	if invite.Voucher != "" {
		voucherHash = digest([]byte(invite.Voucher))
	}
	return s.transact(ctx, s.clockNow(), func(tx *sql.Tx) error {
		var key string
		var existingDigest, existingVoucher []byte
		var expires int64
		err := tx.QueryRowContext(ctx, `SELECT proof_key,digest,expires_ns,voucher_hash FROM invites WHERE id=?`, invite.ID).Scan(&key, &existingDigest, &expires, &existingVoucher)
		if err == nil {
			if key == invite.ProofKey && expires == invite.ExpiresAt.UnixNano() && bytes.Equal(existingDigest, digest(invite.Packet)) && bytes.Equal(existingVoucher, voucherHash) {
				return nil
			}
			return conflict()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return unavailable()
		}
		if roomErr := s.room(ctx, tx, len(invite.Packet)); roomErr != nil {
			return roomErr
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO invites(id,proof_key,packet,digest,expires_ns,voucher_hash) VALUES(?,?,?,?,?,?)`, invite.ID, invite.ProofKey, invite.Packet, digest(invite.Packet), invite.ExpiresAt.UnixNano(), voucherHash)
		if err != nil {
			return unavailable()
		}
		return nil
	})
}

// GetInvite never reveals the enrollment voucher, which is hashed at rest.
func (s *Store) GetInvite(ctx context.Context, id string, now time.Time) (Invite, error) {
	var invite Invite
	var expires int64
	var revoked bool
	err := s.db.QueryRowContext(ctx, `SELECT id,proof_key,packet,expires_ns,revoked,claim_packet,COALESCE(claim_reader,'') FROM invites WHERE id=?`, id).Scan(&invite.ID, &invite.ProofKey, &invite.Packet, &expires, &revoked, &invite.ClaimPacket, &invite.ClaimReader)
	if errors.Is(err, sql.ErrNoRows) {
		return Invite{}, gone()
	}
	if err != nil {
		return Invite{}, unavailable()
	}
	invite.ExpiresAt = time.Unix(0, expires).UTC()
	if revoked || !invite.ExpiresAt.After(now) {
		return Invite{}, gone()
	}
	return invite, nil
}

// ClaimInvite atomically reserves the one slot after the caller verifies proof.
func (s *Store) ClaimInvite(ctx context.Context, id string, claim Claim, now time.Time) (Claim, error) {
	if !validID(claim.ID) || !validID(claim.ReaderKey) || !validPacket(claim.Packet) {
		return Claim{}, invalid()
	}
	err := s.transact(ctx, now, func(tx *sql.Tx) error {
		var expires int64
		var revoked bool
		var usedID, usedReader sql.NullString
		var usedDigest []byte
		err := tx.QueryRowContext(ctx, `SELECT expires_ns,revoked,claim_id,claim_reader,claim_digest FROM invites WHERE id=?`, id).Scan(&expires, &revoked, &usedID, &usedReader, &usedDigest)
		if errors.Is(err, sql.ErrNoRows) {
			return gone()
		}
		if err != nil {
			return unavailable()
		}
		if revoked || expires <= now.UnixNano() {
			return gone()
		}
		if usedID.Valid {
			if usedID.String == claim.ID && usedReader.String == claim.ReaderKey {
				if bytes.Equal(usedDigest, digest(claim.Packet)) {
					return nil
				}
				return conflict()
			}
			return gone()
		}
		if roomErr := s.reserve(0, int64(len(claim.Packet))); roomErr != nil {
			return roomErr
		}
		_, err = tx.ExecContext(ctx, `UPDATE invites SET claim_id=?,claim_reader=?,claim_packet=?,claim_digest=? WHERE id=?`, claim.ID, claim.ReaderKey, claim.Packet, digest(claim.Packet), id)
		if err != nil {
			return unavailable()
		}
		return nil
	})
	claim.Voucher = ""
	return claim, err
}

// RevokeInvite is a local provisioning action and retains a durable tombstone.
func (s *Store) RevokeInvite(ctx context.Context, id string) error {
	if !validID(id) {
		return invalid()
	}
	return s.transact(ctx, s.clockNow(), func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM invites WHERE id=?`, id).Scan(&exists); err != nil {
			return unavailable()
		}
		if exists == 0 {
			if err := s.room(ctx, tx, 0); err != nil {
				return err
			}
		} else {
			var size int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(LENGTH(packet),0)+COALESCE(LENGTH(claim_packet),0) FROM invites WHERE id=?`, id).Scan(&size); err != nil {
				return unavailable()
			}
			s.pending.bytes -= size
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO invites(id,proof_key,digest,expires_ns,revoked) VALUES(?,'',?,0,1) ON CONFLICT(id) DO UPDATE SET revoked=1,packet=NULL,claim_packet=NULL`, id, digest(nil))
		if err != nil {
			return unavailable()
		}
		return nil
	})
}
