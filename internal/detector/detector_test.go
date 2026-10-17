package detector

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// repo writes files (path -> content) into a fresh temp dir.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectNoStack(t *testing.T) {
	_, err := Detect(repo(t, map[string]string{"README.md": "hi"}))
	if !errors.Is(err, ErrNoStack) {
		t.Fatalf("want ErrNoStack, got %v", err)
	}
	// The exact user-facing sentence US-3 specifies.
	if !strings.EqualFold(ErrNoStack.Error(), "could not detect a supported stack in this repo") {
		t.Fatalf("message drifted: %q", ErrNoStack)
	}
}

func TestDetectMissingPath(t *testing.T) {
	if _, err := Detect(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("want error for missing path")
	}
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o644)
	if _, err := Detect(f); err == nil {
		t.Fatal("want error for a file path")
	}
}

func TestDetectPriorityAndAmbiguity(t *testing.T) {
	dir := repo(t, map[string]string{
		"package.json":     `{"name":"x"}`,
		"go.mod":           "module example.com/x\n\ngo 1.22\n",
		"requirements.txt": "flask\n",
		"main.go":          "package main\nfunc main(){}\n",
	})
	res, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stack != constants.StackNode {
		t.Fatalf("priority: want node first, got %s", res.Stack)
	}
	if !res.Ambiguous() || !reflect.DeepEqual(res.Others, []string{constants.StackGo, constants.StackPython}) {
		t.Fatalf("others = %v", res.Others)
	}
}

func TestDetectMalformedManifestIsAnError(t *testing.T) {
	// Falling through to the next stack would be a guess (Phase 0.10).
	_, err := Detect(repo(t, map[string]string{"package.json": "{not json", "go.mod": "module x\n"}))
	if err == nil || errors.Is(err, ErrNoStack) {
		t.Fatalf("want a parse error, got %v", err)
	}
}

func TestDetectNode(t *testing.T) {
	dir := repo(t, map[string]string{
		"package.json": `{
			"name": "svc", "main": "index.js",
			"scripts": {"start": "node index.js", "build": "tsc"},
			"dependencies": {"express": "^4", "bcrypt": "^5"},
			"devDependencies": {"typescript": "^5"},
			"engines": {"node": ">=20.11"}
		}`,
		"package-lock.json": `{"lockfileVersion": 3, "packages": {
			"": {},
			"node_modules/express": {},
			"node_modules/bcrypt": {"hasInstallScript": true},
			"node_modules/typescript": {"dev": true},
			"node_modules/a/node_modules/b": {"devOptional": true}
		}}`,
	})
	res, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := res.Node
	if n.PackageManager != PackageManagerNPM || n.Lockfile != "package-lock.json" {
		t.Fatalf("pm=%s lock=%s", n.PackageManager, n.Lockfile)
	}
	if n.EngineMajor != 20 || !n.HasScript("build") || n.HasScript("test") {
		t.Fatalf("engine=%d scripts=%v", n.EngineMajor, n.Scripts)
	}
	if n.LockPackages != 4 || n.LockDevPackages != 2 {
		t.Fatalf("lock counts %d/%d", n.LockPackages, n.LockDevPackages)
	}
	if !reflect.DeepEqual(n.NativeModules, []string{"bcrypt"}) {
		t.Fatalf("native = %v", n.NativeModules)
	}
	if !reflect.DeepEqual(n.Dependencies, []string{"bcrypt", "express"}) {
		t.Fatalf("deps = %v", n.Dependencies)
	}
}

func TestDetectNodePackageManagers(t *testing.T) {
	cases := []struct {
		files map[string]string
		pm    string
		berry bool
	}{
		{map[string]string{"package.json": `{}`, "yarn.lock": ""}, PackageManagerYarn, false},
		{map[string]string{"package.json": `{}`, "yarn.lock": "", ".yarnrc.yml": ""}, PackageManagerYarn, true},
		{map[string]string{"package.json": `{"packageManager":"yarn@4.1.0"}`}, PackageManagerYarn, true},
		{map[string]string{"package.json": `{}`, "pnpm-lock.yaml": ""}, PackageManagerPNPM, false},
		{map[string]string{"package.json": `{"packageManager":"pnpm@9.0.0"}`}, PackageManagerPNPM, false},
		{map[string]string{"package.json": `{}`}, PackageManagerNPM, false},
	}
	for i, c := range cases {
		res, err := Detect(repo(t, c.files))
		if err != nil {
			t.Fatal(err)
		}
		if res.Node.PackageManager != c.pm || res.Node.YarnBerry != c.berry {
			t.Errorf("case %d: pm=%s berry=%v", i, res.Node.PackageManager, res.Node.YarnBerry)
		}
	}
}

