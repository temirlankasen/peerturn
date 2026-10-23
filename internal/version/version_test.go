package version_test

import (
	"testing"

	"github.com/temirlankasen/peerturn/internal/version"
)

func TestString(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		date    string
		want    string
	}{
		{
			name: "development", version: "dev", commit: "unknown", date: "unknown",
			want: "peerturn dev (commit unknown, built unknown)",
		},
		{
			name: "release", version: "0.1.0", commit: "abcdef123456", date: "2026-10-21T12:00:00Z",
			want: "peerturn 0.1.0 (commit abcdef123456, built 2026-10-21T12:00:00Z)",
		},
		{
			name: "preview", version: "0.1.1-preview.1", commit: "0123456", date: "2026-10-21T00:00:00Z",
			want: "peerturn 0.1.1-preview.1 (commit 0123456, built 2026-10-21T00:00:00Z)",
		},
	}

	// Linker metadata is process-global, so these cases must run sequentially.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldVersion, oldCommit, oldDate := version.Version, version.Commit, version.Date
			t.Cleanup(func() {
				version.Version, version.Commit, version.Date = oldVersion, oldCommit, oldDate
			})
			version.Version, version.Commit, version.Date = tt.version, tt.commit, tt.date

			if got := version.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
