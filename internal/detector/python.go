package detector

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// Python dependency tools.
const (
	PythonToolPip    = "pip"
	PythonToolPoetry = "poetry"
)

// PythonInfo describes a Python repository.
type PythonInfo struct {
	// Tool is pip or poetry: how dependencies get installed.
	Tool string

	// RequirementsFile is "requirements.txt" when that drives the install.
	RequirementsFile string

	// Requirements are normalised distribution names (PEP 503), from
	// whichever manifest drives the install.
	Requirements []string

	// RequirementSpecs are the raw specifiers from [project].dependencies,
	// used verbatim when pyproject.toml without Poetry drives the install.
	RequirementSpecs []string

	// IncludedFiles are other requirement files referenced with -r or -c;
	// they must be copied alongside requirements.txt.
	IncludedFiles []string

	// LocalReferences is true when requirements reference the project
	// itself or a local path (-e ., ./pkg). The install then needs the whole
	// source tree, not just the manifest.
	LocalReferences bool

	// HasPoetryLock reports poetry.lock.
	HasPoetryLock bool

	// PoetryPackageMode is false when [tool.poetry] package-mode = false.
	PoetryPackageMode bool

	// Version is the major.minor interpreter the project asks for, or "".
	Version string

	// Entrypoints maps candidate entry files present at the root (app.py,
	// main.py, manage.py, ...) to what was found inside them.
	Entrypoints map[string]EntryHints

	// DjangoWSGI is the dotted WSGI module of a Django project
	// ("mysite.wsgi"), or "".
	DjangoWSGI string
}

// EntryHints records what a candidate entry file appears to contain. Read-only
// text inspection: nothing in the repository is imported or run (Phase 9.1).
type EntryHints struct {
	MainGuard bool // if __name__ == "__main__"
	FastAPI   bool // FastAPI( constructed
	Flask     bool // Flask( constructed

	// AppVar is the module-level variable the framework app is assigned to
	// ("app" in `app = Flask(__name__)`), or "" when not found.
	AppVar string
}

// Has reports whether the normalised distribution name is a dependency.
func (p *PythonInfo) Has(name string) bool {
	name = NormalizePythonName(name)
	for _, r := range p.Requirements {
		if r == name {
			return true
		}
	}
	return false
}

// pythonEntryCandidates are checked in order of how strongly they signal "this
// is how the app starts".
var pythonEntryCandidates = []string{"manage.py", "app.py", "main.py", "server.py", "run.py", "wsgi.py", "asgi.py"}

func detectPython(root string) (*Result, error) {
	req, err := readManifest(root, constants.ManifestRequirementsTxt)
	if err != nil {
		return nil, err
	}
	pyproject, err := readManifest(root, constants.ManifestPyProjectTOML)
	if err != nil {
		return nil, err
	}
	if req == nil && pyproject == nil {
		return nil, nil
	}

	info := &PythonInfo{Entrypoints: map[string]EntryHints{}, PoetryPackageMode: true}
	manifest := constants.ManifestRequirementsTxt
	poetryPython := ""

	var pp *pyprojectFile
	if pyproject != nil {
		pp = &pyprojectFile{}
		if _, err := toml.Decode(string(pyproject), pp); err != nil {
			return nil, fmt.Errorf("detector: %s is not valid TOML: %w", constants.ManifestPyProjectTOML, err)
		}
	}

	switch {
	case req != nil:
		// requirements.txt is the most explicit statement of what to
		// install, so it wins when both exist.
		info.Tool = PythonToolPip
		info.RequirementsFile = constants.ManifestRequirementsTxt
		parseRequirements(req, info)
	case pp.Tool.Poetry != nil:
		manifest = constants.ManifestPyProjectTOML
		info.Tool = PythonToolPoetry
		info.HasPoetryLock = exists(root, "poetry.lock")
		if pp.Tool.Poetry.PackageMode != nil {
			info.PoetryPackageMode = *pp.Tool.Poetry.PackageMode
		}
		for name, spec := range pp.Tool.Poetry.Dependencies {
			if strings.EqualFold(name, "python") {
				if s, ok := spec.(string); ok {
					poetryPython = s
				}
				continue
			}
			info.Requirements = append(info.Requirements, NormalizePythonName(name))
		}
		for _, d := range pp.Project.Dependencies {
			info.Requirements = append(info.Requirements, requirementName(d))
		}
	default:
		manifest = constants.ManifestPyProjectTOML
		info.Tool = PythonToolPip
		for _, d := range pp.Project.Dependencies {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			info.RequirementSpecs = append(info.RequirementSpecs, d)
			info.Requirements = append(info.Requirements, requirementName(d))
		}
	}
	info.Requirements = dedupe(info.Requirements)

	info.Version = pythonVersion(root, pp)
	if info.Version == "" {
		if m := pyVersionRe.FindStringSubmatch(poetryPython); m != nil {
			info.Version = "3." + m[1]
		}
	}
	scanPythonEntrypoints(root, info)

	return &Result{Stack: constants.StackPython, Manifest: manifest, Python: info}, nil
}

