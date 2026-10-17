package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/analyzer"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/rules"
)

func sample() *Report {
	res := &rules.Result{
		Dockerfile:     []byte("FROM node:20-alpine\nCMD [\"node\",\"a.js\"]\n"),
		RulesApplied:   []string{rules.RuleMultiStage, rules.RuleAlpineBase},
		AlreadyPresent: []string{rules.RuleNonRootUser},
		Notes:          []string{strings.Repeat("a long note that wraps ", 10)},
		Before:         analyzer.Profile{BaseImage: "node:20", DependenciesMB: 20},
		After:          analyzer.Profile{BaseImage: "node:20-alpine", DependenciesMB: 10},
		BeforeLabel:    "current Dockerfile",
	}
	return New("/repo", "node", "package.json", []string{"go"}, "npm, package-lock.json",
		"Dockerfile (1 stage, runtime base node:20)", "FROM node:20\nCMD [\"node\",\"a.js\"]\n", res)
}

func TestPrintText(t *testing.T) {
	var b bytes.Buffer
	if err := Print(&b, sample(), Options{}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"Detected stack:  node (package.json), npm, package-lock.json",
		"Also found: go",
		"+ multi-stage-build",
		"Already present: non-root-user",
		"estimates, not measurements", // Phase 1.5: always labelled
		"Estimated reduction:",
		"FROM node:20-alpine",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  - ") && len(line) > 90 {
			t.Errorf("note not wrapped: %q", line)
		}
	}
}

func TestPrintDiffAndQuiet(t *testing.T) {
	var b bytes.Buffer
	Print(&b, sample(), Options{Diff: true})
	if !strings.Contains(b.String(), "--- Dockerfile\n+++ Dockerfile.optimized") || !strings.Contains(b.String(), "-FROM node:20\n+FROM node:20-alpine") {
		t.Fatalf("diff:\n%s", b.String())
	}
	b.Reset()
	Print(&b, sample(), Options{Quiet: true})
	if strings.Contains(b.String(), "FROM node") {
		t.Fatal("quiet printed the Dockerfile")
	}

	r := sample()
	r.Existing, r.ExistingRaw, r.RulesApplied = "", "", []string{}
	b.Reset()
	Print(&b, r, Options{Diff: true})
	if !strings.Contains(b.String(), "none found") || !strings.Contains(b.String(), "none new") || !strings.Contains(b.String(), "FROM node:20-alpine") {
		t.Fatalf("no-existing:\n%s", b.String())
	}
}

func TestPrintJSON(t *testing.T) {
	var b bytes.Buffer
	if err := PrintJSON(&b, sample()); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"stack", "rulesApplied", "estimate", "dockerfile", "beforeLabel"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
	if strings.Contains(b.String(), `<`) {
		t.Error("HTML escaping in JSON output")
	}
}

func TestDiff(t *testing.T) {
	if Diff("a", "b", "x\ny\n", "x\ny\n", 3) != "" {
		t.Fatal("identical inputs must produce no diff")
	}
	a := strings.Repeat("same\n", 10) + "old\n" + strings.Repeat("same\n", 10) + "old2\n"
	b := strings.Repeat("same\n", 10) + "new\n" + strings.Repeat("same\n", 10) + "new2\n"
	d := Diff("a", "b", a, b, 2)
	if strings.Count(d, "@@") != 4 { // two hunks, two markers each
		t.Fatalf("hunks:\n%s", d)
	}
	if !strings.Contains(d, "@@ -9,5 +9,5 @@") {
		t.Fatalf("header:\n%s", d)
	}
	if d := Diff("a", "b", "", "only\n", 3); !strings.Contains(d, "+only") {
		t.Fatal(d)
	}
}

func TestMB(t *testing.T) {
	for v, want := range map[float64]string{0: "0 MB", 0.05: "<0.1 MB", 2.25: "2.2 MB", 42.4: "42 MB", 1100: "1100 MB"} {
		if got := mb(v); got != want {
			t.Errorf("%v: %s", v, got)
		}
	}
}
