package store

import (
	"context"
	"database/sql"
	"time"
)

// UseNonce commits a normal admitted-key replay entry for at least two minutes.
func (s *Store) UseNonce(ctx context.Context, key, nonce string, until, now time.Time) error {
	return s.useNonce(ctx, key, nonce, until, now, 0)
}

// UseControlNonce keeps owner ACK, revoke, and delete admission independent of writers.
func (s *Store) UseControlNonce(ctx context.Context, key, nonce string, until, now time.Time) error {
	return s.useNonce(ctx, key, nonce, until, now, 1)
}

// UseAnonymousNonce bounds replay metadata for bootstrap without consuming owner capacity.
func (s *Store) UseAnonymousNonce(ctx context.Context, key, nonce string, until, now time.Time) error {
	return s.useNonce(ctx, key, nonce, until, now, 2)
}

func nonceKey(key string, lane int) []byte {
	return append(make([]byte, lane), digest([]byte(key))...)
}

func (s *Store) useNonce(ctx context.Context, key, nonce string, until, now time.Time, lane int) error {
	if !validID(key) || !validID(nonce) || until.After(now.Add(10*time.Minute)) {
		return invalid()
	}
	if minimum := now.Add(2 * time.Minute); until.Before(minimum) {
		until = minimum
	}
	return s.transact(ctx, now, func(tx *sql.Tx) error {
		var count int
		// The same request is a replay even if it changes admission lanes.
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nonces WHERE key_hash IN (?,?,?) AND nonce_hash=?`, nonceKey(key, 0), nonceKey(key, 1), nonceKey(key, 2), digest([]byte(nonce))).Scan(&count); err != nil {
			return unavailable()
		}
		if count != 0 {
			return &Error{Code: "replay", Status: 409}
		}
		if s.pending.nonces[lane] >= s.nonceLimits[lane] {
			return quota()
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nonces WHERE key_hash=?`, nonceKey(key, lane)).Scan(&count); err != nil {
			return unavailable()
		}
		perKey := min(256, min(s.nonceLimits[lane], max(3, s.nonceLimits[lane]/8)))
		if lane == 2 {
			perKey = min(8, perKey)
		}
		if count >= perKey {
			return quota()
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO nonces(key_hash,nonce_hash,expires_ns) VALUES(?,?,?)`, nonceKey(key, lane), digest([]byte(nonce)), until.UnixNano())
		if err != nil {
			return unavailable()
		}
		s.pending.nonces[lane]++
		return nil
	})
}
