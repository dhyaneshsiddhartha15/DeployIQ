package analyzer

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
)

func TestBaseImageMB(t *testing.T) {
	cases := map[string]struct {
		mb    float64
		known bool
	}{
		"node:20":                           {1100, true},
		"node:20-slim":                      {220, true},
		"node:22-alpine":                    {160, true},
		"python:3.12":                       {1020, true},
		"python:3.12-slim":                  {150, true},
		"python:3.12-alpine":                {60, true},
		"golang:1.22":                       {850, true},
		"golang:1.22-alpine":                {260, true},
		"gcr.io/distroless/static-debian12": {2, true},
		"gcr.io/distroless/base-debian12":   {21, true},
		"gcr.io/distroless/cc-debian12":     {24, true},
		"ubuntu:22.04":                      {78, true},
		"debian:bookworm-slim":              {75, true},
		"debian:bookworm":                   {117, true},
		"alpine:3.20":                       {8, true},
		"scratch":                           {0, true},
		"amazonlinux:2023":                  {145, true},
		"buildpack-deps:bookworm-curl":      {140, true},
		"redhat/ubi9-minimal":               {100, true},
		"mycorp/base:1":                     {unknownBaseMB, false},
	}
	for ref, want := range cases {
		mb, known := BaseImageMB(ref)
		if mb != want.mb || known != want.known {
			t.Errorf("%s: %v %v", ref, mb, known)
		}
	}
}

func TestCompareIsConservative(t *testing.T) {
	before := Profile{BaseImage: "node:20", DependenciesMB: 10.9, SourceMB: 0.4}
	after := Profile{BaseImage: "node:20-alpine", DependenciesMB: 5.1, SourceMB: 0.4}
	e := Compare(before, after)
	// before rounds down, after rounds up: the saving never flatters.
	if e.Before.TotalMB != math.Floor(1100+10.9+0.4) || e.After.TotalMB != math.Ceil(160+5.1+0.4) {
		t.Fatalf("%+v", e)
	}
	if e.SavedMB != e.Before.TotalMB-e.After.TotalMB || e.SavedPercent <= 0 || e.SavedPercent >= 100 {
		t.Fatalf("%+v", e)
	}

	// A "saving" that is really growth reports zero, never negative.
	e = Compare(after, before)
	if e.SavedMB != 0 || e.SavedPercent != 0 {
		t.Fatalf("%+v", e)
	}
}

func TestPackagesMB(t *testing.T) {
	if got := packagesMB([]string{"build-essential", "curl=7.88"}, "debian"); got != 238 {
		t.Fatal(got)
	}
	if got := packagesMB([]string{"something-obscure"}, "debian"); got != 10 {
		t.Fatal(got)
	}
	if got := packagesMB([]string{"something-obscure"}, "alpine"); math.Abs(got-1.8) > 1e-9 {
		t.Fatal(got)
	}
	b := Size(Profile{BaseImage: "debian:bookworm-slim", SystemPackages: []string{"git"}, CacheMB: 40})
	if b.PackagesMB != 45 || b.TotalMB != 75+45+40 {
		t.Fatalf("%+v", b)
	}
}

func TestDependencyEstimates(t *testing.T) {
	prod, all := NodeDependenciesMB(&detector.NodeInfo{LockPackages: 100, LockDevPackages: 60})
	if prod != 40*nodePackageMB || all != 100*nodePackageMB {
		t.Fatal(prod, all)
	}
	prod, all = NodeDependenciesMB(&detector.NodeInfo{Dependencies: []string{"a"}, DevDependencies: []string{"b", "c"}})
	if prod >= all || prod <= 0 {
		t.Fatal(prod, all)
	}
	py := PythonDependenciesMB(&detector.PythonInfo{Requirements: []string{"pandas", "unknown-lib"}})
	if py != 70+defaultPythonPackageMB+15 {
		t.Fatal(py)
	}
	if PipCacheMB(30) != 10 || NodeCacheMB(30) != 15 {
		t.Fatal("cache ratios")
	}
	if GoBinaryMB(&detector.GoInfo{Requires: 10000}) != 80 || GoBinaryMB(&detector.GoInfo{}) != 6 {
		t.Fatal("go binary bounds")
	}
	if GoModCacheMB(&detector.GoInfo{Requires: 10}) != 70 {
		t.Fatal("mod cache")
	}
}

func TestSourceMB(t *testing.T) {
	dir := t.TempDir()
	write := func(p string, n int) {
		full := filepath.Join(dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, make([]byte, n), 0o644)
	}
	write("src/app.js", 1<<20)
	write("node_modules/big/index.js", 50<<20)
	write(".git/objects/x", 10<<20)
	write("logs/a.log", 5<<20)
	write("docs/readme.md", 1<<20)
	if HasDockerignore(dir) {
		t.Fatal("no .dockerignore yet")
	}
	if got := SourceMB(dir); got < 6.9 || got > 7.1 {
		t.Fatalf("without .dockerignore: %.2f", got)
	}
	os.WriteFile(filepath.Join(dir, ".dockerignore"), []byte("# comment\nlogs/\n*.md\n!keep.md\n"), 0o644)
	if got := SourceMB(dir); got < 0.99 || got > 1.01 {
		t.Fatalf("with .dockerignore: %.2f", got)
	}
	if got := SourceMB(dir, "src"); got < 0.99 || got > 1.01 {
		t.Fatalf("only src: %.2f", got)
	}
}
