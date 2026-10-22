// Package codec implements bounded strict JSON and PeerTurn's integer-only JCS subset.
package codec

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/temirlankasen/peerturn/internal/fault"
)

const maxDepth = 64

// Validate rejects oversized, ambiguous, malformed or non-Unicode JSON.
func Validate(data []byte, max int) error {
	_, err := parse(data, max)
	return err
}

func parse(data []byte, max int) (any, error) {
	if max < 0 || len(data) > max {
		return nil, fault.New(fault.TooLarge)
	}
	if !utf8.Valid(data) || !validEscapes(data) {
		return nil, fault.New(fault.InvalidEncoding)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readValue(decoder, 0)
	if err != nil {
		return nil, fault.New(fault.InvalidEncoding)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fault.New(fault.InvalidEncoding)
	}
	return value, nil
}

func readValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > maxDepth {
		return nil, fault.New(fault.InvalidEncoding)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fault.New(fault.InvalidEncoding)
			}
			if _, exists := object[key]; exists {
				return nil, fault.New(fault.InvalidEncoding)
			}
			value, err := readValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := readValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return array, nil
	default:
		return nil, fault.New(fault.InvalidEncoding)
	}
}

// encoding/json replaces unpaired UTF-16 escapes; cryptographic inputs must reject them.
func validEscapes(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		unit, ok := codeUnit(data, i+1)
		if !ok {
			return false
		}
		i += 4
		if unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
		if unit < 0xd800 || unit > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, ok := codeUnit(data, i+3)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func codeUnit(data []byte, start int) (uint64, bool) {
	if start+4 > len(data) {
		return 0, false
	}
	value, err := strconv.ParseUint(string(data[start:start+4]), 16, 16)
	return value, err == nil
}
