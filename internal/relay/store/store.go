package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite" // Register the pinned pure-Go SQLite driver.
)

// Store serializes custody and ACL changes through a single SQLite connection.
type Store struct {
	db              *sql.DB
	limits          Limits
	mu              sync.Mutex
	needsCheckpoint bool
	commits         uint64
	now             func() time.Time
	nextSweep       time.Time
	stats           accounting
	pending         accounting
	dataBudget      int64
	nonceLimits     [3]int
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Open creates or reopens a WAL database with FULL commit durability.
func Open(path string, limits Limits) (*Store, error) {
	limits = defaultLimits(limits)
	if limits.GlobalBytes < 1<<20 || limits.MailboxItems < 1 || limits.MailboxBytes < 1 || limits.MetadataItems < 1 {
		return nil, invalid()
	}
	abs, err := filepath.Abs(path)
	if err != nil || path == "" {
		return nil, invalid()
	}
	if prepareErr := prepareFile(abs); prepareErr != nil {
		return nil, unavailable()
	}
	query := url.Values{}
	for _, pragma := range []string{"journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(ON)", "busy_timeout(5000)", "wal_autocheckpoint(64)", "journal_size_limit(0)"} {
		query.Add("_pragma", pragma)
	}
	// Reserve half the disk budget for a worst-case WAL plus its frame headers.
	pages := (limits.GlobalBytes - (64 << 10)) / (2 * (4096 + 24))
	query.Add("_pragma", "max_page_count("+strconv.FormatInt(pages, 10)+")")
	query.Set("_txlock", "immediate")
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, unavailable()
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if verifyErr := verifyDatabase(db, pages); verifyErr != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, unavailable()
		}
		return nil, verifyErr
	}
	s := &Store{db: db, limits: limits, now: time.Now, dataBudget: pages*4096/2 - (64 << 10)}
	capacity := min(limits.MetadataItems, int(pages*4096/(8*512)))
	s.nonceLimits = [3]int{capacity, capacity, max(1, min(128, capacity/16))}
	if initErr := s.initializeSchema(context.Background()); initErr != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, unavailable()
		}
		return nil, unavailable()
	}
	if err := s.refreshAccounting(context.Background()); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, unavailable()
		}
		return nil, err
	}
	return s, nil
}

func verifyDatabase(db *sql.DB, maxPages int64) error {
	var pageSize, pageCount, actualMax, version, synchronous int64
	checks := []struct {
		query string
		value *int64
	}{
		{`PRAGMA page_size`, &pageSize},
		{`PRAGMA page_count`, &pageCount},
		{`PRAGMA max_page_count`, &actualMax},
		{`PRAGMA user_version`, &version},
		{`PRAGMA synchronous`, &synchronous},
	}
	for _, check := range checks {
		if err := db.QueryRowContext(context.Background(), check.query).Scan(check.value); err != nil {
			return unavailable()
		}
	}
	var journal string
	if err := db.QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&journal); err != nil {
		return unavailable()
	}
	if pageSize != 4096 || pageCount > maxPages || actualMax != maxPages || version < 0 || version > 2 || synchronous != 2 || journal != "wal" {
		return unavailable()
	}
	return nil
}

func prepareFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	file, err := root.OpenFile(filepath.Base(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		if closeErr := root.Close(); closeErr != nil {
			return closeErr
		}
		return err
	}
	fileErr, rootErr := file.Close(), root.Close()
	if fileErr != nil {
		return fileErr
	}
	return rootErr
}

func defaultLimits(limits Limits) Limits {
	if limits.GlobalBytes == 0 {
		limits.GlobalBytes = 1 << 30
	}
	if limits.MailboxItems == 0 {
		limits.MailboxItems = 1000
	}
	if limits.MailboxBytes == 0 {
		limits.MailboxBytes = 32 << 20
	}
	if limits.MetadataItems == 0 {
		limits.MetadataItems = 100000
	}
	return limits
}

// Close releases database resources after pending work finishes.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return unavailable()
	}
	return nil
}

// Ping verifies storage readiness without exposing diagnostics.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return unavailable()
	}
	return nil
}

// BackupEpoch survives restarts; restoring an older backup still requires re-enrollment.
func (s *Store) BackupEpoch(ctx context.Context) (int64, error) {
	var epoch int64
	if err := s.db.QueryRowContext(ctx, `SELECT backup_epoch FROM relay_state WHERE singleton=1`).Scan(&epoch); err != nil {
		return 0, unavailable()
	}
	return epoch, nil
}

func reader(ctx context.Context, tx queryer, mailbox, key string) error {
	var bound string
	if err := tx.QueryRowContext(ctx, `SELECT read_key FROM mailboxes WHERE id=?`, mailbox).Scan(&bound); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return forbidden()
		}
		return unavailable()
	}
	if bound != key {
		return forbidden()
	}
	return nil
}

func digest(value []byte) []byte {
	hash := sha256.Sum256(value)
	return hash[:]
}
