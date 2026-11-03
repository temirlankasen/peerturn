package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// matchSetupPlugin requires the installed plugin to hold exactly the verified
// files under prefix; only the named optional files may be absent.
func matchSetupPlugin(root, prefix, installed string, optional ...string) error {
	if !filepath.IsAbs(installed) {
		return invalidArguments()
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return invalidArguments()
	}
	data, readErr := readSetupFile(directory, ".peerturn-files.sha256", 64*1024)
	closeErr := directory.Close()
	if readErr != nil || closeErr != nil {
		return invalidArguments()
	}
	expected := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		hash, path, ok := strings.Cut(line, "  ")
		if !ok {
			return invalidArguments()
		}
		if local, found := strings.CutPrefix(path, prefix+"/"); found {
			expected[local] = hash
		}
	}
	if len(expected) == 0 {
		return invalidArguments()
	}
	info, err := os.Lstat(installed)
	if err != nil || !info.IsDir() {
		return invalidArguments()
	}
	directory, err = os.OpenRoot(installed)
	if err != nil {
		return invalidArguments()
	}
	err = fs.WalkDir(directory.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.Type()&os.ModeSymlink != 0 {
			return invalidArguments()
		}
		if entry.IsDir() {
			return nil
		}
		want, ok := expected[path]
		if !ok || !entry.Type().IsRegular() {
			return invalidArguments()
		}
		file, openErr := directory.Open(path)
		if openErr != nil {
			return invalidArguments()
		}
		hash := sha256.New()
		n, copyErr := io.Copy(hash, io.LimitReader(file, 128*1024*1024+1))
		fileErr := file.Close()
		if copyErr != nil || fileErr != nil || n > 128*1024*1024 || hex.EncodeToString(hash.Sum(nil)) != want {
			return invalidArguments()
		}
		delete(expected, path)
		return nil
	})
	closeErr = directory.Close()
	for _, path := range optional {
		delete(expected, path)
	}
	if err != nil || closeErr != nil || len(expected) != 0 {
		return invalidArguments()
	}
	return nil
}

// matchSetupFile compares one host-managed file with its verified release copy.
func matchSetupFile(root, path, installedRoot string) bool {
	if !filepath.IsAbs(installedRoot) {
		return false
	}
	release, err := os.OpenRoot(root)
	if err != nil {
		return false
	}
	want, wantErr := readSetupFile(release, path, 64*1024)
	releaseErr := release.Close()
	installed, err := os.OpenRoot(installedRoot)
	if err != nil {
		return false
	}
	got, gotErr := readSetupFile(installed, path, 64*1024)
	installedErr := installed.Close()
	return wantErr == nil && gotErr == nil && releaseErr == nil && installedErr == nil && bytes.Equal(want, got)
}
