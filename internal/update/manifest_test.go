package update_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/update"
)

const (
	hashA = "46fa32b871f47404e16217c398a835d014d439c2af5c016db048ca27668c7b84"
	hashB = "dd1efe4a3f40aa3b0a6485a010442e83ba91fc9ecb24385f25c193bde3eaf007"
)

func TestParseManifestAcceptsReleaseFormat(t *testing.T) {
	t.Parallel()
	manifest, err := update.ParseManifest([]byte(hashB + "  install.ps1\n" + hashA + "  install.sh\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := manifest.Entry("install.sh"); err != nil || got != hashA {
		t.Fatalf("Entry(install.sh) = %q, %v", got, err)
	}
}

func TestParseManifestRejectsInvalidLines(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"empty":             "",
		"short hash":        hashA[:63] + "  install.sh\n",
		"long hash":         hashA + "0  install.sh\n",
		"uppercase hash":    strings.ToUpper(hashA) + "  install.sh\n",
		"non-hex hash":      strings.Repeat("g", 64) + "  install.sh\n",
		"single space":      hashA + " install.sh\n",
		"tab separator":     hashA + "\tinstall.sh\n",
		"missing name":      hashA + "  \n",
		"path in name":      hashA + "  ../install.sh\n",
		"space in name":     hashA + "  install .sh\n",
		"carriage return":   hashA + "  install.sh\r\n",
		"blank middle line": hashA + "  install.sh\n\n" + hashB + "  install.ps1\n",
		"unrelated text":    "not a manifest\n",
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := update.ParseManifest([]byte(data)); err == nil {
				t.Errorf("ParseManifest accepted %q", data)
			}
		})
	}
}

func TestManifestEntryRequiresExactlyOneLine(t *testing.T) {
	t.Parallel()
	manifest, err := update.ParseManifest([]byte(hashA + "  install.sh\n" + hashB + "  install.sh\n" + hashB + "  other\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manifest.Entry("install.sh"); err == nil {
		t.Error("a duplicated entry was accepted")
	}
	if _, err = manifest.Entry("missing"); err == nil {
		t.Error("a missing entry was accepted")
	}
	if got, err := manifest.Entry("other"); err != nil || got != hashB {
		t.Errorf("Entry(other) = %q, %v", got, err)
	}
}

func TestMatchesSHA256(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte("script"))
	if !update.MatchesSHA256([]byte("script"), hex.EncodeToString(sum[:])) {
		t.Error("the digest of the content was rejected")
	}
	if update.MatchesSHA256([]byte("script!"), hex.EncodeToString(sum[:])) {
		t.Error("a different content matched")
	}
	if update.MatchesSHA256([]byte("script"), strings.ToUpper(hex.EncodeToString(sum[:]))) {
		t.Error("a non-canonical digest matched")
	}
}
