package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/rules"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// CommentMarker identifies DeployIQ's pull-request comment so the GitHub
// Action updates one comment per PR instead of adding a new one per push.
const CommentMarker = "<!-- deployiq-optimizer -->"

// PrintMarkdown renders the report as GitHub-flavoured Markdown, for a pull
// request comment or a CI job summary. The generated Dockerfile (or the diff)
// goes in a collapsed block: reviewers see the numbers first.
func PrintMarkdown(w io.Writer, r *Report) error {
	p := &printer{w: w}
	p.line(CommentMarker)
	p.line("### DeployIQ Optimizer")
	p.line("")

	e := r.Estimate
	switch {
	case r.Measured != nil:
		m := r.Measured
		if m.BeforeMB > 0 && m.AfterMB < m.BeforeMB {
			p.line(fmt.Sprintf("**This image can be %.0f%% smaller:** %s → %s (measured with `docker build`).",
				m.SavedPercent(), mb(m.BeforeMB), mb(m.AfterMB)))
		} else {
			p.line(fmt.Sprintf("Optimized image measured at %s.", mb(m.AfterMB)))
		}
	case e.SavedMB > 0:
		p.line(fmt.Sprintf("**This image can be about %.0f%% smaller:** ~%s → ~%s (estimate).",
			e.SavedPercent, mb(e.Before.TotalMB), mb(e.After.TotalMB)))
	default:
		p.line("**No measurable saving.** The Dockerfile already applies what DeployIQ knows.")
	}
	p.line("")

	where := "no Dockerfile (one was generated from conventions)"
	if r.Existing != "" {
		where = "`" + r.Existing + "`"
	}
	p.line("| | |")
	p.line("|---|---|")
	p.line("| Stack | " + r.Stack + " (`" + r.Manifest + "`) |")
	p.line("| Current | " + where + " |")
	p.line(fmt.Sprintf("| Estimated size | ~%s → ~%s |", mb(e.Before.TotalMB), mb(e.After.TotalMB)))
	if m := r.Measured; m != nil {
		before := "—"
		if m.BeforeMB > 0 {
			before = mb(m.BeforeMB)
		}
		p.line(fmt.Sprintf("| Measured size | %s → %s |", before, mb(m.AfterMB)))
	}
	if v := r.Vulns; v != nil {
		p.line(fmt.Sprintf("| Vulnerabilities (critical/high) | %s → %s |", v.Before.Short(), v.After.Short()))
	}
	p.line("")

	if len(r.RulesApplied) > 0 {
		p.line("**Optimizations**")
		p.line("")
		for _, id := range r.RulesApplied {
			p.line("- `" + id + "` — " + rules.Descriptions[id])
		}
		p.line("")
	}
	if len(r.Notes) > 0 {
		p.line("<details><summary>Notes (" + fmt.Sprint(len(r.Notes)) + ")</summary>")
		p.line("")
		for _, n := range r.Notes {
			p.line("- " + n)
		}
		p.line("")
		p.line("</details>")
		p.line("")
	}

	if r.ExistingRaw != "" {
		d := Diff(existingName(r), constants.DockerfileOutputName, r.ExistingRaw, r.Dockerfile, 3)
		p.line("<details><summary>Proposed changes</summary>")
		p.line("")
		p.line("```diff")
		p.raw(d)
		p.line("```")
		p.line("</details>")
	} else {
		p.line("<details><summary>Generated " + constants.DockerfileOutputName + "</summary>")
		p.line("")
		p.line("```dockerfile")
		p.raw(r.Dockerfile)
		p.line("```")
		p.line("</details>")
	}
	p.line("")
	p.line("<sub>Run locally: `doiq -write .` writes `" + constants.DockerfileOutputName +
		"` for review. Sizes marked estimate are directionally accurate, not byte-exact.</sub>")
	return p.err
}

func existingName(r *Report) string {
	if r.ExistingPath != "" {
		return r.ExistingPath
	}
	return constants.DockerfileName
}

// FormatShort is a one-line summary for logs and the bulk-scan table.
func (r *Report) FormatShort() string {
	return strings.TrimSpace(fmt.Sprintf("%s: ~%s → ~%s (%.0f%%)",
		r.Stack, mb(r.Estimate.Before.TotalMB), mb(r.Estimate.After.TotalMB), r.Estimate.SavedPercent))
}
