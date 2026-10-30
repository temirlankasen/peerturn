package relay

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/temirlankasen/peerturn/internal/codec"
	"github.com/temirlankasen/peerturn/internal/relay/config"
)

// Model the old strict response shape without changing the relay client.
type selfServiceLegacyDiscovery struct {
	WireVersions []int           `json:"wire_versions"`
	RelayKey     string          `json:"relay_key"`
	BackupEpoch  int64           `json:"backup_epoch"`
	Limits       json.RawMessage `json:"limits"`
}

func TestSelfServiceHTTPDiscoveryModeIsOptionalCompatibilityHint(t *testing.T) {
	for _, mode := range []config.AdmissionMode{config.AdmissionClosed, config.AdmissionSelfService} {
		t.Run(string(mode), func(t *testing.T) {
			f := newSelfServiceHTTPFixture(t, func(c *config.Config) { c.AdmissionMode = mode })
			body := f.request(t, "GET", "/.well-known/peerturn", nil, nil, 200)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			var legacy selfServiceLegacyDiscovery
			legacyErr := codec.Decode(body, 4096, &legacy)
			if mode == config.AdmissionClosed {
				if _, present := fields["admission_mode"]; present {
					t.Fatal("closed discovery added a field rejected by existing strict clients")
				}
				if legacyErr != nil || legacy.BackupEpoch <= 0 || legacy.RelayKey == "" || len(legacy.WireVersions) != 1 || legacy.WireVersions[0] != 1 {
					t.Fatalf("closed discovery broke the old response contract: %s, %v", body, legacyErr)
				}
				f.publish(t, f.publisher, f.invitation(t, time.Minute), 403)
			} else {
				if string(fields["admission_mode"]) != `"self_service"` {
					t.Fatalf("opted-in discovery omitted the exact compatibility hint: %s", body)
				}
				if legacyErr == nil {
					t.Fatal("fixture no longer demonstrates that open-mode discovery needs upgraded clients")
				}
			}
			f.wantState(t, selfServiceHTTPState{})
		})
	}
}
