package detector

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// ErrNoStack is returned when no supported manifest is found. The CLI maps it
// to constants.ExitStackUnknown (US-3).
var ErrNoStack = errors.New("could not detect a supported stack in this repo")

// maxManifestBytes caps how much of any single manifest is read. A real
// package.json or go.mod is kilobytes; anything near this is not a manifest a
// human wrote, and reading it whole would let a hostile repo exhaust memory
// (Phase 9.1: the repo is untrusted input).
const maxManifestBytes = 8 << 20

// Result is what the detector learned about a repository.
type Result struct {
	// Stack is one of constants.SupportedStacks.
	Stack string

	// Manifest is the file that decided the stack, relative to the root.
	Manifest string

	// Others lists every other supported stack that also matched, in
	// priority order. Non-empty means the repo is polyglot and the choice
	// was made by priority alone (Phase 1.4).
	Others []string

	// Exactly one of these is set, matching Stack.
	Node   *NodeInfo
	Go     *GoInfo
	Python *PythonInfo
	Java   *JavaInfo
}

// Ambiguous reports whether more than one stack matched.
func (r *Result) Ambiguous() bool { return len(r.Others) > 0 }

// probe checks one stack. It returns (nil, nil) when the stack's manifest is
// absent, and an error only when the manifest exists but cannot be used.
type probe func(root string) (*Result, error)

// probes is keyed by stack id; order comes from constants.SupportedStacks so
// the documented priority and the implemented one cannot drift apart.
var probes = map[string]probe{
	constants.StackNode:   detectNode,
	constants.StackGo:     detectGo,
	constants.StackPython: detectPython,
	constants.StackJava:   detectJava,
}

// Detect identifies the stack of the repository at root (FR-1).
//
// Every supported stack is probed so a polyglot repo can be reported as such;
// the first match in constants.SupportedStacks wins. A manifest that exists but
// is malformed is an error rather than a skip: silently falling through to the
// next stack would be exactly the guess Phase 0.10 warns against.
func Detect(root string) (*Result, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("detector: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("detector: %s is not a directory", root)
	}

	var chosen *Result
	var others []string
	for _, stack := range constants.SupportedStacks {
		p, ok := probes[stack]
		if !ok {
			return nil, fmt.Errorf("detector: no probe registered for stack %q", stack)
		}
		res, err := p(root)
		if err != nil {
			return nil, err
		}
		if res == nil {
			continue
		}
		if chosen == nil {
			chosen = res
		} else {
			others = append(others, stack)
		}
	}
	if chosen == nil {
		return nil, ErrNoStack
	}
	chosen.Others = others
	return chosen, nil
}

// readManifest reads a file under root, refusing anything oversized or not a
// regular file. A missing file returns (nil, nil).
func readManifest(root, name string) ([]byte, error) {
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("detector: %w", err)
	}
	// A symlinked manifest could point outside the repository; following it
	// would make the analysis depend on files the user did not point us at.
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("detector: %s is not a regular file", name)
	}
	if info.Size() > maxManifestBytes {
		return nil, fmt.Errorf("detector: %s is larger than %d MiB; refusing to read it", name, maxManifestBytes>>20)
	}
	b, err := os.ReadFile(path) //nolint:gosec // path is root-joined and checked above
	if err != nil {
		return nil, fmt.Errorf("detector: %w", err)
	}
	return b, nil
}

// exists reports whether a regular file or directory named name exists under
// root.
func exists(root, name string) bool {
	_, err := os.Lstat(filepath.Join(root, name))
	return err == nil
}

// isDir reports whether name under root is a directory.
func isDir(root, name string) bool {
	info, err := os.Lstat(filepath.Join(root, name))
	return err == nil && info.IsDir()
}
