// Package scan finds every project under a set of directories (or in a GitHub
// organisation) and aggregates per-project reports into one fleet summary:
// how much image size an organisation could save, and where.
package scan

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/report"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// manifests mark a directory as a project.
var manifests = []string{
	constants.ManifestPackageJSON, constants.ManifestGoMod, constants.ManifestRequirementsTxt,
	constants.ManifestPyProjectTOML, constants.ManifestPomXML, constants.ManifestBuildGradle,
	constants.ManifestBuildGradleKts,
}

// skipDirs are never searched for projects.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true,
	"dist": true, ".venv": true, "venv": true, "__pycache__": true, ".gradle": true,
	"testdata": true, ".idea": true, ".vscode": true,
}

// Discover returns every project directory under roots, up to depth levels
// below each root. A project's own subdirectories are not searched — except
// in a monorepo root (workspaces, a Maven reactor, a Gradle multi-project),
// whose services are the real projects.
func Discover(roots []string, depth int, isMonorepoRoot func(dir string) bool) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", root)
		}
		walk(abs, 0, depth, isMonorepoRoot, func(p string) {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		})
	}
	sort.Strings(out)
	return out, nil
}

func walk(dir string, level, depth int, isMono func(string) bool, add func(string)) {
	if isProject(dir) {
		if isMono == nil || !isMono(dir) {
			add(dir)
			return
		}
	}
	if level >= depth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		walk(filepath.Join(dir, e.Name()), level+1, depth, isMono, add)
	}
}

