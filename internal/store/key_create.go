package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/temirlankasen/peerturn/internal/fault"
)

// CreateFileSealer explicitly initializes a new key without replacing any path.
// A synced temporary inode is linked into place so concurrent opens never see a
// partial key, and a losing initializer cannot replace the winner's key.
func CreateFileSealer(path string) (_ Sealer, err error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, failure(fault.InvalidEncoding)
	}
	if err = privateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	root, err := directoryRoot(filepath.Dir(path), false)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			err = failure(StorageUnavailable)
		}
	}()
	var suffix [16]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		return nil, failure(SealingFailed)
	}
	temp := ".peerturn-key-" + hex.EncodeToString(suffix[:])
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, failure(StorageUnavailable)
	}
	defer func() {
		if removeErr := root.Remove(temp); removeErr != nil && err == nil {
			err = failure(StorageUnavailable)
		}
	}()
	var key [32]byte
	defer clear(key[:])
	if err = f.Chmod(0600); err == nil {
		_, err = rand.Read(key[:])
	}
	if err == nil {
		_, err = f.Write(key[:])
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return nil, failure(StorageUnavailable)
	}
	if err = root.Link(temp, filepath.Base(path)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, failure(fault.Conflict)
		}
		return nil, failure(StorageUnavailable)
	}
	if err = syncDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return OpenFileSealer(path)
}
