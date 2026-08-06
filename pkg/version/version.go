// Package version exposes build metadata injected at link time.
//
// Values are set by the Makefile via -ldflags -X. They stay at their
// placeholder values for `go run` and `go test`, which is intentional: no
// generated file is committed to the tree.
package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the semantic version of the build (Phase 11.2: vMAJOR.MINOR.PATCH).
	Version = "dev"

	// Commit is the short git SHA the build came from.
	Commit = "unknown"

	// BuildDate is the commit date in RFC3339 form.
	BuildDate = "unknown"
)

// String returns a single-line, human-readable build identifier. Used by the
// CLI's --version flag and reported once at API server startup.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s, %s/%s, %s)",
		Version, Commit, BuildDate, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
