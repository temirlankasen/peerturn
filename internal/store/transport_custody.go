package store

import (
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// CommitTransport records transport custody only after trusted ingress has
// authenticated and durably retained bootstrap content in its local journal.
// It cannot authorize ordinary messages, sender sequences or model dispatch.
func (s *Store) CommitTransport(ctx context.Context, mailbox string, seq uint64, packet []byte, now int64) (Custody, error) {
	if !validTransportSlot(mailbox, seq, now) {
		return Custody{}, failure(fault.InvalidEncoding)
	}
	p, err := protocol.DecodePacket(packet)
	if err != nil {
		return Custody{}, err
	}
	if p.Header.DstMailbox != mailbox {
		return Custody{}, failure(fault.AudienceMismatch)
	}
	raw, err := codec.Canonical(p)
	if err != nil {
		return Custody{}, err
	}
	return s.commitTransportSlot(ctx, mailbox, seq, p.Header.MessageID, peercrypto.Digest(raw), "custody_committed", p.Header.ExpiresAt, now)
}

// CommitTombstone records an authenticated relay expiry slot without inventing
// sender metadata. Unknown preceding slots still prevent frontier advancement.
func (s *Store) CommitTombstone(ctx context.Context, mailbox string, seq uint64, now int64) (Custody, error) {
	if !validTransportSlot(mailbox, seq, now) {
		return Custody{}, failure(fault.InvalidEncoding)
	}
	digest := peercrypto.Digest(nil)
	return s.commitTransportSlot(ctx, mailbox, seq, digest[:32], digest, "expired", now, now)
}

func validTransportSlot(mailbox string, seq uint64, now int64) bool {
	return protocol.ValidToken(mailbox) && seq > 0 && seq <= protocol.MaxSafeInteger && now > 0 && now <= protocol.MaxSafeInteger
}

func (s *Store) commitTransportSlot(ctx context.Context, mailbox string, seq uint64, id, digest, stage string, expiry, now int64) (Custody, error) {
	result := Custody{MessageID: id, Stage: stage}
	err := s.write(ctx, func(tx *sql.Tx) error {
		r := InboxRequest{Mailbox: mailbox, MailboxSeq: seq, decision: stage}
		r.Open.Now = now
		var err error
		result.Frontier, err = s.commitMailbox(ctx, tx, r, id, digest, expiry)
		return err
	})
	if err != nil {
		return Custody{}, err
	}
	return result, nil
}

// OutboxExists checks every retained stage, including already relayed controls.
// It lets a trusted journal retry avoid allocating a fresh sender sequence.
func (s *Store) OutboxExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outbox WHERE id=?)`, id).Scan(&exists); err != nil {
		return false, failure(StorageUnavailable)
	}
	return exists, nil
}

// MarkRelayed records a verified relay storage response, never receiver custody.
// A late response cannot overwrite a stronger or terminal stored receipt.
func (s *Store) MarkRelayed(ctx context.Context, id string, now int64) error {
	if !protocol.ValidID(id) || now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var grantID, lane, stage string
		err := tx.QueryRowContext(ctx, `SELECT grant_id,lane,stage FROM outbox WHERE id=?`, id).Scan(&grantID, &lane, &stage)
		if err != nil {
			return lookupError(err)
		}
		if stage != "sender_stored" {
			return nil
		}
		if lane == "data" {
			return s.recordReceipt(ctx, tx, OutboundReceipt, grantID, protocol.ReceiptPayload{MessageID: id, AttemptID: id, Stage: "relay_stored", EvidenceKind: "relay_commit", At: now})
		}
		_, err = tx.ExecContext(ctx, `UPDATE outbox SET stage='relay_stored' WHERE id=? AND stage='sender_stored'`, id)
		return databaseError(err)
	})
}

// RevokePending lists the exact data messages for a trusted caller to prebuild
// side-effect-free revocation notifications before GrantRevoke's transaction.
func (s *Store) RevokePending(ctx context.Context, grantID string) ([]string, error) {
	if !protocol.ValidToken(grantID) {
		return nil, failure(fault.InvalidEncoding)
	}
	var ids []string
	err := s.write(ctx, func(tx *sql.Tx) error {
		var exists bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM grants WHERE id=?)`, grantID).Scan(&exists)
		if err != nil {
			return failure(StorageUnavailable)
		}
		if !exists {
			return failure(NotFound)
		}
		messages, err := revokeMessages(ctx, tx, grantID)
		if err != nil {
			return err
		}
		for _, message := range messages {
			ids = append(ids, message.id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}
