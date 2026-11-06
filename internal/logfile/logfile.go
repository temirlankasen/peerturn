// Package logfile is a bounded, rotating operator log. The daemon writes
// codes, identifiers and stages into it so an operator can learn why a link
// or a delivery stalled; it never carries bodies, links, grants or keys.
package logfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Writer appends to one file and rotates before a write would cross the
// limit, keeping a fixed number of older files beside it.
type Writer struct {
	mu    sync.Mutex
	path  string
	limit int64
	keep  int
	file  *os.File
	size  int64
}

// Open creates the directory and the file privately. The caller falls back
// to a silent logger when the path is unusable: logging never blocks work.
func Open(path string, limit int64, keep int) (*Writer, error) {
	if limit <= 0 || keep < 1 || !filepath.IsAbs(path) {
		return nil, errors.New("logfile: invalid bounds")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	w := &Writer{path: path, limit: limit, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) open() error {
	file, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return errors.Join(err, file.Close())
	}
	w.file, w.size = file, info.Size()
	return nil
}

// Write keeps every file under the limit; a single line larger than the
// limit still goes out whole rather than being lost.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.limit {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *Writer) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	for i := w.keep - 1; i >= 1; i-- {
		err := os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return w.open()
}

// Close ends the log; later writes fail instead of reopening the file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
