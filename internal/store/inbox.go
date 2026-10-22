package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/temirlankasen/peerturn/internal/codec"
	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// ReceiptBuilder must sign and E2E-seal a receipt using the trusted sender adapter.
// It runs after admission and before commit, must not publish anything, and must
// not return plaintext. A local signer may journal its immutable reservation for
// crash recovery; failed custody must never publish that reservation.
// The store accepts only a bounded wire packet, and never publishes it itself.
type ReceiptBuilder func(protocol.ReceiptPayload) ([]byte, error)

// InboxRequest carries trusted transport context and M1a pinned key material.
// Replay, conversation, grant and budget snapshots in Open are always replaced.
type InboxRequest struct {
	Packet     []byte
	Mailbox    string
	MailboxSeq uint64
	GrantID    string
	Open       envelope.OpenOptions
	Receipt    ReceiptBuilder
	decision   string
	metered    bool
}

// InboxItem is local decrypted data with the exact M1a routing decision.
type InboxItem struct {
	MessageID string
	Body      []byte
	Routing   envelope.Routing
	State     string
}

// InboxCommit serializes validation and durable custody. ACK is exposed only after
// ciphertext, sealed body, dedup, decision, budgets and receipt outbox all commit.
func (s *Store) InboxCommit(ctx context.Context, r InboxRequest) (Custody, error) {
	return s.inboxCommit(ctx, r, false)
}

