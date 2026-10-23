package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Only the protocol request is copied from macos-live-2; no environment or authority.
func claudeInitializeFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/claude-2.1.306-initialize.json")
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err = json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func fixtureObject(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	child, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("fixture %s is not an object", key)
	}
	return child
}

func TestInitializeCapturedClaudeAndInformationalExtensions(t *testing.T) {
	for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"} {
		for _, extended := range []bool{false, true} {
			t.Run(version+"/"+map[bool]string{false: "captured", true: "extended"}[extended], func(t *testing.T) {
				request := claudeInitializeFixture(t)
				params := fixtureObject(t, request, "params")
				params["protocolVersion"] = version
				if extended {
					info := fixtureObject(t, params, "clientInfo")
					info["icons"] = []any{map[string]any{"src": "https://example.invalid/icon.png", "mimeType": "image/png", "sizes": []string{"48x48"}, "theme": "dark", "future": true}}
					info["future"] = map[string]any{"session": "never-authority"}
					capabilities := fixtureObject(t, params, "capabilities")
					capabilities["future"] = map[string]any{"grant": "never-authority"}
					fixtureObject(t, capabilities, "roots")["future"] = true
					params["_meta"] = map[string]any{"future": "never-authority"}
				}
				raw, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				caller := &recordingCaller{}
				var output bytes.Buffer
				input := string(raw) + "\n" + `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
				if err = Serve(t.Context(), strings.NewReader(input), &output, Options{Session: "chosen", Caller: caller}); err != nil {
					t.Fatal(err)
				}
				var initialized struct {
					Error  *rpcError
					Result struct{ ProtocolVersion string }
				}
				decoder := json.NewDecoder(&output)
				if err = decoder.Decode(&initialized); err != nil || initialized.Error != nil || initialized.Result.ProtocolVersion != version {
					t.Fatalf("initialize: error=%v response=%+v", err, initialized)
				}
				var listed struct {
					Error  *rpcError
					Result struct{ Tools []struct{ Name string } }
				}
				if err = decoder.Decode(&listed); err != nil || listed.Error != nil || len(listed.Result.Tools) != 7 {
					t.Fatalf("tools unavailable: %v %+v", err, listed)
				}
				if len(caller.requests) != 2 || caller.requests[0].Session != "chosen" || caller.requests[1].Method != "bye" {
					t.Fatal("informational metadata changed authority")
				}
			})
		}
	}
}

func TestInitializeRejectsInvalidMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, object, key string
		value             any
	}{
		{"description-type", "clientInfo", "description", 1},
		{"description-size", "clientInfo", "description", strings.Repeat("x", 4097)},
		{"website-type", "clientInfo", "websiteUrl", nil},
		{"website-size", "clientInfo", "websiteUrl", strings.Repeat("x", 2049)},
		{"icons-type", "clientInfo", "icons", "invalid"},
		{"icons-null", "clientInfo", "icons", nil},
		{"icon-src-type", "clientInfo", "icons", []any{map[string]any{"src": 3}}},
		{"icon-sizes-type", "clientInfo", "icons", []any{map[string]any{"src": "data:image/png;base64,AA==", "sizes": "48x48"}}},
		{"icon-theme-type", "clientInfo", "icons", []any{map[string]any{"src": "https://example.invalid/i", "theme": true}}},
		{"name-size", "clientInfo", "name", strings.Repeat("x", 257)},
		{"name-required", "clientInfo", "name", ""},
		{"version-required", "clientInfo", "version", ""},
		{"capability-type", "capabilities", "roots", true},
		{"capability-flag-type", "capabilities", "roots", map[string]any{"listChanged": "yes"}},
		{"capability-form-type", "capabilities", "elicitation", map[string]any{"form": true}},
		{"capability-form-null", "capabilities", "elicitation", map[string]any{"form": nil}},
		{"capability-tasks-type", "capabilities", "tasks", map[string]any{"list": false}},
		{"client-type", "", "clientInfo", []any{}},
		{"capabilities-type", "", "capabilities", true},
		{"capabilities-null", "", "capabilities", nil},
		{"envelope-strict", "", "session", "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := fixtureObject(t, claudeInitializeFixture(t), "params")
			target := params
			if tc.object != "" {
				target = fixtureObject(t, params, tc.object)
			}
			target[tc.key] = tc.value
			raw, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = initialize(raw); err == nil {
				t.Fatal("invalid initialize metadata accepted")
			}
		})
	}
	for _, raw := range []string{
		`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"a","name":"b","version":"1"}}`,
		`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"a","version":"1","future":{"x":1,"x":2}}}`,
		`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"a","version":"1","future":"` + strings.Repeat("x", maxLine) + `"}}`,
	} {
		if _, err := initialize(json.RawMessage(raw)); err == nil {
			t.Fatal("duplicate or oversized informational extension accepted")
		}
	}
}
