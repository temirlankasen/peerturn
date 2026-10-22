package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
)

const keyCheckAAD = "peerturn/store/v1/keycheck"
const keyCheckPlain = "PeerTurn local store key check v1"

func (s *Store) checkKey(ctx context.Context, tx *sql.Tx, initialize bool) error {
	if initialize {
		sealed, err := s.sealer.Seal([]byte(keyCheckAAD), []byte(keyCheckPlain))
		if err != nil {
			return failure(SealingFailed)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO key_check(id,sealed) VALUES(1,?)`, sealed); err != nil {
			return failure(StorageUnavailable)
		}
	}
	var sealed []byte
	if err := tx.QueryRowContext(ctx, `SELECT sealed FROM key_check WHERE id=1`).Scan(&sealed); err != nil {
		return failure(SealingFailed)
	}
	plain, err := s.sealer.Open([]byte(keyCheckAAD), sealed)
	defer clear(plain)
	if err != nil || subtle.ConstantTimeCompare(plain, []byte(keyCheckPlain)) != 1 {
		return failure(SealingFailed)
	}
	return nil
}
