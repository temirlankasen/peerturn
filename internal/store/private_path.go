package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func ownedBy(info os.FileInfo, uid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(uid)
}

func privateFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && ownedBy(info, os.Geteuid())
}

func privateDir(path string) error      { return preparePrivateDir(path, true) }
func checkPrivateDir(path string) error { return preparePrivateDir(path, false) }

func preparePrivateDir(path string, create bool) (err error) {
	root, err := directoryRoot(path, create)
	if err != nil {
		return failure(StorageUnavailable)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			err = failure(StorageUnavailable)
		}
	}()
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !ownedBy(info, os.Geteuid()) {
		return failure(StorageUnavailable)
	}
	return nil
}

// Each ancestor is inspected relative to its open parent before descent. The
// private leaf's owner controls later path reuse; same-uid compromise is excluded.
func directoryRoot(path string, create bool) (*os.Root, error) {
	if !filepath.IsAbs(path) {
		return nil, failure(StorageUnavailable)
	}
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	for part := range strings.SplitSeq(strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		next, openErr := descendDirectory(root, part, create)
		closeErr := root.Close()
		if openErr != nil {
			return nil, openErr
		}
		if closeErr != nil {
			if err = next.Close(); err != nil {
				return nil, err
			}
			return nil, closeErr
		}
		root = next
	}
	return root, nil
}

func descendDirectory(root *os.Root, name string, create bool) (*os.Root, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = root.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if err == nil {
			if err = root.Chmod(name, 0700); err != nil {
				return nil, err
			}
		}
		info, err = root.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || (!ownedBy(info, os.Geteuid()) && !ownedBy(info, 0)) {
		return nil, failure(StorageUnavailable)
	}
	if info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0 {
		return nil, failure(StorageUnavailable)
	}
	return root.OpenRoot(name)
}

func syncDir(path string) error {
	f, err := openLocalFile(path, os.O_RDONLY)
	if err != nil {
		return failure(StorageUnavailable)
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return failure(StorageUnavailable)
	}
	return nil
}

func openLocalFile(path string, flags int) (*os.File, error) {
	root, err := directoryRoot(filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	f, openErr := root.OpenFile(filepath.Base(path), flags, 0600)
	if openErr == nil {
		info, statErr := root.Lstat(filepath.Base(path))
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			openErr = failure(StorageUnavailable)
		}
	}
	closeErr := root.Close()
	if openErr != nil || closeErr != nil {
		if f != nil {
			if err = f.Close(); err != nil {
				return nil, err
			}
		}
		if openErr != nil {
			return nil, openErr
		}
		return nil, failure(StorageUnavailable)
	}
	return f, nil
}
