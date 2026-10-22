package store

import (
	"bytes"
	"context"
	"database/sql"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/protocol"
)

// AskFork is local cleanup metadata, never peer receipt or answer evidence.
type AskFork struct {
	RunID    string
	NativeID []byte
	Snapshot []byte
}

// RememberAskFork durably binds a preselected native child ID before spawn.
// Recording an ID does not claim native acceptance or advance any receipt.
func (s *Store) RememberAskFork(ctx context.Context, runID string, nativeID []byte, now int64) error {
	if !protocol.ValidID(runID) || len(nativeID) == 0 || len(nativeID) > 4096 || now <= 0 || now > protocol.MaxSafeInteger {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		a, err := s.loadAsk(ctx, tx, runID)
		if err != nil {
			return err
		}
		if a.state != "reserved" || (a.attemptState != "intent" && a.attemptState != "authorized") {
			return failure(fault.Conflict)
		}
		if _, err = s.authorizeAsk(ctx, tx, a, now); err != nil {
			return err
		}
		return s.rememberAskFork(ctx, tx, runID, nativeID)
	})
}

func (s *Store) rememberAskFork(ctx context.Context, tx *sql.Tx, runID string, nativeID []byte) error {
	var previous []byte
	if err := tx.QueryRowContext(ctx, `SELECT fork_native_id FROM responder_runs WHERE run_id=?`, runID).Scan(&previous); err != nil {
		return lookupError(err)
	}
	if len(previous) != 0 {
		plain, err := s.unseal("responder_fork", runID, previous)
		if err != nil {
			return err
		}
		if !bytes.Equal(plain, nativeID) {
			return failure(fault.Conflict)
		}
		return nil
	}
	sealed, err := s.seal("responder_fork", runID, nativeID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE responder_runs SET fork_native_id=? WHERE run_id=?`, sealed, runID)
	return databaseError(err)
}

// ForgetAskFork records trusted local proof that the exact native child's
// journal was cleaned. It changes no run, admission or delivery evidence; revoked
// authority must not prevent finishing cleanup after the native process exits.
func (s *Store) ForgetAskFork(ctx context.Context, runID string, nativeID []byte) error {
	if !protocol.ValidID(runID) || len(nativeID) == 0 || len(nativeID) > 4096 {
		return failure(fault.InvalidEncoding)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var sealed []byte
		if err := tx.QueryRowContext(ctx, `SELECT fork_native_id FROM responder_runs WHERE run_id=?`, runID).Scan(&sealed); err != nil {
			return lookupError(err)
		}
		if len(sealed) == 0 {
			return failure(fault.Conflict)
		}
		stored, err := s.unseal("responder_fork", runID, sealed)
		if err != nil {
			return err
		}
		if !bytes.Equal(stored, nativeID) {
			return failure(fault.Conflict)
		}
		_, err = tx.ExecContext(ctx, `UPDATE responder_runs SET fork_native_id=NULL WHERE run_id=?`, runID)
		return databaseError(err)
	})
}

// ListAskForks includes terminal runs whose native journal cleanup may have
// failed. It never selects a replacement parent, discovers sessions or replays.
func (s *Store) ListAskForks(ctx context.Context) ([]AskFork, error) {
	var result []AskFork
	err := s.write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT run_id,fork_native_id FROM responder_runs WHERE length(fork_native_id)>0 ORDER BY run_id`)
		if err != nil {
			return failure(StorageUnavailable)
		}
		var sealedIDs [][]byte
		for rows.Next() {
			var fork AskFork
			var sealed []byte
			if err = rows.Scan(&fork.RunID, &sealed); err != nil {
				break
			}
			result = append(result, fork)
			sealedIDs = append(sealedIDs, sealed)
		}
		iterErr, closeErr := rows.Err(), rows.Close()
		if err != nil || iterErr != nil || closeErr != nil {
			return failure(StorageUnavailable)
		}
		for i := range result {
			fork := &result[i]
			a, loadErr := s.loadAsk(ctx, tx, fork.RunID)
			if loadErr != nil {
				return loadErr
			}
			fork.Snapshot = a.Snapshot
			if fork.NativeID, err = s.unseal("responder_fork", fork.RunID, sealedIDs[i]); err != nil {
				return err
			}
			if len(fork.NativeID) == 0 || len(fork.NativeID) > 4096 {
				return failure(InvalidState)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
