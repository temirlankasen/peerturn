// Package store owns client custody transactions; no method performs native effects.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/temirlankasen/peerturn/internal/fault"
	_ "modernc.org/sqlite" // Registers the pure-Go database/sql driver.
)

// Store-specific errors share M1a's typed, input-free fault representation.
const (
	StorageUnavailable fault.Code = "store_unavailable"
	NewerSchema        fault.Code = "newer_schema"
	SealingFailed      fault.Code = "sealing_failed"
	NotFound           fault.Code = "not_found"
	InvalidState       fault.Code = "invalid_state"
	Backpressure       fault.Code = "backpressure"
	ReceiptUnavailable fault.Code = "receipt_unavailable"
)

// Options requires an explicit local sealing policy, never a plaintext fallback.
type Options struct {
	Sealer Sealer
	// RequireExistingSchema forbids initialization when the daemon journal
	// already exists. A lost/truncated database must not become an empty store.
	RequireExistingSchema bool
	// Clock is the trusted local retention clock; nil uses time.Now.
	Clock func() time.Time
}

// Store serializes read-modify-write operations with SQLite IMMEDIATE transactions,
// including across independent handles/processes. It is safe for concurrent callers.
type Store struct {
	db     *sql.DB
	sealer Sealer
	clock  func() time.Time
	// abort is an internal crash-window seam; production leaves it nil.
	abort func(string) error
}

//go:embed migrations/*.sql
var migrations embed.FS

const schemaVersion = 1

// Open refuses ambiguous paths and newer schemas before admitting any work.
func Open(ctx context.Context, path string, o Options) (*Store, error) {
	if o.Sealer == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, failure(fault.InvalidEncoding)
	}
	if err := privateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	flags := os.O_RDWR | os.O_CREATE | os.O_EXCL
	if o.RequireExistingSchema {
		flags = os.O_RDWR
	}
	f, err := openLocalFile(path, flags)
	if errors.Is(err, os.ErrExist) {
		f, err = openLocalFile(path, os.O_RDWR)
	} else if err == nil && !o.RequireExistingSchema {
		err = f.Chmod(0600)
		if err != nil {
			if closeErr := f.Close(); closeErr != nil {
				return nil, failure(StorageUnavailable)
			}
		}
	}
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	info, statErr := f.Stat()
	closeErr := f.Close()
	if statErr != nil || closeErr != nil || !privateFile(info) {
		return nil, failure(StorageUnavailable)
	}
	q := url.Values{"_txlock": {"immediate"}, "_pragma": {
		"journal_mode(WAL)", "foreign_keys(ON)", "busy_timeout(10000)",
		"synchronous(FULL)", "fullfsync(ON)", "checkpoint_fullfsync(ON)", "secure_delete(ON)", "temp_store(MEMORY)",
	}}
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	db.SetMaxOpenConns(1)
	if o.Clock == nil {
		o.Clock = time.Now
	}
	s := &Store{db: db, sealer: o.Sealer, clock: o.Clock}
	if err = s.migrate(ctx, o.RequireExistingSchema); err == nil {
		err = syncDir(filepath.Dir(path))
	}
	if err != nil {
		if closeErr = db.Close(); closeErr != nil {
			return nil, failure(StorageUnavailable)
		}
		return nil, err
	}
	return s, nil
}

// Close leaves WAL recovery to SQLite; it never reports pending work as delivered.
func (s *Store) Close() error { return databaseError(s.db.Close()) }

func (s *Store) migrate(ctx context.Context, requireExisting bool) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if !requireExisting {
			if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version(version INTEGER PRIMARY KEY CHECK(version>0))`); err != nil {
				return failure(StorageUnavailable)
			}
		}
		var version, count int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0),COUNT(*) FROM schema_version`).Scan(&version, &count); err != nil {
			return failure(StorageUnavailable)
		}
		if version > schemaVersion {
			return failure(NewerSchema)
		}
		if count != version || (requireExisting && version != schemaVersion) {
			return failure(InvalidState)
		}
		for v := version + 1; v <= schemaVersion; v++ {
			data, err := migrations.ReadFile(fmt.Sprintf("migrations/%03d.sql", v))
			if err != nil {
				return failure(StorageUnavailable)
			}
			if _, err = tx.ExecContext(ctx, string(data)); err != nil {
				return failure(StorageUnavailable)
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES(?)`, v); err != nil {
				return failure(StorageUnavailable)
			}
		}
		return s.checkKey(ctx, tx, version == 0)
	})
}

func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) (result error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return failure(StorageUnavailable)
	}
	// Rollback also runs while unwinding a panicking injected dependency, so a
	// failed signer cannot strand the sole writer connection or expose partial state.
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			result = failure(StorageUnavailable)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = s.step("before_commit"); err != nil {
		return err
	}
	return databaseError(tx.Commit())
}

func (s *Store) step(name string) error {
	if s.abort != nil {
		return s.abort(name)
	}
	return nil
}

func (s *Store) seal(table, id string, data []byte) ([]byte, error) {
	b, err := s.sealer.Seal([]byte("peerturn/store/v1/"+table+"/"+id), data)
	if err != nil {
		return nil, failure(SealingFailed)
	}
	return b, nil
}

func (s *Store) unseal(table, id string, data []byte) ([]byte, error) {
	b, err := s.sealer.Open([]byte("peerturn/store/v1/"+table+"/"+id), data)
	if err != nil {
		return nil, failure(SealingFailed)
	}
	return b, nil
}

func failure(code fault.Code) error { return fault.New(code) }
func databaseError(err error) error {
	if err != nil {
		return failure(StorageUnavailable)
	}
	return nil
}
