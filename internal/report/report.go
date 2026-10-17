package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/analyzer"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/rules"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// Report is everything the CLI prints about one run.
type Report struct {
	// Repo is the analysed directory.
	Repo string `json:"repo"`

	// Stack and Manifest come from the detector; Others lists further
	// stacks found in a polyglot repo.
	Stack    string   `json:"stack"`
	Manifest string   `json:"manifest"`
	Others   []string `json:"otherStacks,omitempty"`

	// StackDetail is a one-line description ("npm, package-lock.json").
	StackDetail string `json:"stackDetail,omitempty"`

	// Existing describes the current Dockerfile, or "" when there is none.
	Existing    string `json:"existingDockerfile,omitempty"`
	ExistingRaw string `json:"-"`

	RulesApplied   []string `json:"rulesApplied"`
	AlreadyPresent []string `json:"alreadyPresent,omitempty"`
	Notes          []string `json:"notes,omitempty"`

	Estimate    analyzer.Estimate `json:"estimate"`
	BeforeLabel string            `json:"beforeLabel"`

	Dockerfile string `json:"dockerfile"`
}

// Options controls rendering.
type Options struct {
	// Diff shows a unified diff against the existing Dockerfile instead of
	// the full generated file. Ignored when there is no existing file.
	Diff bool

	// Quiet omits the generated Dockerfile entirely.
	Quiet bool
}

// New assembles a Report from the pipeline's outputs.
func New(repo, stack, manifest string, others []string, detail, existing, existingRaw string, res *rules.Result) *Report {
	return &Report{
		Repo:           repo,
		Stack:          stack,
		Manifest:       manifest,
		Others:         others,
		StackDetail:    detail,
		Existing:       existing,
		ExistingRaw:    existingRaw,
		RulesApplied:   nonNil(res.RulesApplied),
		AlreadyPresent: res.AlreadyPresent,
		Notes:          res.Notes,
		Estimate:       analyzer.Compare(res.Before, res.After),
		BeforeLabel:    res.BeforeLabel,
		Dockerfile:     string(res.Dockerfile),
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// PrintJSON writes the report as one JSON document, for CI and scripting.
func PrintJSON(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// Print renders the human-readable report. stdout carries only this — logs go
// to stderr — so `doiq . > report.txt` captures exactly what a user would see.
func Print(w io.Writer, r *Report, opts Options) error {
	p := &printer{w: w}

	p.line("DeployIQ Optimizer")
	p.line("")
	p.kv("Repository", r.Repo)
	stack := r.Stack + " (" + r.Manifest + ")"
	if r.StackDetail != "" {
		stack += ", " + r.StackDetail
	}
	p.kv("Detected stack", stack)
	if len(r.Others) > 0 {
		p.kv("", "Also found: "+strings.Join(r.Others, ", ")+". Using "+r.Stack+" by priority; polyglot repos are not fully supported yet.")
	}
	if r.Existing != "" {
		p.kv("Dockerfile", r.Existing)
	} else {
		p.kv("Dockerfile", "none found; generated one from "+r.Stack+" conventions")
	}

	p.line("")
	if len(r.RulesApplied) == 0 {
		p.line("Optimizations: none new. The existing Dockerfile already applies every rule this version knows.")
	} else {
		p.line("Optimizations applied:")
		for _, id := range r.RulesApplied {
			p.line(fmt.Sprintf("  + %-18s %s", id, rules.Descriptions[id]))
		}
	}
	if len(r.AlreadyPresent) > 0 {
		p.line("Already present: " + strings.Join(r.AlreadyPresent, ", "))
	}

	p.line("")
	printEstimate(p, r)

	if len(r.Notes) > 0 {
		p.line("")
		p.line("Notes:")
		for _, n := range r.Notes {
			p.wrapped("  - ", n)
		}
	}

	if !opts.Quiet {
		p.line("")
		if opts.Diff && r.ExistingRaw != "" {
			d := Diff(constants.DockerfileName, constants.DockerfileOutputName, r.ExistingRaw, r.Dockerfile, 3)
			if d == "" {
				p.line("(no differences)")
			} else {
				p.raw(d)
			}
		} else {
			p.line("----- " + constants.DockerfileOutputName + " " + strings.Repeat("-", 40))
			p.raw(r.Dockerfile)
			p.line(strings.Repeat("-", 46+len(constants.DockerfileOutputName)))
		}
	}
	return p.err
}

func printEstimate(p *printer, r *Report) {
	e := r.Estimate
	p.line("Estimated image size (estimates, not measurements):")
	p.line(fmt.Sprintf("  %-22s %12s %12s", "", "before", "after"))
	row := func(name string, b, a float64) {
		if b == 0 && a == 0 {
			return
		}
		p.line(fmt.Sprintf("  %-22s %12s %12s", name, mb(b), mb(a)))
	}
	row("base image", e.Before.BaseMB, e.After.BaseMB)
	row("OS packages", e.Before.PackagesMB, e.After.PackagesMB)
	row("dependencies", e.Before.DependenciesMB, e.After.DependenciesMB)
	row("package caches", e.Before.CacheMB, e.After.CacheMB)
	row("toolchain / caches", e.Before.ToolchainMB, e.After.ToolchainMB)
	row("binary", e.Before.ArtifactMB, e.After.ArtifactMB)
	row("application files", e.Before.SourceMB, e.After.SourceMB)
	p.line(fmt.Sprintf("  %-22s %12s %12s", "total (approx.)", "~"+mb(e.Before.TotalMB), "~"+mb(e.After.TotalMB)))
	if e.SavedMB > 0 {
		p.line(fmt.Sprintf("  Estimated reduction: ~%s (%.0f%%)", mb(e.SavedMB), e.SavedPercent))
	} else {
		p.line("  Estimated reduction: none measurable")
	}
	p.line("  \"before\" is the " + r.BeforeLabel + ". Figures are directionally accurate, not byte-exact.")
}

func mb(v float64) string {
	switch {
	case v >= 10:
		return fmt.Sprintf("%.0f MB", v)
	case v >= 0.1:
		return fmt.Sprintf("%.1f MB", v)
	case v > 0:
		return "<0.1 MB"
	}
	return "0 MB"
}

// printer accumulates the first write error so the formatting code does not
// check every line.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) raw(s string) {
	if p.err == nil {
		_, p.err = io.WriteString(p.w, s)
	}
}

func (p *printer) line(s string) { p.raw(s + "\n") }

func (p *printer) kv(k, v string) {
	if k != "" {
		k += ":"
	}
	p.line(fmt.Sprintf("%-16s %s", k, v))
}

// wrapped writes a long line wrapped at 88 columns with a hanging indent.
func (p *printer) wrapped(prefix, text string) {
	const width = 88
	indent := strings.Repeat(" ", len(prefix))
	cur := prefix
	for _, word := range strings.Fields(text) {
		if len(cur)+len(word)+1 > width && strings.TrimSpace(cur) != strings.TrimSpace(prefix) {
			p.line(cur)
			cur = indent
		}
		if cur == prefix || cur == indent {
			cur += word
		} else {
			cur += " " + word
		}
	}
	p.line(cur)
}
