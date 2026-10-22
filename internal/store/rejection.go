package store

import (
	"context"
	"database/sql"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// RejectMailbox is a trusted ingress operation after a terminal validation
// failure. It retains only a bounded transport tombstone, never an unverified
// sender sequence, body, grant or receipt response. This lets a rejected relay
// slot advance custody without turning untrusted metadata into admission.
func (s *Store) RejectMailbox(ctx context.Context, mailbox string, seq uint64, packet []byte, reason fault.Code, now int64) (Custody, error) {
	if !protocol.ValidToken(mailbox) || seq == 0 || seq > protocol.MaxSafeInteger || len(packet) > protocol.MaxPacket || now <= 0 || now > protocol.MaxSafeInteger {
		return Custody{}, failure(fault.InvalidEncoding)
	}
	switch reason {
	case fault.InvalidEncoding, fault.TooLarge, fault.UnsupportedVersion, fault.InvalidSignature,
		fault.DecryptionFailed, fault.InvalidCertificate, fault.NeedsVerify, fault.AudienceMismatch,
		fault.EpochMismatch, fault.InvalidGrant, fault.Expired, fault.NotYetValid, fault.ScopeDenied,
		fault.Revoked, fault.Conflict, fault.Replay, fault.InvalidInvite, fault.AlreadyUsed,
		fault.SequenceWindowExceeded, fault.BudgetExceeded, Backpressure:
	case fault.SequenceGap:
		return Custody{}, failure(fault.InvalidEncoding)
	default:
		return Custody{}, failure(fault.InvalidEncoding)
	}
	digest := peercrypto.Digest(packet)
	result := Custody{MessageID: digest[:32], Stage: "rejected"}
	err := s.write(ctx, func(tx *sql.Tx) error {
		request := InboxRequest{Mailbox: mailbox, MailboxSeq: seq}
		request.Open.Now = now
		var custodyErr error
		result.Frontier, custodyErr = s.commitMailbox(ctx, tx, request, result.MessageID, digest, now)
		if custodyErr != nil {
			return custodyErr
		}
		// The first decision is the audit event; an identical transport retry
		// must not become an unbounded append-only log amplification primitive.
		var exists int
		if custodyErr = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit WHERE action='mailbox_reject' AND message_id=? AND digest=?`, result.MessageID, digest).Scan(&exists); custodyErr != nil {
			return failure(StorageUnavailable)
		}
		result.Duplicate = exists != 0
		if exists != 0 {
			return nil
		}
		return s.rejectionAudit(ctx, tx, "mailbox_reject", "", 0, result.MessageID, digest, reason)
	})
	if err != nil {
		return Custody{}, err
	}
	return result, nil
}
