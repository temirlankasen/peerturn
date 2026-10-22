package codec

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/temirlankasen/peerturn/internal/fault"
)

type nested struct {
	Value string `json:"value"`
}

type schema struct {
	Name  string   `json:"name"`
	Item  nested   `json:"item"`
	List  []nested `json:"list"`
	Count int      `json:"count"`
}

func TestDecodeRejectsAmbiguousOrInvalidJSON(t *testing.T) {
	tests := []string{
		`{"name":"a","name":"b"}`, `{"name":"a","\u006eame":"b"}`,
		`{"item":{"value":"a","value":"b"}}`, `{"list":[{"value":"a","value":"b"}]}`,
		`{"unknown":1}`, `{"Name":"a"}`, `{"item":{"Value":"a"}}`,
		`{"list":[{"Value":"a"}]}`, `{"name":"a"} {}`, `{"name":"a"} false`,
		`{"name":"\ud800"}`, `{"name":"\udc00"}`, `{"name":"\ud800\u0061"}`,
		`{"name":"\ud800\\udc00"}`, `{"count":1.0}`, `{"count":"1"}`, `{"count":null}`,
		`{"name":null}`, `{"name":1}`, `{"name":"` + string([]byte{0xff}) + `"}`, ``,
		strings.Repeat(`[`, 65) + strings.Repeat(`]`, 65),
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			var out schema
			if err := Decode([]byte(input), 4096, &out); !fault.IsCode(err, fault.InvalidEncoding) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestDecodeBoundsAndSuccess(t *testing.T) {
	input := []byte(`{"name":"😀","item":{"value":"ok"},"list":[{"value":"yes"}],"count":2}`)
	var out schema
	if err := Decode(input, len(input), &out); err != nil || out.Name != "😀" || out.Count != 2 {
		t.Fatalf("decode: %#v %v", out, err)
	}
	if err := Decode(input, len(input)-1, &out); !fault.IsCode(err, fault.TooLarge) {
		t.Fatalf("size: %v", err)
	}
	if err := Decode(input, 0, &out); !fault.IsCode(err, fault.TooLarge) {
		t.Fatalf("zero size: %v", err)
	}
	if err := Decode(input, 4096, schema{}); !fault.IsCode(err, fault.InvalidEncoding) {
		t.Fatalf("non-pointer: %v", err)
	}
	if err := Validate([]byte(`{"x":[{"z":1,"z":2}]}`), 4096); err == nil {
		t.Fatal("nested duplicate accepted")
	}
}

func FuzzDecode(f *testing.F) {
	for _, seed := range []string{`{}`, `{"name":"value"}`, `{"name":"a","name":"b"}`, `{"name":"\ud800"}`, `[0]`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		var out schema
		if err := Decode(input, 4096, &out); err != nil {
			return
		}
		if !json.Valid(input) || len(input) > 4096 {
			t.Fatal("accepted invalid or oversized JSON")
		}
		first, err := Canonical(out)
		if err != nil {
			t.Fatal(err)
		}
		var again schema
		if decodeErr := Decode(first, 4096, &again); decodeErr != nil || !reflect.DeepEqual(out, again) {
			t.Fatalf("roundtrip: %v", decodeErr)
		}
		second, err := Canonical(again)
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("canonical instability: %v", err)
		}
	})
}
