package localipc

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// StateLock owns the sealed journal independently of the selected IPC runtime.
type StateLock struct {
	directory *os.File
	file      *os.File
	once      sync.Once
	closeErr  error
}

// LockStateDir takes an exclusive lock through the verified private state root.
// The inode remains in place after Close so competing processes use one flock.
func LockStateDir(dir string, create bool) (*StateLock, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, NewError("invalid_encoding", "rejected")
	}
	directory, err := privateDirectory(dir, create)
	if err != nil {
		return nil, err
	}
	file, err := lockDirectory(directory, "state.lock")
	if err != nil {
		return nil, errors.Join(err, directory.Close())
	}
	return &StateLock{directory: directory, file: file}, nil
}

// Close releases ownership after all state users have finished.
func (l *StateLock) Close() error {
	l.once.Do(func() { l.closeErr = errors.Join(l.closeErr, l.file.Close(), l.directory.Close()) })
	return l.closeErr
}
