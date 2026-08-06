// Package constants holds values shared across more than one package.
//
// Anything used by exactly one package belongs in that package, not here — a
// grab-bag constants package is how import cycles and dead identifiers start.
package constants

// Product identity. Used in log fields, User-Agent strings and CLI output.
const (
	// AppName is the CLI binary name (Phase 1: `doiq .`).
	AppName = "doiq"

	// ServiceName identifies the backend API service in structured logs
	// (Phase 8.5).
	ServiceName = "deployiq-api"
)

// Stack identifiers produced by the detector (Phase 1 build) and stored in the
// optimizations collection as detectedStack (Phase 7.1). Values are part of the
// API contract and the on-disk report format — treat them as frozen.
const (
	StackNode    = "node"
	StackGo      = "go"
	StackPython  = "python"
	StackUnknown = ""
)

// SupportedStacks is the ordered detection priority list. Order matters: a repo
// carrying more than one manifest resolves to the first match and the ambiguity
// is reported to the user (Phase 1.4, polyglot edge case).
//
// Phase 1.5 business rule: a stack may only be listed here once it has fixture
// coverage under testdata/fixtures/. Documentation claims must match this slice.
var SupportedStacks = []string{StackNode, StackGo, StackPython}

// Manifest filenames the detector inspects (Phase 1, FR-1).
const (
	ManifestPackageJSON     = "package.json"
	ManifestGoMod           = "go.mod"
	ManifestRequirementsTxt = "requirements.txt"
	ManifestPyProjectTOML   = "pyproject.toml"
)

// Dockerfile filenames read and written by the CLI.
const (
	// DockerfileName is the file the parser reads from the repo root (FR-2).
	DockerfileName = "Dockerfile"

	// DockerfileOutputName is the only file the CLI ever writes, and only
	// under --write or an interactive confirmation (FR-5, FR-6).
	DockerfileOutputName = "Dockerfile.optimized"
)

// API routing (Phase 4.4 naming convention: /api/v1/<plural resource>).
const (
	// APIPathPrefix is the versioned prefix every backend route lives under.
	// Versioned from day one so a breaking change never strands an old CLI
	// (Phase 5.2).
	APIPathPrefix = "/api/v1"

	// AdminPathPrefix covers loopback-only operational endpoints. Never
	// exposed on the public listener.
	AdminPathPrefix = "/internal"
)

// Exit codes. The CLI's contract with CI systems, so they are explicit rather
// than incidental (Phase 1.3, US-3 requires a non-zero exit on undetected stack).
const (
	ExitOK           = 0
	ExitFailure      = 1
	ExitUsage        = 2
	ExitStackUnknown = 3
)
