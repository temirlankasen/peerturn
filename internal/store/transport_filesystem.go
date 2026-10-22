package store

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/temirlankasen/peerturn/internal/fault"
)

// OpenPrivateDirectory shares the store's canonical, owned filesystem boundary
// with client configuration. The open root confines subsequent relative operations.
func OpenPrivateDirectory(path string, create bool) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, failure(fault.InvalidEncoding)
	}
	root, err := directoryRoot(path, create)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !ownedBy(info, os.Geteuid()) {
		return nil, errors.Join(failure(StorageUnavailable), root.Close())
	}
	return root, nil
}
