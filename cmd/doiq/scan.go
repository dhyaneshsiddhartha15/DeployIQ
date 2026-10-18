package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/rules"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/scan"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// analyseMu serialises the static analysis. It is milliseconds per project,
// and it installs per-repo .doiq.yml settings process-wide; the slow parts of
// a scan (cloning, docker builds) still run in parallel.
var analyseMu sync.Mutex

type scanOptions struct {
	depth     int
	jobs      int
	format    string
	out       string
	config    string
	writeDir  string
	measure   bool
	scanVulns bool
	buildArgs multiFlag
	report    bool
	api       string

	githubOwner     string
	githubAPI       string
	tokenEnv        string
	includeForks    bool
	includeArchived bool
	limit           int
	maxRepoSizeMB   int
	cloneDir        string
	keepClones      bool
}

// runScan implements `doiq scan`: every project under the given directories,
// or every repository of a GitHub organisation, in one fleet report.
func runScan(args []string, e env) int {
	o := &scanOptions{}
	fs := flag.NewFlagSet(constants.AppName+" scan", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.IntVar(&o.depth, "depth", 3, "how many directory levels below each path to search for projects")
	fs.IntVar(&o.jobs, "j", runtime.NumCPU(), "projects processed in parallel")
	fs.StringVar(&o.format, "format", "text", "output: text, markdown, json or csv")
	fs.StringVar(&o.out, "out", "", "write the report to this file instead of stdout")
	fs.StringVar(&o.config, "config", "", "company settings for every project (each repo's .doiq.yml still applies)")
	fs.StringVar(&o.writeDir, "write-dir", "", "write each project's Dockerfile.optimized under this directory (repositories are never modified)")
	fs.BoolVar(&o.measure, "measure", false, "docker build every project's images for real sizes (slow)")
	fs.BoolVar(&o.scanVulns, "scan-vulns", false, "also Trivy-scan every image (implies -measure)")
	fs.Var(&o.buildArgs, "build-arg", "KEY=VALUE for -measure builds; repeatable")
	fs.BoolVar(&o.report, "report", false, "send each result to your dashboard (requires `doiq login`)")
	fs.StringVar(&o.api, "api", "", "API endpoint for -report")
	fs.StringVar(&o.githubOwner, "github-org", "", "scan every repository of this GitHub organisation or user (shallow clones)")
	fs.StringVar(&o.githubAPI, "github-api", "", "GitHub API base for GitHub Enterprise Server, e.g. https://ghe.acme.com/api/v3")
	fs.StringVar(&o.tokenEnv, "token-env", "GITHUB_TOKEN", "environment variable holding the GitHub token (needed for private repos)")
	fs.BoolVar(&o.includeForks, "include-forks", false, "include forks")
	fs.BoolVar(&o.includeArchived, "include-archived", false, "include archived repositories")
	fs.IntVar(&o.limit, "limit", 0, "scan at most this many repositories (0 = all)")
	fs.IntVar(&o.maxRepoSizeMB, "max-repo-size", 0, "skip repositories larger than this many MB (0 = no limit)")
	fs.StringVar(&o.cloneDir, "clone-dir", "", "where to clone (default: a temporary directory, removed afterwards)")
	fs.BoolVar(&o.keepClones, "keep-clones", false, "keep the clones after the scan")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "Usage: %s scan [flags] [dir ...]\n       %s scan -github-org ORG [flags]\n\n", constants.AppName, constants.AppName)
		fmt.Fprintf(out, "Analyse every project in a set of directories, or in a GitHub organisation, and\nsummarise how much image size could be saved and where. Nothing is modified.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return constants.ExitOK
		}
		return constants.ExitUsage
	}
	if o.scanVulns {
		o.measure = true
	}
	switch o.format {
	case "text", "markdown", "json", "csv":
	default:
		fmt.Fprintf(e.stderr, "-format must be text, markdown, json or csv\n")
		return constants.ExitUsage
	}
	if o.jobs < 1 {
		o.jobs = 1
	}

	ctx := context.Background()
	type target struct{ name, dir string }
	var targets []target
	var entries []scan.Entry
	var mu sync.Mutex

	if o.githubOwner != "" {
		if fs.NArg() > 0 {
			fmt.Fprintf(e.stderr, "give either directories or -github-org, not both\n")
			return constants.ExitUsage
		}
		dirs, cleanup, errEntries, err := cloneOrg(ctx, o, e)
		if err != nil {
			fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
			return constants.ExitFailure
		}
		defer cleanup()
		entries = append(entries, errEntries...)
		for name, dir := range dirs {
			projects, _ := scan.Discover([]string{dir}, o.depth, isMonorepoRoot)
			if len(projects) == 0 {
				entries = append(entries, scan.Entry{Name: name, Path: name, Status: scan.StatusNoStack, Reason: "no supported manifest found"})
			}
			for _, p := range projects {
				rel, _ := filepath.Rel(dir, p)
				n := name
				if rel != "." {
					n = name + "/" + filepath.ToSlash(rel)
				}
				targets = append(targets, target{n, p})
			}
		}
	} else {
		roots := fs.Args()
		if len(roots) == 0 {
			roots = []string{"."}
		}
		projects, err := scan.Discover(roots, o.depth, isMonorepoRoot)
		if err != nil {
			fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
			return constants.ExitFailure
		}
		base := commonBase(roots)
		for _, p := range projects {
			rel, err := filepath.Rel(base, p)
			if err != nil || rel == "." {
				rel = filepath.Base(p)
			}
			targets = append(targets, target{filepath.ToSlash(rel), p})
		}
	}
	if len(targets) == 0 && len(entries) == 0 {
		fmt.Fprintf(e.stderr, "no projects found (looked for package.json, go.mod, requirements.txt, pyproject.toml, pom.xml, build.gradle)\n")
		return constants.ExitStackUnknown
	}
	fmt.Fprintf(e.stderr, "Analysing %d project(s) with %d worker(s)...\n", len(targets), o.jobs)

	work := make(chan target)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < o.jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				entry := analyseProject(t.name, t.dir, o, e)
				mu.Lock()
				entries = append(entries, entry)
				mu.Unlock()
			}
		}()
	}
	for _, t := range targets {
		work <- t
	}
	close(work)
	wg.Wait()

	summary := scan.Summarize(entries)
	fmt.Fprintf(e.stderr, "Done in %s.\n", time.Since(start).Round(time.Second))

	var w io.Writer = e.stdout
	if o.out != "" {
		f, err := os.Create(o.out) //nolint:gosec // operator-chosen path
		if err != nil {
			fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
			return constants.ExitFailure
		}
		defer f.Close()
		w = f
	}
	var err error
	switch o.format {
	case "json":
		err = scan.PrintJSON(w, summary)
	case "csv":
		err = scan.PrintCSV(w, summary)
	case "markdown":
		err = scan.PrintMarkdown(w, summary)
	default:
		err = scan.PrintText(w, summary)
	}
	if err != nil {
		fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
		return constants.ExitFailure
	}
	if o.out != "" {
		fmt.Fprintf(e.stderr, "Report written to %s\n", o.out)
	}
	return constants.ExitOK
}

