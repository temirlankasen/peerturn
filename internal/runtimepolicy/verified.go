package runtimepolicy

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed verified.json
var verifiedJSON []byte

var packagedVerified = loadVerified(verifiedJSON)

func loadVerified(raw []byte) map[string][]Evidence {
	records, err := decodeVerified(raw)
	if err != nil {
		// Invalid qualification data cannot enable verification or ask. Runtime
		// admission still uses the independently defined minimum versions.
		return nil
	}
	return records
}

func decodeVerified(raw []byte) (map[string][]Evidence, error) {
	invalid := errors.New("invalid_packaged_verification")
	if len(raw) == 0 || len(raw) > 65536 || !utf8.Valid(raw) {
		return nil, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	records := make(map[string][]Evidence)
	for decoder.More() {
		token, err := decoder.Token()
		runtime, ok := token.(string)
		if err != nil || !ok || (runtime != Claude && runtime != Codex) || records[runtime] != nil {
			return nil, invalid
		}
		entries, err := decodeEvidenceList(decoder)
		if err != nil {
			return nil, invalid
		}
		records[runtime] = entries
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || len(records) != 2 {
		return nil, invalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, invalid
	}
	return records, nil
}

func decodeEvidenceList(decoder *json.Decoder) ([]Evidence, error) {
	invalid := errors.New("invalid_packaged_verification")
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return nil, invalid
	}
	var entries []Evidence
	seen := make(map[string]bool)
	for decoder.More() {
		entry, err := decodeEvidence(decoder)
		identity := strings.Join([]string{entry.Version, entry.OS, entry.Arch, entry.AdapterRevision, entry.Profile}, "|")
		if err != nil || seen[identity] || len(entries) >= 256 {
			return nil, invalid
		}
		seen[identity] = true
		entries = append(entries, entry)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') || len(entries) == 0 {
		return nil, invalid
	}
	return entries, nil
}

func decodeEvidence(decoder *json.Decoder) (Evidence, error) {
	invalid := errors.New("invalid_packaged_verification")
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return Evidence{}, invalid
	}
	fields := make(map[string]string)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || (key != "version" && key != "reference" && key != "os" && key != "arch" && key != "adapter_revision" && key != "profile") {
			return Evidence{}, invalid
		}
		if _, exists := fields[key]; exists {
			return Evidence{}, invalid
		}
		token, err = decoder.Token()
		value, ok := token.(string)
		if err != nil || !ok {
			return Evidence{}, invalid
		}
		fields[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || len(fields) != 6 {
		return Evidence{}, invalid
	}
	entry := Evidence{Version: fields["version"], Reference: fields["reference"], OS: fields["os"], Arch: fields["arch"],
		AdapterRevision: fields["adapter_revision"], Profile: fields["profile"]}
	if _, err := Parse(entry.Version); err != nil {
		return Evidence{}, invalid
	}
	for _, value := range []string{entry.OS, entry.Arch, entry.AdapterRevision, entry.Profile} {
		if !evidenceToken(value) {
			return Evidence{}, invalid
		}
	}
	if strings.TrimSpace(entry.Reference) == "" || len(entry.Reference) > 2048 || strings.ContainsFunc(entry.Reference, unicode.IsControl) {
		return Evidence{}, invalid
	}
	return entry, nil
}

func evidenceToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, c := range value {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '/' || c == '-' || c == '_'
		if !valid {
			return false
		}
	}
	return true
}
