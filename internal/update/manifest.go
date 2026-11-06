package update

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const sha256HexLength = 64

type manifestEntry struct{ hash, name string }

// Manifest is a parsed "<sha256>  <asset>" checksum list as written by the release workflow.
type Manifest struct{ entries []manifestEntry }

// ParseManifest accepts only the canonical sha256sum layout: 64 lowercase hex
// digits, two spaces and a plain file name. Anything looser could let a
// crafted line be read differently here than by the shell installer.
func ParseManifest(data []byte) (Manifest, error) {
	text, found := strings.CutSuffix(string(data), "\n")
	if !found || text == "" {
		return Manifest{}, errors.New("checksum manifest is empty or lacks a final newline")
	}
	var manifest Manifest
	for number, line := range strings.Split(text, "\n") {
		hash, name, ok := strings.Cut(line, "  ")
		if !ok || !validHash(hash) || !validAssetName(name) {
			return Manifest{}, fmt.Errorf("checksum manifest line %d is not \"<sha256>  <file>\"", number+1)
		}
		manifest.entries = append(manifest.entries, manifestEntry{hash: hash, name: name})
	}
	return manifest, nil
}

// Entry returns the digest listed for name; the name must be listed exactly once.
func (m Manifest) Entry(name string) (string, error) {
	var hash string
	count := 0
	for _, entry := range m.entries {
		if entry.name == name {
			hash = entry.hash
			count++
		}
	}
	if count != 1 {
		return "", fmt.Errorf("checksum manifest lists %s %d times, expected exactly once", name, count)
	}
	return hash, nil
}

// MatchesSHA256 compares the content's digest with a canonical lowercase hex digest.
func MatchesSHA256(content []byte, want string) bool {
	sum := sha256.Sum256(content)
	got := hex.EncodeToString(sum[:])
	return len(want) == sha256HexLength && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func validHash(hash string) bool {
	if len(hash) != sha256HexLength {
		return false
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validAssetName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		alphanumeric := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !alphanumeric && r != '.' && r != '_' && r != '-' && r != '+' {
			return false
		}
	}
	return true
}
