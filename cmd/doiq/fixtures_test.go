//go:build fixtures

// The Phase 10.2 release gate: "Does the generated Dockerfile actually build,
// for every supported stack, every time?" Run with:
//
//	go test ./cmd/doiq -run TestFixtures -tags=fixtures -v
//
// For every repository under testdata/fixtures/ it:
//
//  1. runs the CLI exactly as a user would (`doiq -write -yes <repo>`) on a
//     copy of the fixture, so testdata is never modified;
//  2. runs `docker build` on the generated Dockerfile.optimized — the step
//     that catches what a text assertion cannot;
//  3. starts the container and requests the URL in fixture.json, so "builds"
//     also means "starts and serves";
//  4. when the fixture has its own Dockerfile, builds that too and requires the
//     optimized image to be smaller, logging estimate against reality.
//
// Needs a Docker daemon. Set DOIQ_FIXTURE_COMPARE=0 to skip step 4.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

const fixturesDir = "../../testdata/fixtures"

type fixtureSpec struct {
	Port   int    `json:"port"`
	Path   string `json:"path"`
	Expect string `json:"expect"`
}

func TestFixtures(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker is required for the fixture gate; it must not silently pass without it")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Fatalf("docker daemon unavailable: %v\n%s", err, out)
	}

	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("no fixtures found")
	}

	// Every supported stack must have at least one fixture (Phase 1.5).
	for _, stack := range constants.SupportedStacks {
		found := false
		for _, n := range names {
			if strings.HasPrefix(n, stack+"-") {
				found = true
			}
		}
		if !found {
			t.Errorf("stack %q is advertised as supported but has no fixture", stack)
		}
	}

	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runFixture(t, name)
		})
	}
}

func runFixture(t *testing.T, name string) {
	src := filepath.Join(fixturesDir, name)
	dir := t.TempDir()
	if err := copyTree(src, dir); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-write", "-yes", "-format", "json", dir}, env{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr})
	if code != constants.ExitOK {
		t.Fatalf("doiq exited %d\nstderr:\n%s", code, stderr.String())
	}
	var rep struct {
		Estimate struct {
			Before struct{ TotalMB float64 } `json:"before"`
			After  struct{ TotalMB float64 } `json:"after"`
		} `json:"estimate"`
		RulesApplied []string `json:"rulesApplied"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("report is not JSON: %v\n%s", err, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, constants.DockerfileOutputName)); err != nil {
		t.Fatalf("%s was not written: %v", constants.DockerfileOutputName, err)
	}

	tag := "doiq-fixture-" + name + ":optimized"
	docker(t, 15*time.Minute, "build", "-q", "-f", filepath.Join(dir, constants.DockerfileOutputName), "-t", tag, dir)
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", tag).Run() })

	if spec, ok := readSpec(t, src); ok {
		smokeTest(t, tag, spec)
	}

	optimized := imageSizeMB(t, tag)
	t.Logf("optimized image: %.0f MB actual, %.0f MB estimated; rules %v", optimized, rep.Estimate.After.TotalMB, rep.RulesApplied)

	if os.Getenv("DOIQ_FIXTURE_COMPARE") == "0" {
		return
	}
	// Wherever the tool found it: the root, or build/ and friends.
	existing, err := dockerfile.Parse(src)
	if err != nil || existing == nil {
		return
	}
	origTag := "doiq-fixture-" + name + ":original"
	docker(t, 15*time.Minute, "build", "-q", "-f", filepath.Join(dir, filepath.FromSlash(existing.RelPath)), "-t", origTag, dir)
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", origTag).Run() })
	original := imageSizeMB(t, origTag)
	t.Logf("original image:  %.0f MB actual, %.0f MB estimated", original, rep.Estimate.Before.TotalMB)
	if optimized >= original {
		t.Errorf("optimized image (%.0f MB) is not smaller than the original (%.0f MB)", optimized, original)
	}
	actualSaving := (original - optimized) / original * 100
	estSaving := (rep.Estimate.Before.TotalMB - rep.Estimate.After.TotalMB) / rep.Estimate.Before.TotalMB * 100
	t.Logf("saving: %.0f%% actual, %.0f%% estimated", actualSaving, estSaving)
}

func smokeTest(t *testing.T, tag string, spec fixtureSpec) {
	t.Helper()
	cid := strings.TrimSpace(docker(t, time.Minute, "run", "-d", "-p", "127.0.0.1::"+strconv.Itoa(spec.Port), tag))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", cid).Run() })

	mapping := strings.TrimSpace(docker(t, time.Minute, "port", cid, strconv.Itoa(spec.Port)))
	// "127.0.0.1:49153" — possibly one line per address family.
	hostPort := strings.Split(mapping, "\n")[0]
	url := "http://" + hostPort + spec.Path

	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:gosec // local test container
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), spec.Expect) {
				return
			}
			lastErr = fmt.Errorf("status %d, body %q", resp.StatusCode, body)
		} else {
			lastErr = err
		}
		time.Sleep(time.Second)
	}
	logs, _ := exec.Command("docker", "logs", cid).CombinedOutput()
	t.Fatalf("container never served %s: %v\ncontainer logs:\n%s", url, lastErr, logs)
}

func readSpec(t *testing.T, dir string) (fixtureSpec, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
	if os.IsNotExist(err) {
		return fixtureSpec{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var s fixtureSpec
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("fixture.json: %v", err)
	}
	if s.Path == "" {
		s.Path = "/"
	}
	return s, true
}

func docker(t *testing.T, timeout time.Duration, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker %s: %v\n%s%s", strings.Join(args, " "), err, out.String(), errb.String())
	}
	return out.String()
}

func imageSizeMB(t *testing.T, tag string) float64 {
	t.Helper()
	out := strings.TrimSpace(docker(t, time.Minute, "image", "inspect", "-f", "{{.Size}}", tag))
	n, err := strconv.ParseFloat(out, 64)
	if err != nil {
		t.Fatalf("image size %q: %v", out, err)
	}
	return n / (1 << 20)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