func TestDetectNodeWorkspaces(t *testing.T) {
	res, err := Detect(repo(t, map[string]string{"package.json": `{"workspaces":["packages/*"]}`}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Node.Workspaces {
		t.Fatal("workspaces not detected")
	}
}

func TestParseNodeEngine(t *testing.T) {
	for spec, want := range map[string]int{">=18": 18, "^20.11.0": 20, "20.x": 20, "18 || 20": 18, "": 0, "latest": 0, "*": 0, "4": 0} {
		if got := parseNodeEngine(spec); got != want {
			t.Errorf("%q: got %d want %d", spec, got, want)
		}
	}
}

func TestDetectGo(t *testing.T) {
	dir := repo(t, map[string]string{
		"go.mod": `module github.com/acme/api // comment

go 1.22.3

toolchain go1.23.1

require (
	github.com/a/b v1.0.0
	github.com/c/d v1.0.0 // indirect
)

require github.com/e/f v1.0.0
`,
		"go.sum":                   "",
		"cmd/api/main.go":          "package main\nimport \"embed\"\n//go:embed x\nvar _ embed.FS\nfunc main(){}\n",
		"cmd/worker/main.go":       "package main\nfunc main(){}\n",
		"internal/x/x.go":          "package x\n",
		"internal/x/x_test.go":     "package main\n",
		"vendor/z/main.go":         "package main\n",
		"testdata/fixture/main.go": "package main\n",
	})
	res, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := res.Go
	if g.Module != "github.com/acme/api" || g.GoVersion != "1.23" || g.Requires != 3 || !g.HasGoSum || !g.HasVendor {
		t.Fatalf("%+v", g)
	}
	if !reflect.DeepEqual(g.MainPackages, []string{"cmd/api", "cmd/worker"}) {
		t.Fatalf("mains = %v", g.MainPackages)
	}
	if g.MainPackage() != "cmd/api" {
		t.Fatalf("main package = %q (should pick the one named after the module)", g.MainPackage())
	}
	if !g.Embeds || g.UsesCgo {
		t.Fatalf("embeds=%v cgo=%v", g.Embeds, g.UsesCgo)
	}
}

func TestGoMainPackageChoice(t *testing.T) {
	cases := []struct {
		mains []string
		want  string
	}{
		{nil, ""},
		{[]string{"."}, "."},
		{[]string{".", "cmd/x"}, "."},
		{[]string{"cmd/a", "cmd/b"}, ""},
	}
	for _, c := range cases {
		g := &GoInfo{Module: "example.com/m", MainPackages: c.mains}
		if got := g.MainPackage(); got != c.want {
			t.Errorf("%v: got %q want %q", c.mains, got, c.want)
		}
	}
}

func TestDetectGoCgoAndErrors(t *testing.T) {
	res, err := Detect(repo(t, map[string]string{
		"go.mod":  "module m\ngo 1.21\n",
		"main.go": "package main\n// #include <stdio.h>\nimport \"C\"\nfunc main(){}\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Go.UsesCgo || res.Go.GoVersion != "1.21" || res.Go.MainPackage() != "." {
		t.Fatalf("%+v", res.Go)
	}
	if _, err := Detect(repo(t, map[string]string{"go.mod": "go 1.21\n"})); err == nil {
		t.Fatal("want error for go.mod without module directive")
	}
}

func TestDetectPythonRequirements(t *testing.T) {
	dir := repo(t, map[string]string{
		"requirements.txt": `# comment
Flask==3.0.0
gunicorn>=21 # inline comment
-r base.txt
--index-url https://example.com/simple
psycopg2[pool]>=2.9; python_version > "3.8"
Some_Package.Name
`,
		".python-version": "3.11.4\n",
		"app.py":          "from flask import Flask\napp = Flask(__name__)\n",
	})
	res, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := res.Python
	if res.Manifest != constants.ManifestRequirementsTxt || p.Tool != PythonToolPip {
		t.Fatalf("manifest=%s tool=%s", res.Manifest, p.Tool)
	}
	want := []string{"flask", "gunicorn", "psycopg2", "some-package-name"}
	if !reflect.DeepEqual(p.Requirements, want) {
		t.Fatalf("reqs = %v", p.Requirements)
	}
	if !reflect.DeepEqual(p.IncludedFiles, []string{"base.txt"}) || p.LocalReferences {
		t.Fatalf("includes=%v local=%v", p.IncludedFiles, p.LocalReferences)
	}
	if p.Version != "3.11" || !p.Has("Flask") || p.Has("django") {
		t.Fatalf("version=%s", p.Version)
	}
	h := p.Entrypoints["app.py"]
	if !h.Flask || h.AppVar != "app" || h.MainGuard {
		t.Fatalf("hints = %+v", h)
	}
}

func TestDetectPythonLocalRefs(t *testing.T) {
	for _, line := range []string{"-e .", ".", "./libs/x", "file:///tmp/x"} {
		res, err := Detect(repo(t, map[string]string{"requirements.txt": line + "\n"}))
		if err != nil {
			t.Fatal(err)
		}
		if !res.Python.LocalReferences {
			t.Errorf("%q not detected as local reference", line)
		}
	}
}

func TestDetectPythonPyproject(t *testing.T) {
	res, err := Detect(repo(t, map[string]string{
		"pyproject.toml": `[project]
name = "x"
requires-python = ">=3.10"
dependencies = ["fastapi>=0.100", "uvicorn[standard]"]
`,
		"main.py": "import fastapi\napi = fastapi.FastAPI()\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	p := res.Python
	if res.Manifest != constants.ManifestPyProjectTOML || p.Tool != PythonToolPip || p.Version != "3.10" {
		t.Fatalf("%+v", p)
	}
	if !reflect.DeepEqual(p.RequirementSpecs, []string{"fastapi>=0.100", "uvicorn[standard]"}) {
		t.Fatalf("specs = %v", p.RequirementSpecs)
	}
	if h := p.Entrypoints["main.py"]; !h.FastAPI || h.AppVar != "api" {
		t.Fatalf("hints = %+v", h)
	}
}

func TestDetectPythonPoetry(t *testing.T) {
	res, err := Detect(repo(t, map[string]string{
		"pyproject.toml": `[tool.poetry]
name = "x"
package-mode = false

[tool.poetry.dependencies]
python = "^3.11"
Django = "^5.0"
gunicorn = "*"
`,
		"poetry.lock":        "",
		"manage.py":          "if __name__ == '__main__':\n    pass\n",
		"mysite/wsgi.py":     "",
		"mysite/settings.py": "",
	}))
	if err != nil {
		t.Fatal(err)
	}
	p := res.Python
	if p.Tool != PythonToolPoetry || !p.HasPoetryLock || p.PoetryPackageMode || p.Version != "3.11" {
		t.Fatalf("%+v", p)
	}
	if !reflect.DeepEqual(p.Requirements, []string{"django", "gunicorn"}) || p.DjangoWSGI != "mysite.wsgi" {
		t.Fatalf("reqs=%v wsgi=%s", p.Requirements, p.DjangoWSGI)
	}
}

func TestDetectPythonBadToml(t *testing.T) {
	if _, err := Detect(repo(t, map[string]string{"pyproject.toml": "[[[nope"})); err == nil {
		t.Fatal("want TOML error")
	}
}

func TestReadManifestRefusesOversizedAndIrregular(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "package.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(dir); err == nil {
		t.Fatal("a directory named package.json must be an error, not a match")
	}

	big := t.TempDir()
	f, _ := os.Create(filepath.Join(big, "go.mod"))
	f.Truncate(maxManifestBytes + 1)
	f.Close()
	if _, err := Detect(big); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("want size refusal, got %v", err)
	}
}

func TestNormalizePythonName(t *testing.T) {
	for in, want := range map[string]string{"Django": "django", "zope.interface": "zope-interface", "a__b--c": "a-b-c"} {
		if got := NormalizePythonName(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
