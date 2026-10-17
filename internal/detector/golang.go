package detector

import (
	"bufio"
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// GoInfo describes a Go module.
type GoInfo struct {
	// Module is the module path from go.mod.
	Module string

	// GoVersion is the major.minor toolchain the module needs, e.g. "1.24",
	// taken from the toolchain directive when present, else the go directive.
	GoVersion string

	// HasGoSum reports whether go.sum exists. A module without dependencies
	// legitimately has none, and COPY of a missing file fails the build.
	HasGoSum bool

	// Requires counts require entries (direct and indirect).
	Requires int

	// HasVendor reports a vendor/ directory; builds then use it instead of
	// downloading modules.
	HasVendor bool

	// MainPackages lists every directory (relative, slash-separated, "."
	// for the root) holding a package main, within the scan depth.
	MainPackages []string

	// UsesCgo reports an `import "C"` anywhere scanned. A cgo binary cannot
	// run on a static distroless image.
	UsesCgo bool

	// Embeds reports any //go:embed directive: assets are compiled in, so
	// they need not be copied into the runtime image.
	Embeds bool
}

// MainPackage returns the package to build: the only main package, or the root
// one when several exist, or the one under cmd/ named after the module. It
// returns "" when no single choice is defensible.
func (g *GoInfo) MainPackage() string {
	switch len(g.MainPackages) {
	case 0:
		return ""
	case 1:
		return g.MainPackages[0]
	}
	for _, p := range g.MainPackages {
		if p == "." {
			return p
		}
	}
	base := g.Module
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	for _, p := range g.MainPackages {
		if p == "cmd/"+base {
			return p
		}
	}
	return ""
}

var goVersionRe = regexp.MustCompile(`^(?:go)?(\d+)\.(\d+)`)

func detectGo(root string) (*Result, error) {
	raw, err := readManifest(root, constants.ManifestGoMod)
	if err != nil || raw == nil {
		return nil, err
	}
	info, err := parseGoMod(raw)
	if err != nil {
		return nil, err
	}
	info.HasGoSum = exists(root, "go.sum")
	info.HasVendor = isDir(root, "vendor")

	if err := scanGoPackages(root, info); err != nil {
		return nil, err
	}
	return &Result{Stack: constants.StackGo, Manifest: constants.ManifestGoMod, Go: info}, nil
}

// parseGoMod reads the handful of directives the generator needs. Hand-rolled
// rather than golang.org/x/mod: four directives do not justify a dependency in
// a single static binary (Phase 0.7).
func parseGoMod(raw []byte) (*GoInfo, error) {
	info := &GoInfo{}
	var goDirective, toolchain string
	inRequire := false

	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64<<10), maxManifestBytes)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if inRequire {
			if line == ")" {
				inRequire = false
			} else {
				info.Requires++
			}
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "module":
			if len(fields) > 1 {
				info.Module = strings.Trim(fields[1], `"`)
			}
		case "go":
			if len(fields) > 1 {
				goDirective = fields[1]
			}
		case "toolchain":
			if len(fields) > 1 {
				toolchain = fields[1]
			}
		case "require":
			if len(fields) > 1 && fields[1] == "(" {
				inRequire = true
			} else if len(fields) > 2 {
				info.Requires++
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("detector: reading go.mod: %w", err)
	}
	if info.Module == "" {
		return nil, fmt.Errorf("detector: go.mod has no module directive")
	}

	// The toolchain directive, when present, is the version the author
	// actually builds with; the go directive is only the language floor.
	for _, v := range []string{toolchain, goDirective} {
		if m := goVersionRe.FindStringSubmatch(v); m != nil {
			info.GoVersion = m[1] + "." + m[2]
			break
		}
	}
	return info, nil
}

// skipDirs are never descended into when scanning a repository. vendor and
// testdata hold packages that are not this module's own binaries.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "testdata": true,
	".venv": true, "venv": true, "__pycache__": true, "dist": true, "bin": true,
}

// maxScanDepth bounds the package scan. Binaries conventionally live at the
// root or one or two levels down (cmd/<name>); a full walk of a large repo
// would cost the five-second budget for nothing (Phase 1.2).
const maxScanDepth = 3

// maxGoFiles caps how many files are parsed, for the same reason.
const maxGoFiles = 2000

func scanGoPackages(root string, info *GoInfo) error {
	mains := map[string]bool{}
	parsed := 0
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if rel != "." && strings.Count(rel, "/")+1 > maxScanDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		if parsed >= maxGoFiles {
			return filepath.SkipAll
		}
		parsed++

		// ImportsOnly plus comments is enough for package name, imports and
		// //go:embed directives, and far cheaper than a full parse.
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.ParseComments)
		if perr != nil {
			return nil // a file that does not parse is the compiler's problem
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == `"C"` {
				info.UsesCgo = true
			}
		}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.HasPrefix(c.Text, "//go:embed") {
					info.Embeds = true
				}
			}
		}
		if f.Name.Name == "main" {
			mains[filepath.ToSlash(filepath.Dir(rel))] = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("detector: scanning go packages: %w", err)
	}
	for p := range mains {
		info.MainPackages = append(info.MainPackages, p)
	}
	sort.Strings(info.MainPackages)
	return nil
}
