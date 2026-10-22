package codec

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/temirlankasen/peerturn/internal/fault"
)

const maxSafeInteger int64 = 9007199254740991

// Canonical emits the RFC8785 subset of null, booleans, Unicode strings, containers
// and safe integers. Floats and non-integer number spellings are rejected.
func Canonical(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil || !validGoValue(reflect.ValueOf(value), 0) {
		return nil, fault.New(fault.InvalidEncoding)
	}
	parsed, err := parse(data, len(data))
	if err != nil {
		return nil, err
	}
	result, err := appendCanonical(nil, parsed)
	if err != nil {
		return nil, fault.New(fault.InvalidEncoding)
	}
	return result, nil
}

func validGoValue(value reflect.Value, depth int) bool {
	if depth > maxDepth {
		return false
	}
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return false
	case reflect.String:
		return utf8.ValidString(value.String())
	case reflect.Pointer, reflect.Interface:
		return value.IsNil() || validGoValue(value.Elem(), depth+1)
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return false
		}
		for _, key := range value.MapKeys() {
			if !utf8.ValidString(key.String()) || !validGoValue(value.MapIndex(key), depth+1) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if !validGoValue(value.Index(i), depth+1) {
				return false
			}
		}
	case reflect.Struct:
		for field := range value.Type().Fields() {
			tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if field.IsExported() && tag != "-" && !validGoValue(value.FieldByIndex(field.Index), depth+1) {
				return false
			}
		}
	case reflect.Invalid, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return false
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32,
		reflect.Uint64, reflect.Uintptr:
		return true
	}
	return true
}

func appendCanonical(out []byte, value any) ([]byte, error) {
	switch value := value.(type) {
	case nil:
		return append(out, "null"...), nil
	case bool:
		return strconv.AppendBool(out, value), nil
	case string:
		return appendString(out, value), nil
	case json.Number:
		if strings.ContainsAny(string(value), ".eE") || value == "-0" {
			return nil, fault.New(fault.InvalidEncoding)
		}
		integer, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil || integer > maxSafeInteger || integer < -maxSafeInteger {
			return nil, fault.New(fault.InvalidEncoding)
		}
		return strconv.AppendInt(out, integer, 10), nil
	case []any:
		out = append(out, '[')
		for i, child := range value {
			if i > 0 {
				out = append(out, ',')
			}
			var err error
			out, err = appendCanonical(out, child)
			if err != nil {
				return nil, err
			}
		}
		return append(out, ']'), nil
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		slices.SortFunc(keys, func(a, b string) int { return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b))) })
		out = append(out, '{')
		for i, key := range keys {
			if i > 0 {
				out = append(out, ',')
			}
			out = appendString(out, key)
			out = append(out, ':')
			var err error
			out, err = appendCanonical(out, value[key])
			if err != nil {
				return nil, err
			}
		}
		return append(out, '}'), nil
	default:
		return nil, fault.New(fault.InvalidEncoding)
	}
}

func appendString(out []byte, value string) []byte {
	const hex = "0123456789abcdef"
	out = append(out, '"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			out = append(out, '\\', byte(r))
		case '\b':
			out = append(out, `\b`...)
		case '\t':
			out = append(out, `\t`...)
		case '\n':
			out = append(out, `\n`...)
		case '\f':
			out = append(out, `\f`...)
		case '\r':
			out = append(out, `\r`...)
		default:
			if r < 0x20 {
				out = append(out, '\\', 'u', '0', '0', hex[r>>4], hex[r&15])
			} else {
				out = utf8.AppendRune(out, r)
			}
		}
	}
	return append(out, '"')
}
