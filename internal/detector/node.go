package detector

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// Node package managers.
const (
	PackageManagerNPM  = "npm"
	PackageManagerYarn = "yarn"
	PackageManagerPNPM = "pnpm"
)

// NodeInfo describes a Node.js repository.
type NodeInfo struct {
	// Name is package.json's name, possibly empty.
	Name string

	// PackageManager is npm, yarn or pnpm, decided by lockfile first and
	// the packageManager field second.
	PackageManager string

	// YarnBerry is true for Yarn 2+ (different install flags from v1).
	YarnBerry bool

	// Lockfile is the lockfile present at the root, or "" when there is
	// none. Without one, installs cannot be reproducible and the generated
	// Dockerfile says so.
	Lockfile string

	// Scripts are the package.json scripts.
	Scripts map[string]string

	// Main is package.json's main field.
	Main string

	// EngineMajor is the Node major version requested by engines.node, or
	// 0 when unspecified or unparseable.
	EngineMajor int

	// Dependencies and DevDependencies are the direct dependency names.
	Dependencies    []string
	DevDependencies []string

	// LockPackages and LockDevPackages count installed packages recorded in
	// package-lock.json (v2+), total and dev-only. Zero when unknown. Used by
	// the size estimate, never to decide what to generate.
	LockPackages    int
	LockDevPackages int

	// NativeModules lists packages that run an install script (node-gyp and
	// friends). Their presence means the install stage needs a compiler and
	// the runtime must match the libc they were built against.
	NativeModules []string

	// Workspaces is true when package.json declares workspaces — a monorepo,
	// which v1 does not support (Phase 1.4).
	Workspaces bool
}

// HasScript reports whether a non-empty script named name exists.
func (n *NodeInfo) HasScript(name string) bool {
	return strings.TrimSpace(n.Scripts[name]) != ""
}

type packageJSON struct {
	Name            string            `json:"name"`
	Main            string            `json:"main"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Engines         map[string]string `json:"engines"`
	PackageManager  string            `json:"packageManager"`
	Workspaces      json.RawMessage   `json:"workspaces"`
}

type packageLock struct {
	LockfileVersion int `json:"lockfileVersion"`
	Packages        map[string]struct {
		Dev              bool `json:"dev"`
		DevOptional      bool `json:"devOptional"`
		HasInstallScript bool `json:"hasInstallScript"`
	} `json:"packages"`
}

func detectNode(root string) (*Result, error) {
	raw, err := readManifest(root, constants.ManifestPackageJSON)
	if err != nil || raw == nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, fmt.Errorf("detector: %s is not valid JSON: %w", constants.ManifestPackageJSON, err)
	}

	info := &NodeInfo{
		Name:            pkg.Name,
		Main:            pkg.Main,
		Scripts:         pkg.Scripts,
		Dependencies:    sortedKeys(pkg.Dependencies),
		DevDependencies: sortedKeys(pkg.DevDependencies),
		EngineMajor:     parseNodeEngine(pkg.Engines["node"]),
		Workspaces:      len(pkg.Workspaces) > 0 && string(pkg.Workspaces) != "null",
	}
	if info.Scripts == nil {
		info.Scripts = map[string]string{}
	}

	switch {
	case exists(root, "pnpm-lock.yaml"):
		info.PackageManager, info.Lockfile = PackageManagerPNPM, "pnpm-lock.yaml"
	case exists(root, "yarn.lock"):
		info.PackageManager, info.Lockfile = PackageManagerYarn, "yarn.lock"
	case exists(root, "package-lock.json"):
		info.PackageManager, info.Lockfile = PackageManagerNPM, "package-lock.json"
	case exists(root, "npm-shrinkwrap.json"):
		info.PackageManager, info.Lockfile = PackageManagerNPM, "npm-shrinkwrap.json"
	default:
		info.PackageManager = PackageManagerNPM
		pmName, _, _ := strings.Cut(pkg.PackageManager, "@")
		switch pmName {
		case PackageManagerYarn, PackageManagerPNPM:
			info.PackageManager = pmName
		}
	}
	if info.PackageManager == PackageManagerYarn {
		info.YarnBerry = exists(root, ".yarnrc.yml") || strings.HasPrefix(pkg.PackageManager, "yarn@2") ||
			strings.HasPrefix(pkg.PackageManager, "yarn@3") || strings.HasPrefix(pkg.PackageManager, "yarn@4")
	}

	if info.Lockfile == "package-lock.json" || info.Lockfile == "npm-shrinkwrap.json" {
		if err := readNPMLock(root, info.Lockfile, info); err != nil {
			return nil, err
		}
	}

	return &Result{Stack: constants.StackNode, Manifest: constants.ManifestPackageJSON, Node: info}, nil
}

// readNPMLock fills the lockfile-derived counts. A lockfile v1 (npm 6) has no
// "packages" map; counts then stay zero and the estimate falls back to the
// direct dependency count.
func readNPMLock(root, name string, info *NodeInfo) error {
	raw, err := readManifest(root, name)
	if err != nil || raw == nil {
		return err
	}
	var lock packageLock
	if err := json.Unmarshal(raw, &lock); err != nil {
		return fmt.Errorf("detector: %s is not valid JSON: %w", name, err)
	}
	for path, p := range lock.Packages {
		if path == "" { // the root project itself
			continue
		}
		info.LockPackages++
		if p.Dev || p.DevOptional {
			info.LockDevPackages++
		}
		if p.HasInstallScript && !p.Dev {
			info.NativeModules = append(info.NativeModules, strings.TrimPrefix(path[strings.LastIndex(path, "node_modules/")+len("node_modules/"):], "/"))
		}
	}
	sort.Strings(info.NativeModules)
	return nil
}

var engineMajorRe = regexp.MustCompile(`(\d+)`)

// parseNodeEngine pulls a major version out of an engines.node range. It takes
// the first number: ">=18", "^20.11", "20.x" and "18 || 20" all resolve to the
// lowest major the author named, which is the one they have tested against.
func parseNodeEngine(spec string) int {
	m := engineMajorRe.FindStringSubmatch(spec)
	if m == nil {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(m[1], "%d", &n); err != nil || n < 10 || n > 99 {
		return 0
	}
	return n
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
