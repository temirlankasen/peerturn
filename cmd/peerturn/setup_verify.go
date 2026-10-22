package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func readSetupFile(root *os.Root, path string, limit int64) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) > limit {
		return nil, invalidArguments()
	}
	return data, nil
}

// The installer builds this inventory only after authenticating the complete
// archive. A custom local path requires its inventory hash through a trusted channel.
func verifySetupTree(root, pin string) error {
	if len(pin) != 64 || pin != strings.ToLower(pin) {
		return invalidArguments()
	}
	if _, err := hex.DecodeString(pin); err != nil {
		return invalidArguments()
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return invalidArguments()
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return invalidArguments()
	}
	err = verifySetupRoot(directory, pin)
	closeErr := directory.Close()
	if err != nil || closeErr != nil {
		return invalidArguments()
	}
	return nil
}

func verifySetupRoot(root *os.Root, pin string) error {
	data, err := readSetupFile(root, ".peerturn-files.sha256", 64*1024)
	if err != nil {
		return invalidArguments()
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != pin {
		return invalidArguments()
	}
	expected := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		hash, path, ok := strings.Cut(line, "  ")
		if !ok || len(hash) != 64 || !filepath.IsLocal(path) || filepath.Clean(path) != path || expected[path] != "" || strings.ContainsAny(path, "\\\r\n") {
			return invalidArguments()
		}
		if _, decodeErr := hex.DecodeString(hash); decodeErr != nil {
			return invalidArguments()
		}
		expected[path] = hash
	}
	if len(expected) == 0 || len(expected) > 256 {
		return invalidArguments()
	}
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.Type()&os.ModeSymlink != 0 {
			return invalidArguments()
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return invalidArguments()
		}
		if path == ".peerturn-files.sha256" || path == ".peerturn-verified" {
			return nil
		}
		want, ok := expected[path]
		if !ok {
			return invalidArguments()
		}
		file, openErr := root.Open(path)
		if openErr != nil {
			return invalidArguments()
		}
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(file, 128*1024*1024+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || n > 128*1024*1024 || hex.EncodeToString(hash.Sum(nil)) != want {
			return invalidArguments()
		}
		delete(expected, path)
		return nil
	})
	if err != nil || len(expected) != 0 {
		return invalidArguments()
	}
	return nil
}
