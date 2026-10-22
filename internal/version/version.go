// Package version exposes build metadata supplied by the release linker flags.
package version

import "fmt"

// Build metadata defaults allow a plain go build to identify a development binary.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns a human-readable version with its source and build timestamp.
func String() string {
	return fmt.Sprintf("peerturn %s (commit %s, built %s)", Version, Commit, Date)
}