func (s *Store) inboxCommit(ctx context.Context, r InboxRequest, recheck bool) (Custody, error) {
	if !protocol.ValidToken(r.Mailbox) || r.MailboxSeq == 0 || r.MailboxSeq > protocol.MaxSafeInteger {
		return Custody{}, failure(fault.InvalidEncoding)
	}
	p, err := protocol.DecodePacket(r.Packet)
	if err != nil {
		return Custody{}, err
	}
	if p.Header.DstMailbox != r.Mailbox {
		return Custody{}, failure(fault.AudienceMismatch)
	}
	raw, err := codec.Canonical(p)
	if err != nil {
		return Custody{}, err
	}
	digest := peercrypto.Digest(raw)
	result := Custody{MessageID: p.Header.MessageID, GrantID: r.GrantID, Stage: "receiver_stored"}
	err = s.write(ctx, func(tx *sql.Tx) error {
		var collision int
		if collisionErr := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE id=?`, p.Header.MessageID).Scan(&collision); collisionErr != nil {
			return failure(StorageUnavailable)
		}
		if collision != 0 {
			return failure(fault.Conflict)
		}
		if handled, retryErr := s.rejectedRetry(ctx, tx, r, digest, &result); handled || retryErr != nil {
			return retryErr
		}
		var oldDigest, oldGrant, state string
		err = tx.QueryRowContext(ctx, `SELECT digest,grant_id,state FROM inbox WHERE id=?`, p.Header.MessageID).Scan(&oldDigest, &oldGrant, &state)
		if err == nil {
			if oldDigest != digest || oldGrant != r.GrantID {
				return failure(fault.Conflict)
			}
			if state != "parked" || !recheck {
				result.Duplicate = true
				if state == "parked" {
					result.Stage = "sequence_gap"
				}
				if state == "expired" {
					result.Stage = "expired"
				}
				result.Frontier, err = s.commitMailbox(ctx, tx, r, p.Header.MessageID, digest, p.Header.ExpiresAt)
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return failure(StorageUnavailable)
		}
		if errors.Is(err, sql.ErrNoRows) && !recheck {
			if handled, retryErr := s.compactedCustody(ctx, tx, r, &result); handled || retryErr != nil {
				return retryErr
			}
		}
		verificationTime := r.Open.Now
		if p.Header.ExpiresAt <= verificationTime {
			verificationTime = p.Header.ExpiresAt - 1
		}
		g, authErr := s.authorization(ctx, tx, r.GrantID, verificationTime)
		err = authErr
		if err != nil {
			return err
		}
		o, snapshotErr := s.openSnapshot(ctx, tx, r, g)
		if snapshotErr != nil {
			return snapshotErr
		}
		budget := o.Budget
		o.Budget = envelope.BudgetSnapshot{MinuteStart: o.Now, TurnWindowStart: o.Now}
		a, openErr := envelope.OpenForCustody(raw, o)
		gap := fault.IsCode(openErr, fault.SequenceGap)
		expired := a.Expired != nil && fault.IsCode(openErr, fault.Expired)
		if a.Rejected != nil {
			return s.refuseAdmission(ctx, tx, r, g, a.Rejected, o.Replay.NextSeq, openErr, &result)
		}
		if openErr != nil && !gap && !expired {
			return openErr
		}
		control := a.Routing == envelope.Control || a.Gap != nil && a.Gap.Kind == protocol.Receipt || a.Expired != nil && a.Expired.Kind == protocol.Receipt
		if (control || expired) && state != "parked" {
			r.metered = true
			if err = chargeControl(ctx, tx, g, o.Now); err != nil {
				if !fault.IsCode(err, fault.BudgetExceeded) {
					return err
				}
				return s.refuseAdmission(ctx, tx, r, g, admissionMetadata(a, g), o.Replay.NextSeq, err, &result)
			}
		}
		if !control && !expired {
			o.Budget = budget
			a, openErr = envelope.OpenForCustody(raw, o)
			gap = fault.IsCode(openErr, fault.SequenceGap)
			if a.Rejected != nil {
				return s.refuseAdmission(ctx, tx, r, g, a.Rejected, o.Replay.NextSeq, openErr, &result)
			}
			if openErr != nil && !gap {
				return openErr
			}
		}
		if g.GrantID != r.GrantID {
			return failure(fault.InvalidGrant)
		}
		if _, err = tx.ExecContext(ctx, `SAVEPOINT admission`); err != nil {
			return failure(StorageUnavailable)
		}
		if err = s.persistAdmission(ctx, tx, r, g, a, raw, state == "parked"); err != nil {
			return s.admissionFailure(ctx, tx, r, g, a, o.Replay.NextSeq, err, &result)
		}
		if gap {
			result.Stage = "sequence_gap"
		}
		if expired {
			result.Stage = "expired"
		}
		if err = s.step("inbox_decision"); err != nil {
			return err
		}
		if state != "parked" && !control && !expired {
			receipt := protocol.ReceiptPayload{MessageID: p.Header.MessageID, AttemptID: p.Header.MessageID, Stage: "receiver_stored", EvidenceKind: "local_commit", At: o.Now}
			if gap {
				receipt.Outcome = "sequence_gap"
			}
			if err = s.receiptOutbox(ctx, tx, r, receipt); err != nil {
				return s.admissionFailure(ctx, tx, r, g, a, o.Replay.NextSeq, err, &result)
			}
		}
		if expired && !control {
			if err = s.recordReceipt(ctx, tx, InboundReceipt, r.GrantID, protocol.ReceiptPayload{MessageID: p.Header.MessageID, AttemptID: p.Header.MessageID, Stage: "expired", EvidenceKind: "local_expiry", At: o.Now}); err != nil {
				return err
			}
		}
		if control && !gap && !expired {
			receipt, decodeErr := protocol.DecodeReceipt([]byte(a.Message.BodyUTF8))
			if decodeErr != nil {
				return decodeErr
			}
			if err = s.acceptPeerReceipt(ctx, tx, r.GrantID, receipt); err != nil {
				return s.admissionFailure(ctx, tx, r, g, a, o.Replay.NextSeq, err, &result)
			}
		}
		if err = s.step("inbox_receipt"); err != nil {
			return err
		}
		result.Frontier, err = s.commitMailbox(ctx, tx, r, p.Header.MessageID, digest, p.Header.ExpiresAt)
		return err
	})
	if err != nil {
		return Custody{}, err
	}
	return result, nil
}

func (s *Store) persistAdmission(ctx context.Context, tx *sql.Tx, r InboxRequest, g grant.Grant, a envelope.Admission, raw []byte, promote bool) error {
	channel := channelKey(g.Subject, g.Issuer)
	record, expiry, route, state := a.Record, a.Message.ExpiresAt, a.Routing, "pending"
	if a.Gap != nil {
		record, expiry, route, state = a.Gap.Record, a.Gap.ExpiresAt, "", "parked"
	}
	if a.Expired != nil {
		record, expiry, route, state = a.Expired.Record, a.Expired.ExpiresAt, "", "expired"
	}
	if record.SenderSeq > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	lane := "data"
	if route == envelope.Control || a.Gap != nil && a.Gap.Kind == protocol.Receipt || a.Expired != nil && a.Expired.Kind == protocol.Receipt {
		lane = "control"
		if a.Gap == nil && a.Expired == nil {
			state = "handled"
		}
	}
	if !promote {
		if a.Expired != nil {
			if err := retentionCapacity(ctx, tx, g.GrantID, lane, 0, true); err != nil {
				return err
			}
		} else {
			if err := queueCapacity(ctx, tx, g.GrantID, lane, len(raw), true); err != nil {
				return err
			}
		}
	}
	body, err := s.seal("inbox", record.MessageID, []byte(a.Message.BodyUTF8))
	if err != nil {
		return err
	}
	retainedPacket := raw
	storedBytes := len(raw)
	if a.Expired != nil {
		retainedPacket = nil
		storedBytes = 0
	}
	packet, err := s.seal("inbox_packet", record.MessageID, retainedPacket)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO inbox(id,channel,grant_id,epoch,sender_seq,digest,packet,body,route,lane,state,expires_at,bytes,created_at,conversation_id,parent_id,budget_id,hop,mailbox,mailbox_seq) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,packet=excluded.packet,bytes=excluded.bytes,route=excluded.route,lane=excluded.lane,state=excluded.state,conversation_id=excluded.conversation_id,parent_id=excluded.parent_id,budget_id=excluded.budget_id,hop=excluded.hop`, record.MessageID, channel, g.GrantID, g.GrantEpoch, record.SenderSeq, record.Digest, packet, body, route, lane, state, expiry, storedBytes, r.Open.Now, a.Message.ConversationID, a.Message.ParentID, a.Message.BudgetID, a.Message.Hop, r.Mailbox, r.MailboxSeq)
	if err != nil {
		return failure(StorageUnavailable)
	}
	if err = s.step("inbox_insert"); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dedup(key,channel,message_id,digest,sender_seq,state,retain_until) VALUES(?,?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET state=excluded.state`, "sender/"+channel+"/"+record.MessageID, channel, record.MessageID, record.Digest, record.SenderSeq, state, retainUntil(expiry, r.Open.Now))
	if err != nil {
		return failure(StorageUnavailable)
	}
	if a.Gap == nil && a.Expired == nil && !a.Duplicate && route != envelope.Control {
		return chargeBudget(ctx, tx, g, r.Open.Now, a.WantsModel)
	}
	return nil
}

func (s *Store) receiptOutbox(ctx context.Context, tx *sql.Tx, r InboxRequest, receipt protocol.ReceiptPayload) error {
	if r.Receipt == nil {
		return failure(InvalidState)
	}
	raw, err := r.Receipt(receipt)
	if err != nil {
		return failure(ReceiptUnavailable)
	}
	p, err := protocol.DecodePacket(raw)
	if err != nil {
		return err
	}
	if p.Header.MessageID == receipt.MessageID || p.Header.ExpiresAt <= r.Open.Now {
		return failure(fault.InvalidEncoding)
	}
	raw, err = codec.Canonical(p)
	if err != nil {
		return err
	}
	if _, err = s.putOutbox(ctx, tx, p.Header.MessageID, r.GrantID, raw, "control", p.Header.ExpiresAt, r.Open.Now); err != nil {
		return err
	}
	return s.recordReceipt(ctx, tx, InboundReceipt, r.GrantID, receipt)
}

// Inbox returns literal local text; it performs no expansion, fetch or dispatch.
func (s *Store) Inbox(ctx context.Context, id string) (InboxItem, error) {
	item := InboxItem{MessageID: id}
	var sealed []byte
	if err := s.db.QueryRowContext(ctx, `SELECT body,route,state FROM inbox WHERE id=?`, id).Scan(&sealed, &item.Routing, &item.State); err != nil {
		return item, lookupError(err)
	}
	var err error
	item.Body, err = s.unseal("inbox", id, sealed)
	return item, err
}
