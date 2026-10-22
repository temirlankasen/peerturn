package daemon

import (
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/store"
)

const authorityMetadata = "wrapping.json"

type wrappingMetadata struct {
	Version int    `json:"version"`
	KeyID   string `json:"key_id"`
	Phase   string `json:"phase"`
}

func readWrappingMetadata(dir string) (wrappingMetadata, error) {
	var metadata wrappingMetadata
	root, err := store.OpenPrivateDirectory(dir, false)
	if err != nil {
		return metadata, err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			return
		}
	}()
	info, err := root.Lstat(authorityMetadata)
	if err != nil {
		return metadata, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !ownedJournal(info) {
		return metadata, fault.New(store.SealingFailed)
	}
	f, err := root.Open(authorityMetadata)
	if err != nil {
		return metadata, fault.New(store.SealingFailed)
	}
	opened, statErr := f.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		return metadata, errors.Join(fault.New(store.SealingFailed), f.Close())
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, 513))
	if err = errors.Join(readErr, f.Close()); err != nil || len(raw) > 512 {
		return metadata, fault.New(store.SealingFailed)
	}
	if err = codec.Decode(raw, 512, &metadata); err != nil {
		return metadata, fault.New(store.SealingFailed)
	}
	if metadata.Version != 1 || len(metadata.KeyID) != 32 || (metadata.Phase != "active" && metadata.Phase != "pending") {
		return metadata, fault.New(store.SealingFailed)
	}
	if _, err = hex.DecodeString(metadata.KeyID); err != nil {
		return metadata, fault.New(store.SealingFailed)
	}
	return metadata, nil
}

func writeWrappingMetadata(dir string, metadata wrappingMetadata, initial bool) (result error) {
	raw, err := codec.Canonical(metadata)
	if err != nil {
		return err
	}
	root, err := store.OpenPrivateDirectory(dir, false)
	if err != nil {
		return err
	}
	defer func() {
		if err = root.Close(); err != nil {
			result = fault.New(store.StorageUnavailable)
		}
	}()
	temp := ".wrapping-" + randomID()
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fault.New(store.StorageUnavailable)
	}
	defer func() {
		if err = root.Remove(temp); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = fault.New(store.StorageUnavailable)
		}
	}()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return fault.New(store.StorageUnavailable)
	}
	if initial {
		// The caller owns LockStateDir. Rename publishes one inode without the
		// hardlink cleanup window that would make pending recovery reject it.
		if _, statErr := root.Lstat(authorityMetadata); statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
			return fault.New(store.StorageUnavailable)
		}
	}
	err = root.Rename(temp, authorityMetadata)
	if err != nil {
		return fault.New(store.StorageUnavailable)
	}
	return syncAuthorityDirectory(root)
}

func syncAuthorityDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return fault.New(store.StorageUnavailable)
	}
	if err = errors.Join(f.Sync(), f.Close()); err != nil {
		return fault.New(store.StorageUnavailable)
	}
	return nil
}
