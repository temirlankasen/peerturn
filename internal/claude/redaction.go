package claude

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/temirlankasen/peerturn/internal/outbound"
)

// These are bounded final-answer heuristics, not a DLP or encoded-secret proof.
// Go's regexp engine guarantees linear-time matching on untrusted model output.
var answerSecretPatterns = []struct {
	class   string
	pattern *regexp.Regexp
}{
	{"private_key", regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----.*?-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----`)},
	{"github", regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`)},
	{"aws_access_key", regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`)},
	{"api_token", regexp.MustCompile(`sk-(?:ant-[A-Za-z0-9_-]{20,}|proj-[A-Za-z0-9_-]{20,}|[A-Za-z0-9]{20,})`)},
	{"bearer", regexp.MustCompile(`(?i)\b(?:authorization\s*:\s*)?bearer\s+[A-Za-z0-9._~+/=-]{8,}`)},
	{"credential", regexp.MustCompile(`(?i)\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret|password)\s*[=:]\s*["']?[^\s,"';]{4,}["']?`)},
}

type redactionSpan struct {
	start, end int
	class      string
}

func redactAnswer(answer string, known []string) (string, int, error) {
	const maxMatches = 65536
	if !utf8.ValidString(answer) || len(answer) > 16<<10 || len(known) > 128 {
		return "", 0, &ResponderError{Code: RedactionFailed}
	}
	var spans []redactionSpan
	for _, rule := range answerSecretPatterns {
		for _, match := range rule.pattern.FindAllStringIndex(answer, maxMatches+1) {
			spans = append(spans, redactionSpan{match[0], match[1], rule.class})
		}
		if len(spans) > maxMatches {
			return "", 0, &ResponderError{Code: RedactionFailed}
		}
	}
	for _, secret := range known {
		if secret == "" {
			continue
		}
		variants, err := outbound.SecretEncodings(secret)
		if err != nil {
			return "", 0, &ResponderError{Code: RedactionFailed}
		}
		for _, value := range variants {
			for offset := 0; offset < len(answer); {
				index := strings.Index(answer[offset:], value)
				if index < 0 {
					break
				}
				start := offset + index
				spans = append(spans, redactionSpan{start, start + len(value), "known"})
				if len(spans) > maxMatches {
					return "", 0, &ResponderError{Code: RedactionFailed}
				}
				offset = start + 1
			}
		}
	}
	if len(spans) == 0 {
		return answer, 0, nil
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		if spans[i].end != spans[j].end {
			return spans[i].end > spans[j].end
		}
		return spans[i].class < spans[j].class
	})
	merged := spans[:1]
	for _, span := range spans[1:] {
		last := &merged[len(merged)-1]
		if span.start < last.end {
			last.end = max(last.end, span.end)
			if span.class == "known" {
				last.class = "known"
			}
		} else {
			merged = append(merged, span)
		}
	}
	var output strings.Builder
	offset := 0
	for _, span := range merged {
		output.WriteString(answer[offset:span.start])
		output.WriteString("[REDACTED:" + span.class + "]")
		offset = span.end
	}
	output.WriteString(answer[offset:])
	return output.String(), len(merged), nil
}
