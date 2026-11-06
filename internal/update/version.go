// Package update finds the latest PeerTurn release and verifies its installer.
//
// It never installs anything itself: it only downloads, checks and hands back
// a script that the caller runs, so the trust decision stays in one place.
package update

import (
	"strconv"
	"strings"
)

// maxComponentDigits keeps every version number far from integer overflow.
const maxComponentDigits = 9

type semantic [3]int

// ValidVersion reports whether v is MAJOR.MINOR.PATCH with an optional -suffix.
// The check also guards version strings that become URL and file-name parts.
func ValidVersion(v string) bool {
	_, ok := parseVersion(v)
	return ok
}

// Newer reports whether latest is a higher MAJOR.MINOR.PATCH than current.
// The -preview suffix is ignored for ordering; anything unparsable is never newer,
// so a development build or a malformed remote tag cannot trigger an upgrade.
func Newer(current, latest string) bool {
	have, haveOK := parseVersion(strings.TrimPrefix(current, "v"))
	want, wantOK := parseVersion(strings.TrimPrefix(latest, "v"))
	if !haveOK || !wantOK {
		return false
	}
	for i := range want {
		if want[i] != have[i] {
			return want[i] > have[i]
		}
	}
	return false
}

func parseVersion(v string) (semantic, bool) {
	core, suffix, hasSuffix := strings.Cut(v, "-")
	if hasSuffix && !validSuffix(suffix) {
		return semantic{}, false
	}
	parts := strings.Split(core, ".")
	if len(parts) != len(semantic{}) {
		return semantic{}, false
	}
	var parsed semantic
	for i, part := range parts {
		number, ok := component(part)
		if !ok {
			return semantic{}, false
		}
		parsed[i] = number
	}
	return parsed, true
}

func component(part string) (int, bool) {
	if part == "" || len(part) > maxComponentDigits || (len(part) > 1 && part[0] == '0') {
		return 0, false
	}
	for _, r := range part {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	number, err := strconv.Atoi(part)
	return number, err == nil
}

func validSuffix(suffix string) bool {
	if suffix == "" || strings.HasPrefix(suffix, ".") || strings.HasSuffix(suffix, ".") {
		return false
	}
	for _, r := range suffix {
		alphanumeric := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !alphanumeric && r != '.' && r != '-' {
			return false
		}
	}
	return true
}
