package runtimepolicy

import (
	"cmp"
	"errors"
	"strconv"
	"strings"
)

// Version is a bounded SemVer; build metadata is identity, not precedence.
type Version struct {
	Text string
	core [3]uint64
	pre  []string
}

// Parse accepts canonical SemVer only. Runtime-specific wrappers belong to Evaluate.
func Parse(raw string) (Version, error) {
	invalid := errors.New("runtime_version_unknown")
	if len(raw) == 0 || len(raw) > 128 {
		return Version{}, invalid
	}
	text, build, hasBuild := strings.Cut(raw, "+")
	if hasBuild && !identifiers(build, false) {
		return Version{}, invalid
	}
	core, pre, hasPre := strings.Cut(text, "-")
	if hasPre && !identifiers(pre, true) {
		return Version{}, invalid
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, invalid
	}
	v := Version{Text: raw}
	for i, part := range parts {
		if !digits(part) || (len(part) > 1 && part[0] == '0') {
			return Version{}, invalid
		}
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return Version{}, invalid
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
	}
	return v, nil
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func identifiers(s string, prerelease bool) bool {
	for part := range strings.SplitSeq(s, ".") {
		if part == "" || (prerelease && digits(part) && len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, c := range part {
			valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
			if !valid {
				return false
			}
		}
	}
	return true
}

// Compare follows SemVer precedence, including numeric prerelease identifiers.
func Compare(a, b Version) int {
	for i := range a.core {
		if n := cmp.Compare(a.core[i], b.core[i]); n != 0 {
			return n
		}
	}
	if len(a.pre) == 0 && len(b.pre) != 0 {
		return 1
	}
	if len(b.pre) == 0 && len(a.pre) != 0 {
		return -1
	}
	for i := 0; i < min(len(a.pre), len(b.pre)); i++ {
		x, y := a.pre[i], b.pre[i]
		if x == y {
			continue
		}
		xn, yn := digits(x), digits(y)
		switch {
		case xn && yn:
			// Comparing length first avoids integer overflow for long identifiers.
			if n := cmp.Compare(len(x), len(y)); n != 0 {
				return n
			}
		case xn:
			return -1
		case yn:
			return 1
		}
		return cmp.Compare(x, y)
	}
	return cmp.Compare(len(a.pre), len(b.pre))
}