func isProject(dir string) bool {
	for _, m := range manifests {
		if info, err := os.Stat(filepath.Join(dir, m)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// Status of one project in the summary.
const (
	StatusOK          = "ok"
	StatusOptimal     = "already optimal"
	StatusUnsupported = "skipped"
	StatusNoStack     = "no stack"
	StatusError       = "error"
)

// Entry is one project's outcome.
type Entry struct {
	Name   string         `json:"name"`
	Path   string         `json:"path"`
	Status string         `json:"status"`
	Reason string         `json:"reason,omitempty"`
	Report *report.Report `json:"report,omitempty"`
}

// BeforeMB is the current size: measured when available, else estimated.
func (e Entry) BeforeMB() float64 {
	if e.Report == nil {
		return 0
	}
	if m := e.Report.Measured; m != nil && m.BeforeMB > 0 {
		return m.BeforeMB
	}
	return e.Report.Estimate.Before.TotalMB
}

// AfterMB is the optimized size, measured or estimated.
func (e Entry) AfterMB() float64 {
	if e.Report == nil {
		return 0
	}
	if m := e.Report.Measured; m != nil {
		return m.AfterMB
	}
	return e.Report.Estimate.After.TotalMB
}

// SavedMB is never negative.
func (e Entry) SavedMB() float64 {
	if s := e.BeforeMB() - e.AfterMB(); s > 0 {
		return s
	}
	return 0
}

// SavedPercent is never negative.
func (e Entry) SavedPercent() float64 {
	if b := e.BeforeMB(); b > 0 {
		return e.SavedMB() / b * 100
	}
	return 0
}

// Summary is the fleet view.
type Summary struct {
	Projects    int     `json:"projects"`
	Analysed    int     `json:"analysed"`
	Improvable  int     `json:"improvable"`
	Skipped     int     `json:"skipped"`
	Errors      int     `json:"errors"`
	BeforeMB    float64 `json:"currentTotalMB"`
	AfterMB     float64 `json:"optimizedTotalMB"`
	SavedMB     float64 `json:"savedTotalMB"`
	AvgSavedPct float64 `json:"averageSavingPercent"`

	// CriticalVulnsRemoved and HighVulnsRemoved total the scans (-scan-vulns).
	CriticalVulnsRemoved int `json:"criticalVulnerabilitiesRemoved,omitempty"`
	HighVulnsRemoved     int `json:"highVulnerabilitiesRemoved,omitempty"`

	Entries []Entry `json:"entries"`
}

// improvableAt is the saving below which a project counts as already fine.
const improvableAt = 5

// Summarize aggregates entries, worst offenders (most MB to save) first.
func Summarize(entries []Entry) Summary {
	s := Summary{Projects: len(entries)}
	pctSum := 0.0
	for i := range entries {
		e := &entries[i]
		switch e.Status {
		case StatusOK, StatusOptimal:
			s.Analysed++
			if e.SavedPercent() >= improvableAt {
				s.Improvable++
				e.Status = StatusOK
			} else {
				e.Status = StatusOptimal
			}
			s.BeforeMB += e.BeforeMB()
			s.AfterMB += e.AfterMB()
			s.SavedMB += e.SavedMB()
			pctSum += e.SavedPercent()
			if v := e.Report.Vulns; v != nil && v.HasBefore {
				s.CriticalVulnsRemoved += max(0, v.Before.Critical-v.After.Critical)
				s.HighVulnsRemoved += max(0, v.Before.High-v.After.High)
			}
		case StatusError:
			s.Errors++
		default:
			s.Skipped++
		}
	}
	if s.Analysed > 0 {
		s.AvgSavedPct = pctSum / float64(s.Analysed)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].SavedMB() != entries[j].SavedMB() {
			return entries[i].SavedMB() > entries[j].SavedMB()
		}
		return entries[i].Name < entries[j].Name
	})
	s.Entries = entries
	return s
}

// PrintText renders the summary as a terminal table.
func PrintText(w io.Writer, s Summary) error {
	var b strings.Builder
	fmt.Fprintf(&b, "DeployIQ fleet scan: %d projects\n\n", s.Projects)
	fmt.Fprintf(&b, "%-44s %-7s %10s %10s %7s  %s\n", "PROJECT", "STACK", "CURRENT", "OPTIMIZED", "SAVING", "STATUS")
	for _, e := range s.Entries {
		stack := "-"
		if e.Report != nil {
			stack = e.Report.Stack
		}
		cur, opt, pct := "-", "-", "-"
		if e.Report != nil {
			cur, opt, pct = fmtMB(e.BeforeMB()), fmtMB(e.AfterMB()), fmt.Sprintf("%.0f%%", e.SavedPercent())
		}
		status := e.Status
		if e.Reason != "" {
			status += ": " + truncate(e.Reason, 70)
		}
		fmt.Fprintf(&b, "%-44s %-7s %10s %10s %7s  %s\n", truncate(e.Name, 44), stack, cur, opt, pct, status)
	}
	fmt.Fprintf(&b, "\nAnalysed %d of %d (%d skipped, %d errors). %d could be smaller.\n", s.Analysed, s.Projects, s.Skipped, s.Errors, s.Improvable)
	fmt.Fprintf(&b, "Total image size: %s now, %s optimized: %s saved (average %.0f%% per project).\n",
		fmtMB(s.BeforeMB), fmtMB(s.AfterMB), fmtMB(s.SavedMB), s.AvgSavedPct)
	if s.CriticalVulnsRemoved+s.HighVulnsRemoved > 0 {
		fmt.Fprintf(&b, "Known vulnerabilities removed: %d critical, %d high.\n", s.CriticalVulnsRemoved, s.HighVulnsRemoved)
	}
	b.WriteString("Sizes are estimates unless -measure was used.\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// PrintMarkdown renders the summary for a report to share with leadership.
func PrintMarkdown(w io.Writer, s Summary) error {
	var b strings.Builder
	b.WriteString("# DeployIQ fleet report\n\n")
	fmt.Fprintf(&b, "- **Projects:** %d analysed of %d (%d skipped, %d errors)\n", s.Analysed, s.Projects, s.Skipped, s.Errors)
	fmt.Fprintf(&b, "- **Could be smaller:** %d\n", s.Improvable)
	fmt.Fprintf(&b, "- **Total image size:** %s → %s (**%s saved**, average %.0f%% per project)\n",
		fmtMB(s.BeforeMB), fmtMB(s.AfterMB), fmtMB(s.SavedMB), s.AvgSavedPct)
	if s.CriticalVulnsRemoved+s.HighVulnsRemoved > 0 {
		fmt.Fprintf(&b, "- **Known vulnerabilities removed:** %d critical, %d high\n", s.CriticalVulnsRemoved, s.HighVulnsRemoved)
	}
	b.WriteString("\n| Project | Stack | Current | Optimized | Saving | Status |\n|---|---|---:|---:|---:|---|\n")
	for _, e := range s.Entries {
		stack, cur, opt, pct := "—", "—", "—", "—"
		if e.Report != nil {
			stack, cur, opt, pct = e.Report.Stack, fmtMB(e.BeforeMB()), fmtMB(e.AfterMB()), fmt.Sprintf("%.0f%%", e.SavedPercent())
		}
		status := e.Status
		if e.Reason != "" {
			status += " — " + strings.ReplaceAll(truncate(e.Reason, 120), "|", "/")
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s |\n", e.Name, stack, cur, opt, pct, status)
	}
	b.WriteString("\n_Sizes are estimates unless measured with `-measure`. Run `doiq -write` in a project to get its Dockerfile.optimized._\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// PrintJSON renders the summary with every project's full report.
func PrintJSON(w io.Writer, s Summary) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(s)
}

// PrintCSV renders one row per project, for spreadsheets.
func PrintCSV(w io.Writer, s Summary) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"project", "path", "stack", "status", "reason", "current_mb", "optimized_mb", "saved_mb", "saving_percent", "measured", "rules"})
	for _, e := range s.Entries {
		stack, measured, rules := "", "false", ""
		if e.Report != nil {
			stack = e.Report.Stack
			measured = fmt.Sprint(e.Report.Measured != nil)
			rules = strings.Join(e.Report.RulesApplied, " ")
		}
		_ = cw.Write([]string{e.Name, e.Path, stack, e.Status, e.Reason,
			fmt.Sprintf("%.0f", e.BeforeMB()), fmt.Sprintf("%.0f", e.AfterMB()),
			fmt.Sprintf("%.0f", e.SavedMB()), fmt.Sprintf("%.1f", e.SavedPercent()), measured, rules})
	}
	cw.Flush()
	return cw.Error()
}

func fmtMB(v float64) string {
	switch {
	case v >= 1024*1024:
		return fmt.Sprintf("%.1f TB", v/(1024*1024))
	case v >= 1024:
		return fmt.Sprintf("%.1f GB", v/1024)
	}
	return fmt.Sprintf("%.0f MB", v)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
