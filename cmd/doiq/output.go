package main

import (
	"fmt"
	"os"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/report"
)

// writeReportFiles writes -json-out and -markdown-out. These paths are chosen
// by whoever runs doiq (a CI job, typically under $RUNNER_TEMP); they are not
// the repository's Dockerfile, so they need no confirmation.
func writeReportFiles(rep *report.Report, opts *options) error {
	write := func(path string, render func(f *os.File) error) error {
		if path == "" {
			return nil
		}
		f, err := os.Create(path) //nolint:gosec // operator-chosen output path
		if err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		if err := render(f); err != nil {
			f.Close()
			return fmt.Errorf("writing %s: %w", path, err)
		}
		return f.Close()
	}
	if err := write(opts.jsonOut, func(f *os.File) error { return report.PrintJSON(f, rep) }); err != nil {
		return err
	}
	return write(opts.markdownOut, func(f *os.File) error { return report.PrintMarkdown(f, rep) })
}
