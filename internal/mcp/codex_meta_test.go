package mcp

import (
	"bytes"
	"strings"
	"testing"
)

func TestMCPToolMetadataHasSeparateBound(t *testing.T) {
	const limit = 64 * 1024
	base := `{"threadId":"` + codexThread + `","x-codex-turn-metadata":{"extra":""}}`
	for _, profile := range []string{"codex", "inherited", "informational"} {
		for _, variant := range []string{"large", "at_limit", "oversize", "duplicate", "nested_duplicate", "deep", "request_oversize"} {
			t.Run(profile+"/"+variant, func(t *testing.T) {
				meta := strings.Replace(base, `"extra":""`, `"extra":"`+strings.Repeat("x", 20*1024)+`"`, 1)
				switch variant {
				case "at_limit", "oversize", "request_oversize":
					size := limit
					switch variant {
					case "oversize":
						size++
					case "request_oversize":
						size = maxLine
					}
					meta = strings.Replace(base, `"extra":""`, `"extra":"`+strings.Repeat("x", size-len(base))+`"`, 1)
				case "duplicate":
					meta = strings.Replace(meta, `"threadId":`, `"threadId":"`+foreignThread+`","threadId":`, 1)
				case "nested_duplicate":
					meta = strings.Replace(meta, `"extra":`, `"extra":"duplicate","extra":`, 1)
				case "deep":
					meta = strings.Replace(base, `"extra":""`, `"extra":`+strings.Repeat("[", 65)+`0`+strings.Repeat("]", 65), 1)
				}
				caller := &recordingCaller{}
				resolved := 0
				options := Options{ResolveCodex: func(string) (Caller, error) {
					resolved++
					return caller, nil
				}}
				if profile != "codex" {
					options = Options{Session: codexThread, Caller: caller}
					if profile == "inherited" {
						options.CodexThread = codexThread
					}
				}
				var output bytes.Buffer
				if err := Serve(t.Context(), strings.NewReader(codexInit+codexStatus(meta)), &output, options); err != nil {
					t.Fatal(err)
				}
				accepted := variant == "large" || variant == "at_limit"
				statuses := 0
				for _, request := range caller.requests {
					if request.Method == "status" {
						statuses++
					}
				}
				failed := strings.Contains(output.String(), `"isError":true`) || strings.Contains(output.String(), `"error":`)
				if accepted && (statuses != 1 || failed) || !accepted && (statuses != 0 || !failed || resolved != 0) {
					t.Fatalf("metadata bound: accepted=%v statuses=%d resolved=%d response=%s", accepted, statuses, resolved, output.String())
				}
			})
		}
	}
}
