// Package measure builds images with Docker to report real sizes, and scans
// them for known vulnerabilities with Trivy.
//
// Opt-in only (-measure, -scan-vulns). The default doiq run stays static and
// offline (Phase 0.6); this package is the one place the CLI runs external
// programs, and it only ever runs `docker` and `trivy` with arguments it
// builds itself — never anything taken from the repository.
package measure

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/report"
)

// ErrNoDocker means Docker is not installed or its daemon is not running.
var ErrNoDocker = errors.New("docker is not available: install Docker and make sure the daemon is running")

// TrivyImage is the scanner image used when no trivy binary is installed.
// Pinned: a scanner whose version drifts produces numbers that cannot be
// compared over time.
const TrivyImage = "aquasec/trivy:0.58.1"

// Docker runs builds and inspections.
type Docker struct {
	// Bin is the docker executable; "docker" on PATH by default.
	Bin string

	// BuildArgs are passed to every build as --build-arg (private module
	// tokens and the like). Never logged.
	BuildArgs []string

	// Timeout bounds one build.
	Timeout time.Duration
}

// New checks that Docker answers and returns a client.
func New(ctx context.Context, buildArgs []string) (*Docker, error) {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, ErrNoDocker
	}
	d := &Docker{Bin: bin, BuildArgs: buildArgs, Timeout: 30 * time.Minute}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := d.run(cctx, "info", "--format", "{{.ServerVersion}}"); err != nil {
		return nil, ErrNoDocker
	}
	return d, nil
}

// Tag returns a fresh local tag pair for one measurement.
func Tag() (before, after string) {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	return "doiq-measure-" + id + ":before", "doiq-measure-" + id + ":after"
}

// Build builds dockerfile (an absolute path; it may lie outside the context)
// with root as the build context, tags it, and returns its size in MB.
func (d *Docker) Build(ctx context.Context, root, dockerfile, tag string) (float64, error) {
	bctx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	args := []string{"build", "-q", "-f", dockerfile, "-t", tag}
	for _, a := range d.BuildArgs {
		args = append(args, "--build-arg", a)
	}
	args = append(args, root)
	if out, err := d.run(bctx, args...); err != nil {
		return 0, fmt.Errorf("docker build of %s failed:\n%s", filepath.Base(dockerfile), tail(out, 25))
	}
	return d.SizeMB(ctx, tag)
}

// BuildBytes writes content to a temporary file outside the repository and
// builds it: measuring never writes into the user's checkout.
func (d *Docker) BuildBytes(ctx context.Context, root string, content []byte, tag string) (float64, error) {
	f, err := os.CreateTemp("", "doiq-Dockerfile-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(content); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	return d.Build(ctx, root, f.Name(), tag)
}

// SizeMB is the image's size as Docker reports it, in MB (2^20 bytes).
func (d *Docker) SizeMB(ctx context.Context, tag string) (float64, error) {
	out, err := d.run(ctx, "image", "inspect", "-f", "{{.Size}}", tag)
	if err != nil {
		return 0, fmt.Errorf("inspecting %s: %s", tag, strings.TrimSpace(out))
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(out), 64)
	if err != nil {
		return 0, fmt.Errorf("inspecting %s: unexpected size %q", tag, out)
	}
	return n / (1 << 20), nil
}

// Remove deletes the given tags, ignoring errors (best-effort cleanup).
func (d *Docker) Remove(tags ...string) {
	for _, t := range tags {
		if t != "" {
			_, _ = d.run(context.Background(), "rmi", "-f", t)
		}
	}
}

// Scan counts known vulnerabilities in an image. It uses a trivy binary on
// PATH, else runs the pinned Trivy image against the local Docker daemon.
func (d *Docker) Scan(ctx context.Context, image string) (report.VulnCounts, string, error) {
	sctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	trivyArgs := []string{"image", "--quiet", "--format", "json", "--scanners", "vuln", image}

	var out string
	var err error
	scanner := "trivy"
	if bin, lerr := exec.LookPath("trivy"); lerr == nil {
		out, err = runCmd(sctx, bin, trivyArgs...)
	} else {
		scanner = TrivyImage
		args := append([]string{"run", "--rm",
			"-v", "/var/run/docker.sock:/var/run/docker.sock",
			// A named volume keeps the vulnerability database between runs.
			"-v", "doiq-trivy-cache:/root/.cache/",
			TrivyImage}, trivyArgs...)
		out, err = d.run(sctx, args...)
	}
	if err != nil {
		return report.VulnCounts{}, scanner, fmt.Errorf("vulnerability scan of %s failed:\n%s", image, tail(out, 15))
	}
	counts, perr := ParseTrivy([]byte(extractJSON(out)))
	return counts, scanner, perr
}

// ParseTrivy counts findings by severity in Trivy's JSON report.
func ParseTrivy(raw []byte) (report.VulnCounts, error) {
	var doc struct {
		Results []struct {
			Vulnerabilities []struct {
				VulnerabilityID string `json:"VulnerabilityID"`
				PkgName         string `json:"PkgName"`
				Severity        string `json:"Severity"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return report.VulnCounts{}, fmt.Errorf("reading trivy output: %w", err)
	}
	var c report.VulnCounts
	seen := map[string]bool{}
	for _, r := range doc.Results {
		for _, v := range r.Vulnerabilities {
			// The same CVE in the same package is one finding, however
			// many layers mention it.
			key := v.VulnerabilityID + "|" + v.PkgName
			if seen[key] {
				continue
			}
			seen[key] = true
			switch strings.ToUpper(v.Severity) {
			case "CRITICAL":
				c.Critical++
			case "HIGH":
				c.High++
			case "MEDIUM":
				c.Medium++
			case "LOW":
				c.Low++
			default:
				c.Unknown++
			}
		}
	}
	return c, nil
}

// extractJSON drops any log lines a scanner prints before its JSON document.
func extractJSON(s string) string {
	if i := strings.Index(s, "{"); i > 0 {
		return s[i:]
	}
	return s
}

func (d *Docker) run(ctx context.Context, args ...string) (string, error) {
	return runCmd(ctx, d.Bin, args...)
}

func runCmd(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
