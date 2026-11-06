package update_test

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/update"
)

func TestNewer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		current, latest string
		want            bool
	}{
		{"minor bump", "0.1.9-preview", "0.2.0-preview", true},
		{"patch compared numerically", "0.1.9-preview", "0.1.10-preview", true},
		{"major bump", "0.9.9", "1.0.0", true},
		{"same", "0.1.9-preview", "0.1.9-preview", false},
		{"older latest", "0.2.0-preview", "0.1.9-preview", false},
		{"suffix ignored for ordering", "0.1.9", "0.1.9-preview", false},
		{"suffix ignored the other way", "0.1.9-preview", "0.1.9", false},
		{"suffix revision is not newer", "0.1.1-preview.1", "0.1.1-preview", false},
		{"v prefix accepted", "v0.1.9-preview", "v0.2.0-preview", true},
		{"development build", "dev", "0.2.0-preview", false},
		{"garbage latest", "0.1.9-preview", "garbage", false},
		{"empty latest", "0.1.9-preview", "", false},
		{"two components", "0.1.9-preview", "0.2", false},
		{"four components", "0.1.9-preview", "0.2.0.1", false},
		{"overflowing component", "0.1.9-preview", "99999999999999999999.0.0", false},
		{"leading zero", "0.1.9-preview", "0.02.0", false},
		{"build metadata", "0.1.9-preview", "0.2.0+build", false},
		{"empty suffix", "0.1.9-preview", "0.2.0-", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := update.Newer(tt.current, tt.latest); got != tt.want {
				t.Errorf("Newer(%q, %q) = %t, want %t", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestValidVersion(t *testing.T) {
	t.Parallel()
	for version, want := range map[string]bool{
		"0.1.9-preview": true, "0.2.0": true, "1.10.100-preview.2": true,
		"dev": false, "": false, "v0.2.0": false, "0.2": false, "0.2.0/../x": false, "0.2.0-pre view": false,
	} {
		if got := update.ValidVersion(version); got != want {
			t.Errorf("ValidVersion(%q) = %t, want %t", version, got, want)
		}
	}
}
