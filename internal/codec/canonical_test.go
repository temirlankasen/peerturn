package codec

import (
	"encoding/json"
	"math"
	"testing"
)

func TestCanonicalSubset(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"null", nil, `null`}, {"bool", true, `true`}, {"false", false, `false`},
		{"integer", int64(-9007199254740991), `-9007199254740991`},
		{"uint", uint64(9007199254740991), `9007199254740991`},
		{"number", json.Number("0"), `0`},
		{"string", "\"\\\b\t\n\f\r\x00\x01<>&/\u2028\u2029😀", "\"\\\"\\\\\\b\\t\\n\\f\\r\\u0000\\u0001<>&/\u2028\u2029😀\""},
		{"array", []any{true, nil, int(5), "a"}, `[true,null,5,"a"]`},
		{"recursive", map[string]any{"z": []any{map[string]any{"b": 1, "a": 2}}, "a": false}, `{"a":false,"z":[{"a":2,"b":1}]}`},
		{"struct", nested{Value: "ok"}, `{"value":"ok"}`},
		// RFC8785 §3.2.3 requires UTF-16 code-unit order, not UTF-8 byte order.
		{"utf16", map[string]any{"€": 5, "\r": 1, "דּ": 7, "1": 2, "😀": 6, "\u0080": 3, "ö": 4}, "{\"\\r\":1,\"1\":2,\"\u0080\":3,\"ö\":4,\"€\":5,\"😀\":6,\"דּ\":7}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Canonical(test.value)
			if err != nil || string(got) != test.want {
				t.Fatalf("got %s, %v; want %s", got, err, test.want)
			}
		})
	}
}

func TestCanonicalRejectsOutsideSubset(t *testing.T) {
	cycle := make(map[string]any)
	cycle["self"] = cycle
	tests := []any{
		float64(1), float32(1), math.NaN(), math.Inf(1),
		int64(9007199254740992), uint64(9007199254740992),
		json.Number("1.0"), json.Number("1e0"), json.Number("-0"),
		json.Number("9007199254740992"), json.Number("01"),
		string([]byte{0xff}), map[string]any{string([]byte{0xff}): 1},
		json.RawMessage(`{"a":1,"a":2}`), json.RawMessage(`"\ud800"`),
		json.RawMessage(`1e0`), json.RawMessage(`1.0`), make(chan int), cycle,
	}
	for _, value := range tests {
		if got, err := Canonical(value); err == nil {
			t.Fatalf("accepted %T: %s", value, got)
		}
	}
}

func FuzzCanonical(f *testing.F) {
	for _, seed := range []string{`{"b":2,"a":1}`, `[true,null,"😀"]`, `1.5`, `{"x":1,"x":2}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 4096 {
			return
		}
		first, err := Canonical(json.RawMessage(input))
		if err != nil {
			return
		}
		second, err := Canonical(json.RawMessage(first))
		if err != nil || string(first) != string(second) {
			t.Fatalf("not idempotent: %q %q %v", first, second, err)
		}
	})
}
