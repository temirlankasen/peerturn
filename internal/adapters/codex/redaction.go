package codex

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/temirlankasen/peerturn/internal/outbound"
)

// KnownSecret supplies an already-known value. Redaction never reads secret files.
type KnownSecret struct {
	Class string
	Value string
}

// RedactedAnswer counts deterministic replacement spans in the complete answer.
type RedactedAnswer struct {
	Text  string
	Count int
}

type redactionSpan struct {
	start, end int
	class      string
}

var outboundPatterns = []struct {
	class   string
	pattern *regexp.Regexp
}{
	{"private_key", regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----.*?-----END (?:[A-Z ]+ )?PRIVATE KEY-----`)},
	{"token", regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[A-Z0-9]{16})\b`)},
	{"authorization", regexp.MustCompile(`(?i)\bBearer[ \t]+[A-Za-z0-9._~+/-]{16,}={0,2}`)},
	{"path_reference", regexp.MustCompile(`@(?:/|~/|\.\.?/)[^\s\x00<>"']+`)},
}

// RedactAnswer applies known-value and token/path-reference patterns without
// following references. Encodings listed by outbound.SecretEncodings are covered;
// arbitrary transformations/paraphrases and unrecognized personal
// data remain residual risks; this function is not a confidentiality sandbox.
func RedactAnswer(input string, secrets []KnownSecret) (RedactedAnswer, error) {
	if !utf8.ValidString(input) || len(input) > 16<<10 || len(secrets) > 128 {
		return RedactedAnswer{}, &ResponderError{Reason: "redaction_failed"}
	}
	spans := make([]redactionSpan, 0)
	for _, secret := range secrets {
		if !validRedactionClass(secret.Class) {
			return RedactedAnswer{}, &ResponderError{Reason: "redaction_failed"}
		}
		variants, err := outbound.SecretEncodings(secret.Value)
		if err != nil {
			return RedactedAnswer{}, &ResponderError{Reason: "redaction_failed"}
		}
		for _, value := range variants {
			for offset := 0; offset < len(input); {
				index := strings.Index(input[offset:], value)
				if index < 0 {
					break
				}
				start := offset + index
				spans = append(spans, redactionSpan{start, start + len(value), secret.Class})
				if len(spans) > 65536 {
					return RedactedAnswer{}, &ResponderError{Reason: "redaction_failed"}
				}
				offset = start + 1
			}
		}
	}
	for _, pattern := range outboundPatterns {
		for _, match := range pattern.pattern.FindAllStringIndex(input, -1) {
			spans = append(spans, redactionSpan{match[0], match[1], pattern.class})
		}
	}
	slices.SortFunc(spans, func(a, b redactionSpan) int {
		if a.start != b.start {
			return cmp.Compare(a.start, b.start)
		}
		if a.end != b.end {
			return cmp.Compare(b.end, a.end)
		}
		return cmp.Compare(a.class, b.class)
	})
	var output strings.Builder
	offset, count := 0, 0
	for i := 0; i < len(spans); {
		span := spans[i]
		i++
		if span.start < offset {
			continue
		}
		// Union overlapping detections so a shorter pattern cannot expose a suffix.
		for i < len(spans) && spans[i].start < span.end {
			span.end = max(span.end, spans[i].end)
			i++
		}
		output.WriteString(input[offset:span.start])
		output.WriteString("[REDACTED:" + span.class + "]")
		offset = span.end
		count++
	}
	output.WriteString(input[offset:])
	if output.Len() > 16<<10 {
		return RedactedAnswer{}, &ResponderError{Reason: "redaction_failed"}
	}
	return RedactedAnswer{output.String(), count}, nil
}

func validRedactionClass(class string) bool {
	if len(class) == 0 || len(class) > 32 {
		return false
	}
	for _, c := range class {
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return true
}
