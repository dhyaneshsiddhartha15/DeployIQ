package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/analyzer"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/report"
)

func touch(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("{}"), 0o644)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"api/go.mod",
		"api/internal/tools/go.mod", // inside a project: not a separate one
		"web/package.json",
		"web/node_modules/x/package.json",
		"mono/package.json",        // a workspace root...
		"mono/apps/a/package.json", // ...whose children are the projects
		"mono/apps/b/requirements.txt",
		"deep/a/b/c/d/pom.xml", // beyond depth 3
		".hidden/go.mod",
		"docs/readme.md",
	)
	isMono := func(dir string) bool { return filepath.Base(dir) == "mono" }
	got, err := Discover([]string{root}, 3, isMono)
	if err != nil {
		t.Fatal(err)
	}
	var rel []string
	for _, p := range got {
		r, _ := filepath.Rel(root, p)
		rel = append(rel, filepath.ToSlash(r))
	}
	want := "api mono/apps/a mono/apps/b web"
	if strings.Join(rel, " ") != want {
		t.Fatalf("got %v, want %s", rel, want)
	}

	// A repo given directly is itself the project.
	got, _ = Discover([]string{filepath.Join(root, "api")}, 3, nil)
	if len(got) != 1 {
		t.Fatalf("%v", got)
	}
	if _, err := Discover([]string{filepath.Join(root, "nope")}, 3, nil); err == nil {
		t.Fatal("missing dir accepted")
	}
}

func rep(beforeMB, afterMB float64) *report.Report {
	return &report.Report{Stack: "node", Estimate: analyzer.Estimate{
		Before: analyzer.Breakdown{TotalMB: beforeMB}, After: analyzer.Breakdown{TotalMB: afterMB},
	}}
}

func TestSummarize(t *testing.T) {
	measured := rep(1000, 900)
	measured.Measured = &report.Measurement{BeforeMB: 1200, AfterMB: 200}
	measured.Vulns = &report.VulnComparison{HasBefore: true, Before: report.VulnCounts{Critical: 5, High: 30}, After: report.VulnCounts{High: 2}}
	s := Summarize([]Entry{
		{Name: "small", Status: StatusOK, Report: rep(100, 98)},
		{Name: "big", Status: StatusOK, Report: rep(2000, 200)},
		{Name: "measured", Status: StatusOK, Report: measured},
		{Name: "weird", Status: StatusUnsupported, Reason: "custom base | image"},
		{Name: "broken", Status: StatusError, Reason: "boom"},
	})
	if s.Projects != 5 || s.Analysed != 3 || s.Improvable != 2 || s.Skipped != 1 || s.Errors != 1 {
		t.Fatalf("%+v", s)
	}
	if s.Entries[0].Name != "big" || s.Entries[1].Name != "measured" {
		t.Fatalf("worst offenders first: %v, %v", s.Entries[0].Name, s.Entries[1].Name)
	}
	if s.Entries[2].Status != StatusOptimal {
		t.Fatal("a 2% saving is already optimal")
	}
	if s.SavedMB != 1800+1000+2 || s.CriticalVulnsRemoved != 5 || s.HighVulnsRemoved != 28 {
		t.Fatalf("totals %+v", s)
	}

	for name, render := range map[string]func(*bytes.Buffer) error{
		"text":     func(b *bytes.Buffer) error { return PrintText(b, s) },
		"markdown": func(b *bytes.Buffer) error { return PrintMarkdown(b, s) },
		"csv":      func(b *bytes.Buffer) error { return PrintCSV(b, s) },
		"json":     func(b *bytes.Buffer) error { return PrintJSON(b, s) },
	} {
		var b bytes.Buffer
		if err := render(&b); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.Contains(out, "big") || !strings.Contains(out, "weird") {
			t.Errorf("%s output incomplete:\n%s", name, out)
		}
		if name == "markdown" && strings.Contains(out, "custom base | image") {
			t.Error("pipe in a reason breaks the markdown table")
		}
		if name == "json" && !json.Valid(b.Bytes()) {
			t.Error("invalid json")
		}
	}
	if fmtMB(3*1024*1024) != "3.0 TB" || fmtMB(1536) != "1.5 GB" || fmtMB(12) != "12 MB" {
		t.Error("fmtMB")
	}
}

func TestGitHubListPagesAndFilters(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if strings.HasPrefix(r.URL.Path, "/orgs/octocat") {
			http.NotFound(w, r) // a user, not an org
			return
		}
		page := r.URL.Query().Get("page")
		var repos []map[string]any
		if page == "1" {
			for i := 0; i < 100; i++ {
				repos = append(repos, map[string]any{"full_name": fmt.Sprintf("octocat/r%d", i), "clone_url": "x", "size": 10})
			}
			repos[0]["fork"] = true
			repos[1]["archived"] = true
			repos[2]["size"] = 900 * 1024
		} else {
			repos = append(repos, map[string]any{"full_name": "octocat/last", "clone_url": "x"})
		}
		json.NewEncoder(w).Encode(repos)
	}))
	defer srv.Close()

	gh := NewGitHub(srv.URL, "tkn")
	repos, err := gh.List(context.Background(), "octocat", ListOptions{MaxSizeMB: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 98 || repos[len(repos)-1].FullName != "octocat/last" || auth != "Bearer tkn" {
		t.Fatalf("%d repos, last %s, auth %q", len(repos), repos[len(repos)-1].FullName, auth)
	}
	limited, _ := gh.List(context.Background(), "octocat", ListOptions{Limit: 5, IncludeForks: true})
	if len(limited) != 5 || limited[0].FullName != "octocat/r0" {
		t.Fatalf("limit/forks: %d", len(limited))
	}
	if redact("token abc in output", "abc") != "token *** in output" {
		t.Fatal("redact")
	}
}
