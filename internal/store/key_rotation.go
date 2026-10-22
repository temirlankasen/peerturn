package store

import (
	"bytes"
	"context"
	"database/sql"
)

// Every local AEAD column is listed explicitly: identifiers and SQL are static,
// and receipt AAD includes its complete directional primary key.
var sealedColumns = []struct{ query, update, prefix string }{
	{`SELECT rowid,'',sealed FROM key_check`, `UPDATE key_check SET sealed=? WHERE rowid=?`, keyCheckAAD},
	{`SELECT rowid,session_id,context FROM bindings`, `UPDATE bindings SET context=? WHERE rowid=?`, "peerturn/store/v1/bindings/"},
	{`SELECT rowid,id,signed FROM grants`, `UPDATE grants SET signed=? WHERE rowid=?`, "peerturn/store/v1/grants/"},
	{`SELECT rowid,invite_id,result FROM invite_claims`, `UPDATE invite_claims SET result=? WHERE rowid=?`, "peerturn/store/v1/invite_claims/"},
	{`SELECT rowid,id,packet FROM outbox`, `UPDATE outbox SET packet=? WHERE rowid=?`, "peerturn/store/v1/outbox/"},
	{`SELECT rowid,id,packet FROM inbox`, `UPDATE inbox SET packet=? WHERE rowid=?`, "peerturn/store/v1/inbox_packet/"},
	{`SELECT rowid,id,body FROM inbox`, `UPDATE inbox SET body=? WHERE rowid=?`, "peerturn/store/v1/inbox/"},
	{`SELECT rowid,id,native_result FROM delivery_attempts WHERE native_result IS NOT NULL`, `UPDATE delivery_attempts SET native_result=? WHERE rowid=?`, "peerturn/store/v1/delivery_attempts/"},
	{`SELECT rowid,direction||'/'||grant_id||'/'||channel||'/'||message_id,evidence FROM receipts`, `UPDATE receipts SET evidence=? WHERE rowid=?`, "peerturn/store/v1/receipts/"},
	{`SELECT rowid,run_id,snapshot_handle FROM responder_runs WHERE length(snapshot_handle)>0`, `UPDATE responder_runs SET snapshot_handle=? WHERE rowid=?`, "peerturn/store/v1/responder_snapshot/"},
	{`SELECT rowid,run_id,fork_native_id FROM responder_runs WHERE fork_native_id IS NOT NULL AND length(fork_native_id)>0`, `UPDATE responder_runs SET fork_native_id=? WHERE rowid=?`, "peerturn/store/v1/responder_fork/"},
}

type sealedRecord struct {
	row    int64
	id     string
	sealed []byte
}

// RotateSealer reseals every retained AEAD field atomically, checking each new
// ciphertext before commit. The daemon must hold exclusive state ownership.
func (s *Store) RotateSealer(ctx context.Context, target Sealer) error {
	if target == nil {
		return failure(SealingFailed)
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		for _, column := range sealedColumns {
			records, err := readSealed(ctx, tx, column.query)
			if err != nil {
				return err
			}
			for _, record := range records {
				aad := []byte(column.prefix + record.id)
				sealed, sealErr := reseal(s.sealer, target, aad, record.sealed)
				if sealErr != nil {
					return sealErr
				}
				if _, err = tx.ExecContext(ctx, column.update, sealed, record.row); err != nil {
					return failure(StorageUnavailable)
				}
			}
		}
		return verifySealed(ctx, tx, target)
	})
	if err == nil {
		s.sealer = target
	}
	return err
}

// VerifySealer authenticates all local ciphertext without returning plaintext.
func (s *Store) VerifySealer(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error { return verifySealed(ctx, tx, s.sealer) })
}

func readSealed(ctx context.Context, tx *sql.Tx, query string) (_ []sealedRecord, result error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	defer func() {
		if err = rows.Close(); err != nil {
			result = failure(StorageUnavailable)
		}
	}()
	var records []sealedRecord
	for rows.Next() {
		var record sealedRecord
		if err = rows.Scan(&record.row, &record.id, &record.sealed); err != nil {
			return nil, failure(StorageUnavailable)
		}
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		return nil, failure(StorageUnavailable)
	}
	return records, nil
}

func verifySealed(ctx context.Context, tx *sql.Tx, sealer Sealer) error {
	for _, column := range sealedColumns {
		records, err := readSealed(ctx, tx, column.query)
		if err != nil {
			return err
		}
		for _, record := range records {
			plain, openErr := sealer.Open([]byte(column.prefix+record.id), record.sealed)
			clear(plain)
			if openErr != nil {
				return failure(SealingFailed)
			}
		}
	}
	return nil
}

func reseal(source, target Sealer, aad, ciphertext []byte) ([]byte, error) {
	plain, err := source.Open(aad, ciphertext)
	if err != nil {
		return nil, failure(SealingFailed)
	}
	defer clear(plain)
	sealed, err := target.Seal(aad, plain)
	if err != nil {
		return nil, failure(SealingFailed)
	}
	checked, err := target.Open(aad, sealed)
	defer clear(checked)
	if err != nil || !bytes.Equal(plain, checked) {
		return nil, failure(SealingFailed)
	}
	return sealed, nil
}
