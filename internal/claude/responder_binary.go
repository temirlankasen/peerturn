package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type responderBinaryStamp struct {
	device   string
	inode    uint64
	size     int64
	modified time.Time
	changed  time.Time
	mode     os.FileMode
}

type responderBinaryHashEntry struct {
	mu    sync.Mutex
	stamp responderBinaryStamp
	hash  string
}

var responderBinaryHashes = struct {
	sync.Mutex
	entries map[string]*responderBinaryHashEntry
}{entries: make(map[string]*responderBinaryHashEntry)}

func configurationBinaryHash(path string) (hash string, hashErr error) {
	file, err := openResponderFile(path)
	if err != nil {
		return "", &ResponderError{Code: ConfigurationUnavailable}
	}
	defer func() {
		if file.Close() != nil {
			hash, hashErr = "", &ResponderError{Code: ConfigurationUnavailable}
		}
	}()
	return configurationBinaryFileHash(file)
}

func configurationBinaryFileHash(file *os.File) (string, error) {
	entry := responderBinaryCacheEntry(filepath.Clean(file.Name()))
	// Serialize only this pathname: unrelated binaries never wait for its read.
	entry.mu.Lock()
	defer entry.mu.Unlock()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Size() > 512<<20 {
		return "", &ResponderError{Code: ConfigurationUnavailable}
	}
	stamp, cacheable := responderBinaryFileStamp(info)
	if cacheable && entry.hash != "" && entry.stamp == stamp {
		return entry.hash, nil
	}
	hash, err := readResponderBinaryHash(file, info.Size())
	if err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil {
		return "", &ResponderError{Code: ConfigurationUnavailable}
	}
	current, currentCacheable := responderBinaryFileStamp(after)
	if !os.SameFile(info, after) || info.Size() != after.Size() || info.ModTime() != after.ModTime() ||
		info.Mode() != after.Mode() || cacheable && (!currentCacheable || current != stamp) {
		return "", &ResponderError{Code: ConfigurationUnavailable}
	}
	if cacheable {
		entry.stamp, entry.hash = stamp, hash
	}
	return hash, nil
}

func responderBinaryCacheEntry(path string) *responderBinaryHashEntry {
	responderBinaryHashes.Lock()
	defer responderBinaryHashes.Unlock()
	if entry := responderBinaryHashes.entries[path]; entry != nil {
		return entry
	}
	// Bindings are few and private; bound stale pathname retention across rebinds.
	if len(responderBinaryHashes.entries) >= 64 {
		clear(responderBinaryHashes.entries)
	}
	entry := &responderBinaryHashEntry{}
	responderBinaryHashes.entries[path] = entry
	return entry
}

func readResponderBinaryHash(file *os.File, size int64) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", &ResponderError{Code: ConfigurationUnavailable}
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, (512<<20)+1))
	if err != nil || count != size || count > 512<<20 {
		return "", &ResponderError{Code: ConfigurationUnavailable}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
