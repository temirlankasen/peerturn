package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// SelfServicePolicy bounds admission per routing key, not per person. Callers
// must opt in separately; zero values must not enable admission or disable caps.
type SelfServicePolicy struct {
	MaxActiveInvites   int
	MaxInvitesPer24h   int
	MaxInvitesLifetime int
	MaxInviters        int
}

// CreateOwnInvite atomically registers a canonical routing key, its derived
// mailbox and an invitation. The caller must authenticate key possession.
func (s *Store) CreateOwnInvite(ctx context.Context, routingKey string, invite Invite, policy SelfServicePolicy) (Mailbox, error) {
	mailbox, err := routingMailbox(routingKey)
	if err != nil {
		return Mailbox{}, err
	}
	if policy.MaxActiveInvites < 1 || policy.MaxInvitesPer24h < 1 || policy.MaxInvitesLifetime < 1 || policy.MaxInviters < 1 ||
		!validID(invite.ID) || !validID(invite.ProofKey) || !validPacket(invite.Packet) || invite.ExpiresAt.IsZero() || len(invite.Voucher) > 256 {
		return Mailbox{}, invalid()
	}
	var voucherHash []byte
	if invite.Voucher != "" {
		voucherHash = digest([]byte(invite.Voucher))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	if now.UnixNano() <= 0 || !time.Unix(0, now.UnixNano()).Equal(now) {
		return Mailbox{}, unavailable()
	}
	err = s.transactLocked(ctx, now, func(tx *sql.Tx) error {
		var blocked bool
		stateErr := tx.QueryRowContext(ctx, `SELECT revoked FROM relay_inviters WHERE routing_key=?`, routingKey).Scan(&blocked)
		registered := stateErr == nil
		if stateErr != nil && !errors.Is(stateErr, sql.ErrNoRows) {
			return unavailable()
		}
		if blocked {
			return forbidden()
		}
		if found, retryErr := retryOwnInvite(ctx, tx, mailbox, invite, voucherHash, now); found || retryErr != nil {
			return retryErr
		}
		if !invite.ExpiresAt.After(now) || invite.ExpiresAt.After(now.Add(600*time.Second)) || invite.ExpiresAt.UnixNano() <= 0 {
			return invalid()
		}
		createMailbox, mailboxErr := ownMailboxAdmission(ctx, tx, mailbox, registered)
		if mailboxErr != nil {
			return mailboxErr
		}
		if quotaErr := ownInviteQuota(ctx, tx, routingKey, registered, policy, now); quotaErr != nil {
			return quotaErr
		}
		if !registered {
			if roomErr := s.room(ctx, tx, 0); roomErr != nil {
				return roomErr
			}
			if _, insertErr := tx.ExecContext(ctx, `INSERT INTO relay_inviters(routing_key) VALUES(?)`, routingKey); insertErr != nil {
				return unavailable()
			}
		}
		if createMailbox {
			if roomErr := s.room(ctx, tx, 0); roomErr != nil {
				return roomErr
			}
			if roomErr := s.tenantRoom(ctx, tx, routingKey, 0); roomErr != nil {
				return roomErr
			}
			if _, insertErr := tx.ExecContext(ctx, `INSERT INTO mailboxes(id,read_key) VALUES(?,?)`, mailbox.ID, routingKey); insertErr != nil {
				return unavailable()
			}
		}
		if roomErr := s.room(ctx, tx, len(invite.Packet)); roomErr != nil {
			return roomErr
		}
		_, insertErr := tx.ExecContext(ctx, `INSERT INTO invites(id,proof_key,packet,digest,expires_ns,voucher_hash,inviter_key,created_ns)
		 VALUES(?,?,?,?,?,?,?,?)`, invite.ID, invite.ProofKey, invite.Packet, digest(invite.Packet), invite.ExpiresAt.UnixNano(), voucherHash, routingKey, now.UnixNano())
		if insertErr != nil {
			return unavailable()
		}
		return nil
	})
	if err != nil {
		return Mailbox{}, err
	}
	return mailbox, nil
}

func routingMailbox(key string) (Mailbox, error) {
	if len(key) != 43 {
		return Mailbox{}, invalid()
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(key)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != key {
		return Mailbox{}, invalid()
	}
	return Mailbox{ID: hex.EncodeToString(digest(raw)), ReadKey: key}, nil
}

func retryOwnInvite(ctx context.Context, tx *sql.Tx, mailbox Mailbox, invite Invite, voucherHash []byte, now time.Time) (bool, error) {
	var owner sql.NullString
	var proof string
	var packetDigest, storedVoucher []byte
	var expires int64
	var revoked bool
	err := tx.QueryRowContext(ctx, `SELECT inviter_key,proof_key,digest,expires_ns,voucher_hash,revoked FROM invites WHERE id=?`, invite.ID).
		Scan(&owner, &proof, &packetDigest, &expires, &storedVoucher, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, unavailable()
	}
	if !owner.Valid || owner.String != mailbox.ReadKey {
		return true, conflict()
	}
	if revoked || expires <= now.UnixNano() {
		return true, gone()
	}
	if proof != invite.ProofKey || expires != invite.ExpiresAt.UnixNano() ||
		!bytes.Equal(packetDigest, digest(invite.Packet)) || !bytes.Equal(storedVoucher, voucherHash) {
		return true, conflict()
	}
	return true, reader(ctx, tx, mailbox.ID, mailbox.ReadKey)
}

func ownMailboxAdmission(ctx context.Context, tx *sql.Tx, mailbox Mailbox, registered bool) (bool, error) {
	var readKey string
	err := tx.QueryRowContext(ctx, `SELECT read_key FROM mailboxes WHERE id=?`, mailbox.ID).Scan(&readKey)
	if errors.Is(err, sql.ErrNoRows) {
		if registered {
			return false, forbidden()
		}
		return true, nil
	}
	if err != nil {
		return false, unavailable()
	}
	if readKey != mailbox.ReadKey {
		return false, conflict()
	}
	return false, nil
}

func ownInviteQuota(ctx context.Context, tx *sql.Tx, key string, registered bool, policy SelfServicePolicy, now time.Time) error {
	if !registered {
		var creators int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM relay_inviters`).Scan(&creators); err != nil {
			return unavailable()
		}
		if creators >= int64(policy.MaxInviters) {
			return quota()
		}
	}
	var active, daily, lifetime int64
	err := tx.QueryRowContext(ctx, `SELECT
	 COALESCE(SUM(CASE WHEN revoked=0 AND expires_ns>? THEN 1 ELSE 0 END),0),
	 COALESCE(SUM(CASE WHEN created_ns>? THEN 1 ELSE 0 END),0),COUNT(*)
	 FROM invites WHERE inviter_key=?`, now.UnixNano(), now.Add(-24*time.Hour).UnixNano(), key).
		Scan(&active, &daily, &lifetime)
	if err != nil {
		return unavailable()
	}
	if active >= int64(policy.MaxActiveInvites) || daily >= int64(policy.MaxInvitesPer24h) || lifetime >= int64(policy.MaxInvitesLifetime) {
		return quota()
	}
	return nil
}
