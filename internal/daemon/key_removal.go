package daemon

import (
	"errors"
	"os"
	"syscall"

	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/store"
)

// Wipe the original inode so ordinary hardlink aliases lose their key too.
// CoW filesystems, backups and snapshots can retain old extents; the fresh key
// protects current state independently of this best-effort overwrite.
func wipeFileKey(root *os.Root) error {
	info, err := root.Lstat("seal.key")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !privateAuthorityKey(info) {
		return fault.New(store.SealingFailed)
	}
	f, err := root.OpenFile("seal.key", os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return fault.New(store.SealingFailed)
	}
	opened, statErr := f.Stat()
	if statErr != nil || !os.SameFile(info, opened) || !privateAuthorityKey(opened) {
		return errors.Join(fault.New(store.SealingFailed), f.Close())
	}
	var zeros [32]byte
	count, writeErr := f.WriteAt(zeros[:], 0)
	if writeErr != nil || count != len(zeros) {
		return errors.Join(fault.New(store.StorageUnavailable), f.Close())
	}
	if err = errors.Join(f.Sync(), f.Close()); err != nil {
		return fault.New(store.StorageUnavailable)
	}
	current, err := root.Lstat("seal.key")
	if err != nil || !os.SameFile(info, current) || !privateAuthorityKey(current) {
		return fault.New(store.SealingFailed)
	}
	return nil
}

func privateAuthorityKey(info os.FileInfo) bool {
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != 32 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(os.Geteuid())
}
