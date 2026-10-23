package daemon

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/store"
)

const (
	runtimePointerName  = "runtime.path"
	runtimePointerLimit = 4096
)

type runtimePointer struct {
	root     *os.Root
	info     os.FileInfo
	once     sync.Once
	closeErr error
}

// A pointer is local routing information, never session or grant authority.
func pointedRuntimeDir() (string, bool, error) {
	state, err := ConfiguredStateDir()
	if err != nil {
		return "", false, err
	}
	root, err := store.OpenPrivateDirectory(state, false)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, unsafeRuntimePointer()
	}
	dir, found, readErr := readRuntimePointer(root)
	if err = errors.Join(readErr, root.Close()); err != nil {
		return "", false, unsafeRuntimePointer()
	}
	if !found {
		return "", false, nil
	}
	runtimeRoot, err := store.OpenPrivateDirectory(dir, false)
	if err != nil {
		return "", false, unsafeRuntimePointer()
	}
	if err = runtimeRoot.Close(); err != nil {
		return "", false, unsafeRuntimePointer()
	}
	// Client.Call still validates the socket and authenticates its connected UID.
	return dir, true, nil
}

func readRuntimePointer(root *os.Root) (string, bool, error) {
	info, err := root.Lstat(runtimePointerName)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil || !privateRuntimePointer(info) {
		return "", false, unsafeRuntimePointer()
	}
	file, err := root.OpenFile(runtimePointerName, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return "", false, unsafeRuntimePointer()
	}
	opened, statErr := file.Stat()
	if statErr != nil || !privateRuntimePointer(opened) || !os.SameFile(info, opened) {
		return "", false, errors.Join(unsafeRuntimePointer(), file.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, runtimePointerLimit+1))
	if err = errors.Join(readErr, file.Close()); err != nil || len(data) > runtimePointerLimit {
		return "", false, unsafeRuntimePointer()
	}
	dir := strings.TrimSuffix(string(data), "\n")
	if !validRuntimePointerPath(dir) {
		return "", false, unsafeRuntimePointer()
	}
	return dir, true, nil
}

func privateRuntimePointer(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode().Perm() == 0600 &&
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 &&
		info.Size() <= runtimePointerLimit && ownedJournal(info)
}

func validRuntimePointerPath(dir string) bool {
	return len(dir) < runtimePointerLimit && filepath.IsAbs(dir) && filepath.Clean(dir) == dir &&
		!strings.ContainsAny(dir, "\x00\r\n")
}

func unsafeRuntimePointer() error { return localipc.NewError("unsafe_runtime", "rejected") }

// Publication happens only while Run owns both the state and runtime locks.
func publishRuntimePointer(stateDir, runtimeDir string) (_ *runtimePointer, result error) {
	if !validRuntimePointerPath(runtimeDir) {
		return nil, unsafeRuntimePointer()
	}
	root, err := store.OpenPrivateDirectory(stateDir, false)
	if err != nil {
		return nil, unsafeRuntimePointer()
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, root.Close())
		}
	}()
	if _, _, err = readRuntimePointer(root); err != nil {
		return nil, err
	}
	temp := ".runtime-" + randomID()
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, unsafeRuntimePointer()
	}
	defer func() {
		if removeErr := root.Remove(temp); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			result = errors.Join(result, unsafeRuntimePointer())
		}
	}()
	if err = file.Chmod(0600); err == nil {
		_, err = io.WriteString(file, runtimeDir+"\n")
	}
	if err == nil {
		err = file.Sync()
	}
	info, statErr := file.Stat()
	if err = errors.Join(err, statErr, file.Close()); err != nil || !privateRuntimePointer(info) {
		return nil, unsafeRuntimePointer()
	}
	if err = root.Rename(temp, runtimePointerName); err != nil {
		return nil, unsafeRuntimePointer()
	}
	pointer := &runtimePointer{root: root, info: info}
	if err = syncRuntimePointer(root); err != nil {
		return nil, errors.Join(err, root.Remove(runtimePointerName))
	}
	return pointer, nil
}

func syncRuntimePointer(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return unsafeRuntimePointer()
	}
	if err = errors.Join(dir.Sync(), dir.Close()); err != nil {
		return unsafeRuntimePointer()
	}
	return nil
}

func (p *runtimePointer) Close() error {
	p.once.Do(func() {
		info, err := p.root.Lstat(runtimePointerName)
		switch {
		case err == nil && os.SameFile(p.info, info):
			err = p.root.Remove(runtimePointerName)
			if err == nil {
				err = syncRuntimePointer(p.root)
			}
		case errors.Is(err, os.ErrNotExist):
			err = nil
		default:
			err = unsafeRuntimePointer()
		}
		p.closeErr = errors.Join(err, p.root.Close())
	})
	return p.closeErr
}
