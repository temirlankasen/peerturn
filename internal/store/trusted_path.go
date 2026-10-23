package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ResolveTrustedPath resolves aliases only through root- or effective-UID-owned
// components. A link's parent must not be writable by others, even when sticky.
// Missing suffixes are preserved to recognize recorded, removed package paths;
// callers requiring an existing file or directory must check that separately.
func ResolveTrustedPath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return "", failure(StorageUnavailable)
	}
	for range 40 {
		if len(path) > 4096 {
			return "", failure(StorageUnavailable)
		}
		resolved, again, err := resolveTrustedPath(path)
		if err != nil {
			return "", failure(StorageUnavailable)
		}
		if !again {
			return resolved, nil
		}
		path = resolved
	}
	return "", failure(StorageUnavailable)
}

func resolveTrustedPath(path string) (canonical string, again bool, err error) {
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return "", false, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	current := string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(path, current), current)
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			next, openErr := directoryRoot(filepath.Dir(current), false)
			if openErr != nil {
				return "", false, openErr
			}
			closeErr := root.Close()
			root, current = next, filepath.Dir(current)
			if closeErr != nil {
				return "", false, closeErr
			}
			continue
		}
		info, statErr := root.Lstat(part)
		if errors.Is(statErr, os.ErrNotExist) {
			for _, remaining := range parts[i:] {
				if remaining == "." || remaining == ".." {
					return "", false, failure(StorageUnavailable)
				}
			}
			return filepath.Join(append([]string{current}, parts[i:]...)...), false, nil
		}
		if statErr != nil {
			return "", false, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			parent, parentErr := root.Stat(".")
			if parentErr != nil || !trustedPathLink(info, parent) {
				return "", false, failure(StorageUnavailable)
			}
			target, linkErr := root.Readlink(part)
			if linkErr != nil {
				return "", false, linkErr
			}
			if !filepath.IsAbs(target) {
				target = current + string(filepath.Separator) + target
			}
			if suffix := strings.Join(parts[i+1:], string(filepath.Separator)); suffix != "" {
				target += string(filepath.Separator) + suffix
			}
			return target, true, nil
		}
		if i == len(parts)-1 && !info.IsDir() {
			if !info.Mode().IsRegular() || !trustedPathOwner(info) || info.Mode().Perm()&0022 != 0 {
				return "", false, failure(StorageUnavailable)
			}
			return filepath.Join(current, part), false, nil
		}
		next, openErr := descendDirectory(root, part, false)
		if openErr != nil {
			return "", false, openErr
		}
		closeErr := root.Close()
		root = next
		if closeErr != nil {
			return "", false, closeErr
		}
		current = filepath.Join(current, part)
	}
	return current, false, nil
}

func trustedPathOwner(info os.FileInfo) bool {
	return ownedBy(info, 0) || ownedBy(info, os.Geteuid())
}

func trustedPathLink(link, parent os.FileInfo) bool {
	return trustedPathOwner(link) && trustedPathOwner(parent) && parent.Mode().Perm()&0022 == 0
}
