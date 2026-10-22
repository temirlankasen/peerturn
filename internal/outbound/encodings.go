// Package outbound provides bounded known-secret encodings for final-answer policy.
// It cannot recognize arbitrary ciphers, chunking or paraphrases of a secret.
package outbound

import (
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// SecretEncodings includes literal bytes, hex, Base32/Base64 alphabets, URL, JSON
// and HTML escapes. Binary keys are accepted, but not rendered as invalid UTF-8.
// No filesystem reads or recursively unbounded encoding expansion occur here.
func SecretEncodings(value string) ([]string, error) {
	if len(value) == 0 || len(value) > 64<<10 {
		return nil, errors.New("redaction_failed")
	}
	data := []byte(value)
	variants := []string{hex.EncodeToString(data), strings.ToUpper(hex.EncodeToString(data))}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		variants = append(variants, encoding.EncodeToString(data))
	}
	for _, encoding := range []*base32.Encoding{base32.StdEncoding, base32.HexEncoding} {
		encoded := encoding.EncodeToString(data)
		variants = append(variants, encoded, strings.TrimRight(encoded, "="), strings.ToLower(encoded), strings.ToLower(strings.TrimRight(encoded, "=")))
	}
	var percent strings.Builder
	for _, b := range data {
		fmt.Fprintf(&percent, "%%%02X", b)
	}
	variants = append(variants, percent.String(), strings.ToLower(percent.String()))
	if utf8.ValidString(value) {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, errors.New("redaction_failed")
		}
		variants = append(variants, value, string(encoded[1:len(encoded)-1]),
			url.QueryEscape(value), url.PathEscape(value), html.EscapeString(value))
		quoted := strconv.QuoteToASCII(value)
		variants = append(variants, quoted[1:len(quoted)-1])
		var escaped, decimal, hexadecimal strings.Builder
		for _, unit := range utf16.Encode([]rune(value)) {
			fmt.Fprintf(&escaped, `\u%04x`, unit)
		}
		for _, r := range value {
			fmt.Fprintf(&decimal, "&#%d;", r)
			fmt.Fprintf(&hexadecimal, "&#x%x;", r)
		}
		variants = append(variants, escaped.String(), upperHexEscapes(escaped.String()), decimal.String(), hexadecimal.String())
	}
	slices.Sort(variants)
	return slices.Compact(variants), nil
}

func upperHexEscapes(input string) string {
	var output strings.Builder
	for i := 0; i < len(input); i += 6 {
		output.WriteString(`\u`)
		output.WriteString(strings.ToUpper(input[i+2 : i+6]))
	}
	return output.String()
}