// analyseProject runs the single-repo pipeline on one project and turns every
// outcome — including refusals — into a summary entry.
func analyseProject(name, dir string, o *scanOptions, e env) scan.Entry {
	entry := scan.Entry{Name: name, Path: dir}

	analyseMu.Lock()
	rep, err := buildReport(dir, "", o.config)
	analyseMu.Unlock()

	switch {
	case errors.Is(err, errStackUnknown):
		entry.Status, entry.Reason = scan.StatusNoStack, "no supported manifest"
		return entry
	case errors.Is(err, rules.ErrUnsupported), errors.Is(err, dockerfile.ErrUnsupported):
		entry.Status, entry.Reason = scan.StatusUnsupported, strings.TrimPrefix(err.Error(), rules.ErrUnsupported.Error()+": ")
		return entry
	case err != nil:
		entry.Status, entry.Reason = scan.StatusError, err.Error()
		return entry
	}
	entry.Status, entry.Report = scan.StatusOK, rep

	if o.measure {
		mo := &options{measure: true, scanVulns: o.scanVulns, buildArgs: o.buildArgs}
		quiet := env{stdin: e.stdin, stdout: io.Discard, stderr: io.Discard}
		if err := measureReport(dir, rep, mo, quiet); err != nil {
			entry.Reason = "measure failed: " + err.Error()
		}
	}
	if o.writeDir != "" {
		target := filepath.Join(o.writeDir, filepath.FromSlash(name), constants.DockerfileOutputName)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
			_ = os.WriteFile(target, []byte(rep.Dockerfile), 0o644)
		}
	}
	if o.report {
		ro := &options{repoName: name, api: o.api}
		if err := sendReport(dir, rep, ro, env{stderr: io.Discard}); err != nil {
			entry.Reason = "report failed: " + err.Error()
		}
	}
	return entry
}

// isMonorepoRoot reports a project whose real projects are its children:
// npm/Yarn/pnpm workspaces, a Maven reactor, a Gradle multi-project build.
func isMonorepoRoot(dir string) bool {
	det, err := detector.Detect(dir)
	if err != nil {
		return false
	}
	return (det.Node != nil && det.Node.Workspaces) || (det.Java != nil && det.Java.MultiModule)
}

func cloneOrg(ctx context.Context, o *scanOptions, e env) (map[string]string, func(), []scan.Entry, error) {
	token := os.Getenv(o.tokenEnv)
	gh := scan.NewGitHub(o.githubAPI, token)
	repos, err := gh.List(ctx, o.githubOwner, scan.ListOptions{
		IncludeForks: o.includeForks, IncludeArchived: o.includeArchived, Limit: o.limit, MaxSizeMB: o.maxRepoSizeMB,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if len(repos) == 0 {
		return nil, nil, nil, fmt.Errorf("no repositories found for %s (private ones need a token in $%s)", o.githubOwner, o.tokenEnv)
	}

	dir := o.cloneDir
	cleanup := func() {}
	if dir == "" {
		tmp, err := os.MkdirTemp("", "doiq-scan-*")
		if err != nil {
			return nil, nil, nil, err
		}
		dir = tmp
		if !o.keepClones {
			cleanup = func() { _ = os.RemoveAll(tmp) }
		}
	}
	fmt.Fprintf(e.stderr, "Cloning %d repositories of %s...\n", len(repos), o.githubOwner)

	dirs := map[string]string{}
	var failed []scan.Entry
	var mu sync.Mutex
	sem := make(chan struct{}, o.jobs)
	var wg sync.WaitGroup
	for _, r := range repos {
		r := r
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d, err := gh.Clone(ctx, r, dir)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, scan.Entry{Name: r.FullName, Status: scan.StatusError, Reason: err.Error()})
				return
			}
			dirs[r.FullName] = d
		}()
	}
	wg.Wait()
	return dirs, cleanup, failed, nil
}

// commonBase is the directory names are reported relative to.
func commonBase(roots []string) string {
	if len(roots) == 1 {
		abs, _ := filepath.Abs(roots[0])
		return abs
	}
	cwd, _ := os.Getwd()
	return cwd
}