type pyprojectFile struct {
	Project struct {
		Dependencies   []string `toml:"dependencies"`
		RequiresPython string   `toml:"requires-python"`
	} `toml:"project"`
	Tool struct {
		Poetry *struct {
			Dependencies map[string]any `toml:"dependencies"`
			PackageMode  *bool          `toml:"package-mode"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

// parseRequirements extracts names, includes and local references from a
// requirements file. Unknown option lines (--index-url and friends) are left
// for pip; they do not change what the generator does.
func parseRequirements(raw []byte, info *PythonInfo) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64<<10), maxManifestBytes)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "-r ") || strings.HasPrefix(line, "-c ") ||
			strings.HasPrefix(line, "--requirement ") || strings.HasPrefix(line, "--constraint "):
			parts := strings.Fields(line)
			if len(parts) > 1 {
				info.IncludedFiles = append(info.IncludedFiles, filepath.ToSlash(parts[1]))
			}
		case strings.HasPrefix(line, "-e ") || strings.HasPrefix(line, "--editable "):
			info.LocalReferences = true
		case strings.HasPrefix(line, "-"):
			// pip option; nothing to learn.
		case line == "." || strings.HasPrefix(line, "./") || strings.HasPrefix(line, "../") ||
			strings.HasPrefix(line, "/") || strings.HasPrefix(line, "file:"):
			info.LocalReferences = true
		default:
			if n := requirementName(line); n != "" {
				info.Requirements = append(info.Requirements, n)
			}
		}
	}
}

var reqNameRe = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)`)

// requirementName extracts the distribution name from a PEP 508 specifier.
func requirementName(spec string) string {
	m := reqNameRe.FindStringSubmatch(spec)
	if m == nil {
		return ""
	}
	return NormalizePythonName(m[1])
}

var pyNormRe = regexp.MustCompile(`[-_.]+`)

// NormalizePythonName applies PEP 503 normalisation: lowercase, runs of -_. to -.
func NormalizePythonName(name string) string {
	return pyNormRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
}

var pyVersionRe = regexp.MustCompile(`3\.(\d+)`)

// pythonVersion resolves the interpreter version from, in order:
// .python-version, runtime.txt, then requires-python's lowest bound.
func pythonVersion(root string, pp *pyprojectFile) string {
	for _, name := range []string{".python-version", "runtime.txt"} {
		if raw, err := readManifest(root, name); err == nil && raw != nil {
			if m := pyVersionRe.FindStringSubmatch(string(raw)); m != nil {
				return "3." + m[1]
			}
		}
	}
	if pp != nil {
		if m := pyVersionRe.FindStringSubmatch(pp.Project.RequiresPython); m != nil {
			return "3." + m[1]
		}
	}
	return ""
}

var (
	mainGuardRe = regexp.MustCompile(`if\s+__name__\s*==\s*['"]__main__['"]`)
	fastAPIRe   = regexp.MustCompile(`\bFastAPI\s*\(`)
	flaskRe     = regexp.MustCompile(`\bFlask\s*\(`)
	appVarRe    = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*\w+\s*)?=\s*(?:fastapi\.|flask\.)?(?:FastAPI|Flask)\s*\(`)
)

// maxEntryBytes caps how much of an entry file is inspected.
const maxEntryBytes = 1 << 20

func scanPythonEntrypoints(root string, info *PythonInfo) {
	for _, name := range pythonEntryCandidates {
		raw, err := readManifest(root, name)
		if err != nil || raw == nil {
			continue
		}
		if len(raw) > maxEntryBytes {
			raw = raw[:maxEntryBytes]
		}
		hints := EntryHints{
			MainGuard: mainGuardRe.Match(raw),
			FastAPI:   fastAPIRe.Match(raw),
			Flask:     flaskRe.Match(raw),
		}
		if m := appVarRe.FindSubmatch(raw); m != nil {
			hints.AppVar = string(m[1])
		}
		info.Entrypoints[name] = hints
	}

	if _, ok := info.Entrypoints["manage.py"]; ok {
		entries, err := os.ReadDir(root)
		if err != nil {
			return
		}
		var found []string
		for _, e := range entries {
			if e.IsDir() && exists(root, filepath.Join(e.Name(), "wsgi.py")) && exists(root, filepath.Join(e.Name(), "settings.py")) {
				found = append(found, e.Name())
			}
		}
		sort.Strings(found)
		if len(found) == 1 {
			info.DjangoWSGI = found[0] + ".wsgi"
		}
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
