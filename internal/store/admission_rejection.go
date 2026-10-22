package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/temirlankasen/peerturn/internal/envelope"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/grant"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

func admissionMetadata(a envelope.Admission, g grant.Grant) *envelope.GapMetadata {
	if a.Rejected != nil {
		return a.Rejected
	}
	if a.Gap != nil {
		return a.Gap
	}
	if a.Expired != nil {
		return a.Expired
	}
	return &envelope.GapMetadata{Record: a.Record, ExpiresAt: a.Message.ExpiresAt, Sender: g.Subject, Recipient: g.Issuer, Kind: a.Message.Kind}
}

func (s *Store) admissionFailure(ctx context.Context, tx *sql.Tx, r InboxRequest, g grant.Grant, a envelope.Admission, next uint64, cause error, result *Custody) error {
	var typed *fault.Error
	if !errors.As(cause, &typed) {
		return cause
	}
	if typed.Code != Backpressure && typed.Code != fault.BudgetExceeded && typed.Code != fault.ScopeDenied &&
		typed.Code != fault.InvalidGrant && typed.Code != fault.AudienceMismatch && typed.Code != NotFound {
		return cause
	}
	if _, err := tx.ExecContext(ctx, `ROLLBACK TO admission`); err != nil {
		return failure(StorageUnavailable)
	}
	return s.refuseAdmission(ctx, tx, r, g, admissionMetadata(a, g), next, cause, result)
}

// Refused packets consume only authenticated sequences inside the bounded
// window. A permanent far-future refusal acknowledges transport, never a gap.
func (s *Store) refuseAdmission(ctx context.Context, tx *sql.Tx, r InboxRequest, g grant.Grant, m *envelope.GapMetadata, next uint64, cause error, result *Custody) error {
	var typed *fault.Error
	if m == nil || !errors.As(cause, &typed) {
		return cause
	}
	result.Stage, result.Reason = "rejected", typed.Code
	if !r.metered {
		if err := chargeControl(ctx, tx, g, r.Open.Now); err != nil && !fault.IsCode(err, fault.BudgetExceeded) {
			return err
		}
	}
	channel := channelKey(g.Subject, g.Issuer)
	seq := m.Record.SenderSeq
	if seq == next {
		if _, err := tx.ExecContext(ctx, `INSERT INTO dedup(key,message_id,digest,sender_seq,state,retain_until) VALUES(?,'','',?,'replay_floor',0) ON CONFLICT(key) DO UPDATE SET sender_seq=MAX(sender_seq,excluded.sender_seq)`, "replay_floor/"+channel, seq); err != nil {
			return failure(StorageUnavailable)
		}
	} else if seq > next && seq-next <= envelope.MaxPendingSequences {
		if _, err := tx.ExecContext(ctx, `INSERT INTO dedup(key,channel,message_id,digest,sender_seq,state,retain_until) VALUES(?,?,?,?,?,'refused',?) ON CONFLICT(key) DO UPDATE SET state='refused'`, "sender/"+channel+"/"+m.Record.MessageID, channel, m.Record.MessageID, m.Record.Digest, seq, retainUntil(m.ExpiresAt, r.Open.Now)); err != nil {
			return failure(StorageUnavailable)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dedup SET state='refused' WHERE channel=? AND message_id=? AND state='parked'`, channel, m.Record.MessageID); err != nil {
		return failure(StorageUnavailable)
	}
	if err := compactChannel(ctx, tx, channel, 0); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM dedup WHERE channel=? AND state='refused' AND sender_seq<=(SELECT sender_seq FROM dedup WHERE key=?)`, channel, "replay_floor/"+channel); err != nil {
		return failure(StorageUnavailable)
	}
	// A rejected promotion has already retained ciphertext. Clear it without
	// losing the previous storage receipt or treating it as native work.
	body, err := s.seal("inbox", m.Record.MessageID, nil)
	if err != nil {
		return err
	}
	packet, err := s.seal("inbox_packet", m.Record.MessageID, nil)
	if err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE inbox SET state='rejected',body=?,packet=?,bytes=0,route='' WHERE id=? AND state='parked'`, body, packet, m.Record.MessageID)
	if err != nil {
		return failure(StorageUnavailable)
	}
	n, err := changed.RowsAffected()
	if err != nil {
		return failure(StorageUnavailable)
	}
	if n != 0 {
		if err = s.recordReceipt(ctx, tx, InboundReceipt, r.GrantID, protocol.ReceiptPayload{MessageID: m.Record.MessageID, AttemptID: m.Record.MessageID, Stage: "rejected", EvidenceKind: "local_rejection", ReasonCode: string(typed.Code), At: r.Open.Now}); err != nil {
			return err
		}
	}
	if err = s.rejectionAudit(ctx, tx, "admission_reject", g.GrantID, g.GrantEpoch, m.Record.MessageID, m.Record.Digest, typed.Code); err != nil {
		return err
	}
	r.decision = "rejected/" + string(typed.Code) + "/" + g.GrantID
	result.Frontier, err = s.commitMailbox(ctx, tx, r, m.Record.MessageID, m.Record.Digest, m.ExpiresAt)
	return err
}

// RejectionStatus is bounded attribution: one aggregate per installed grant,
// plus one for unauthenticated transport. It contains no peer body or authority.
type RejectionStatus struct {
	GrantID, MessageID, Digest string
	Reason                     fault.Code
	Count                      uint64
	At                         int64
}

// Rejections exposes the grant responsible for terminal custody refusals.
func (s *Store) Rejections(ctx context.Context) ([]RejectionStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT grant_id,message_id,digest,reason,count,at FROM audit WHERE action IN ('admission_reject','mailbox_reject') ORDER BY grant_id`)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	var results []RejectionStatus
	for rows.Next() {
		var item RejectionStatus
		if err = rows.Scan(&item.GrantID, &item.MessageID, &item.Digest, &item.Reason, &item.Count, &item.At); err != nil {
			break
		}
		results = append(results, item)
	}
	iterErr, closeErr := rows.Err(), rows.Close()
	if err != nil || iterErr != nil || closeErr != nil {
		return nil, failure(StorageUnavailable)
	}
	return results, nil
}

func (s *Store) rejectionAudit(ctx context.Context, tx *sql.Tx, action, grantID string, epoch uint64, id, digest string, reason fault.Code) error {
	// Updating the aggregate prevents a relay from manufacturing an unbounded
	// audit history merely by assigning fresh transport slots to garbage.
	r, err := tx.ExecContext(ctx, `UPDATE audit SET at=?,epoch=?,message_id=?,digest=?,reason=?,count=count+1 WHERE action=? AND grant_id=?`, s.clock().Unix(), epoch, id, digest, reason, action, grantID)
	if err != nil {
		return failure(StorageUnavailable)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return failure(StorageUnavailable)
	}
	if n == 0 {
		return s.audit(ctx, tx, s.clock().Unix(), action, grantID, epoch, id, digest, string(reason))
	}
	return nil
}
