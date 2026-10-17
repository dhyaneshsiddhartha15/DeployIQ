// Command doiq is the DeployIQ Optimizer CLI — the v1 product (Phase 4.1).
//
// Point it at a repo, get back a smaller Dockerfile. It runs entirely locally:
// no account, no network calls, no telemetry by default (Phase 0.6).
//
// This file is the entrypoint only — flag parsing, input validation and
// orchestration. Every stage of the pipeline lives in its own package under
// internal/:
//
//	detector → dockerfile → rules → analyzer → report
//
// The only network code is behind `doiq login` and the -report flag, both
// explicit opt-ins (Phase 4.6). Without them the CLI makes no network call at
// all (FR-8).
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/report"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/rules"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/logger"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/version"
)

// errStackUnknown maps to its own exit code so CI can distinguish "this repo
// isn't supported" from "the tool broke" (Phase 1.3, US-3).
var errStackUnknown = errors.New("Could not detect a supported stack in this repo") //nolint:revive,stylecheck // US-3 fixes this exact user-facing text

// errNotConfirmed is returned when a write was requested but not confirmed.
var errNotConfirmed = errors.New("nothing was written")

// options is the parsed command line.
type options struct {
	// path is the repository to analyse. Defaults to the working directory
	// so `doiq` alone works, matching Phase 1.2's zero-flag requirement.
	path string

	// write permits the CLI to modify the filesystem. Off by default:
	// FR-5 makes dry-run the default mode and FR-6/US-2 forbid touching a
	// file without an explicit opt-in.
	write bool

	// dryRun is the explicit spelling of the default. Accepted so
	// `--dry-run` (FR-6) works; combining it with -write is a usage error.
	dryRun bool

	// yes skips the interactive confirmation. Only meaningful with -write,
	// and exists for CI, where there is no one to prompt.
	yes bool

	// format is text or json.
	format string

	// diff shows a unified diff against the existing Dockerfile.
	diff bool

	// dockerfile is the existing Dockerfile's path relative to the repo,
	// when it is not in one of the places Parse looks.
	dockerfile string

	// quiet omits the generated Dockerfile from text output.
	quiet bool

	// report sends the result to the API (opt-in, Phase 4.6).
	report   bool
	repoName string
	api      string

	// verbose raises the log level to debug. Logs go to stderr, keeping
	// stdout a clean channel for the report.
	verbose bool

	// showVersion prints build metadata and exits.
	showVersion bool
}

// env is the process environment the CLI runs in, injectable for tests.
type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	// interactive reports whether stdin is a terminal a person can answer
	// a prompt on.
	interactive bool
}

// stdlib flag, not cobra or urfave/cli. The surface is a handful of booleans
// and a positional path; a CLI framework would add a dependency and a
// build-size cost to a single static binary (Phase 0.7) for syntax sugar.
// Phase 1.7 defers a richer interactive TUI outright.
func parseFlags(args []string, stderr io.Writer) (*options, error) {
	opts := &options{}

	fs := flag.NewFlagSet(constants.AppName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&opts.write, "write", false, "write "+constants.DockerfileOutputName+" after confirmation (default: dry run, nothing is modified)")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "explicitly request the default: analyse and print, write nothing")
	fs.BoolVar(&opts.yes, "yes", false, "confirm -write without prompting (for CI)")
	fs.StringVar(&opts.format, "format", "text", "output format: text or json")
	fs.BoolVar(&opts.diff, "diff", false, "show a unified diff against the existing Dockerfile instead of the full file")
	fs.StringVar(&opts.dockerfile, "dockerfile", "", "existing Dockerfile, relative to the repo (default: ./Dockerfile, then build/, docker/, deploy/)")
	fs.BoolVar(&opts.quiet, "quiet", false, "omit the generated Dockerfile from the text report")
	fs.BoolVar(&opts.report, "report", false, "send the result to your DeployIQ dashboard (requires `"+constants.AppName+" login`)")
	fs.StringVar(&opts.repoName, "repo-name", "", "repository name to report (default: from the git origin remote, else the directory name)")
	fs.StringVar(&opts.api, "api", "", "API endpoint for -report and login (default "+constants.DefaultAPIEndpoint+")")
	fs.BoolVar(&opts.verbose, "verbose", false, "log debug detail to stderr")
	fs.BoolVar(&opts.showVersion, "version", false, "print version information and exit")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "Usage: %s [flags] [path]\n", constants.AppName)
		fmt.Fprintf(out, "       %s login | logout | whoami\n\n", constants.AppName)
		fmt.Fprintf(out, "Analyse a repository and generate an optimized Dockerfile.\n")
		fmt.Fprintf(out, "Nothing is written to disk unless -write is given, and nothing leaves\n")
		fmt.Fprintf(out, "this machine unless you log in and pass -report.\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(out, "\nExit codes: 0 ok, 1 error, 2 usage, 3 stack not detected, 4 unsupported repo or Dockerfile.\n")
	}

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if opts.write && opts.dryRun {
		fmt.Fprintln(stderr, "-write and -dry-run are mutually exclusive")
		return nil, errUsage
	}
	if opts.format != "text" && opts.format != "json" {
		fmt.Fprintf(stderr, "-format must be text or json, got %q\n", opts.format)
		return nil, errUsage
	}

	switch fs.NArg() {
	case 0:
		opts.path = "."
	case 1:
		opts.path = fs.Arg(0)
	default:
		fmt.Fprintf(stderr, "expected at most one path, got %d\n", fs.NArg())
		return nil, errUsage
	}
	return opts, nil
}

var errUsage = errors.New("usage")

// validatePath resolves the target and confirms it is a readable directory.
//
// A trust boundary, and Phase 9.1 names it explicitly: the repo path is the
// CLI's only untrusted input. Resolving to an absolute path here means every
// downstream package works from one normalised value rather than re-deriving it
// from a relative string. A missing or unreadable path is a clear filesystem
// error and a non-zero exit (Phase 1.4).
func validatePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	if _, err := os.ReadDir(abs); err != nil {
		return "", fmt.Errorf("reading %s: %w", abs, err)
	}
	return abs, nil
}

func main() {
	os.Exit(run(os.Args[1:], defaultEnv()))
}

func defaultEnv() env {
	interactive := false
	if fi, err := os.Stdin.Stat(); err == nil {
		interactive = fi.Mode()&os.ModeCharDevice != 0
	}
	return env{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, interactive: interactive}
}

// run returns the process exit code. Split from main so it is testable and so
// every deferred call still runs before exit.
func run(args []string, e env) int {
	if len(args) > 0 {
		switch args[0] {
		case "login", "logout", "whoami":
			return runAccount(args[0], args[1:], e)
		}
	}

	opts, err := parseFlags(args, e.stderr)
	if err != nil {
		// flag already printed the reason and the usage text.
		if errors.Is(err, flag.ErrHelp) {
			return constants.ExitOK
		}
		return constants.ExitUsage
	}

	if opts.showVersion {
		fmt.Fprintf(e.stdout, "%s %s\n", constants.AppName, version.String())
		return constants.ExitOK
	}

	level := "warn"
	if opts.verbose {
		level = "debug"
	}
	if err := logger.Init(logger.Options{Level: level, Format: logger.FormatText, Output: e.stderr}); err != nil {
		fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
		return constants.ExitFailure
	}

	root, err := validatePath(opts.path)
	if err != nil {
		fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
		return constants.ExitFailure
	}

	if err := analyse(root, opts, e); err != nil {
		switch {
		case errors.Is(err, errStackUnknown):
			fmt.Fprintf(e.stderr, "%s\n", errStackUnknown)
			fmt.Fprintf(e.stderr, "Looked for: %s, %s, %s, %s (supported stacks: %s).\n",
				constants.ManifestPackageJSON, constants.ManifestGoMod, constants.ManifestRequirementsTxt,
				constants.ManifestPyProjectTOML, strings.Join(constants.SupportedStacks, ", "))
			return constants.ExitStackUnknown
		case errors.Is(err, dockerfile.ErrUnsupported), errors.Is(err, rules.ErrUnsupported):
			fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
			fmt.Fprintf(e.stderr, "Nothing was generated. DeployIQ refuses to guess rather than risk a Dockerfile that does not build.\n")
			return constants.ExitUnsupported
		case errors.Is(err, errNotConfirmed):
			fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
			return constants.ExitOK
		}
		fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
		return constants.ExitFailure
	}
	return constants.ExitOK
}

// analyse runs the optimization pipeline over the repository at root.
func analyse(root string, opts *options, e env) error {
	start := time.Now()

	det, err := detector.Detect(root) // FR-1
	if errors.Is(err, detector.ErrNoStack) {
		return errStackUnknown // FR-7, US-3
	}
	if err != nil {
		return err
	}
	slog.Debug("stack detected", slog.String("stack", det.Stack), slog.Any("others", det.Others))

	// FR-2; nil when absent. -dockerfile names one explicitly; otherwise
	// the root and the conventional build/, docker/, deploy/ dirs are tried.
	var existing *dockerfile.Dockerfile
	if opts.dockerfile != "" {
		existing, err = dockerfile.ParseFile(root, opts.dockerfile)
	} else {
		existing, err = dockerfile.Parse(root)
	}
	if err != nil {
		return err
	}

	res, err := rules.Generate(&rules.Context{Root: root, Detection: det, Existing: existing}) // FR-3
	if err != nil {
		return err
	}

	existingDesc, existingRaw := "", ""
	if existing != nil {
		existingDesc = describeExisting(existing)
		existingRaw = string(existing.Raw)
	}
	rep := report.New(root, det.Stack, det.Manifest, det.Others, stackDetail(det), existingDesc, existingRaw, res) // FR-4
	slog.Debug("analysis complete", slog.Duration("took", time.Since(start)))

	if opts.format == "json" {
		if err := report.PrintJSON(e.stdout, rep); err != nil {
			return err
		}
	} else {
		if err := report.Print(e.stdout, rep, report.Options{Diff: opts.diff, Quiet: opts.quiet}); err != nil {
			return err
		}
	}

	// Then, and only then, honour -write (FR-5, FR-6).
	if opts.write {
		if err := writeOutput(root, res.Dockerfile, opts, e); err != nil {
			return err
		}
	} else if opts.format == "text" {
		fmt.Fprintf(e.stdout, "\nDry run: nothing was written. Re-run with -write to save %s.\n", constants.DockerfileOutputName)
	}

	if opts.report {
		return sendReport(root, rep, opts, e)
	}
	return nil
}

// writeOutput writes Dockerfile.optimized after confirmation. It never touches
// the existing Dockerfile: the output goes next to it, so the user can compare
// and switch deliberately.
func writeOutput(root string, content []byte, opts *options, e env) error {
	target := filepath.Join(root, constants.DockerfileOutputName)
	_, statErr := os.Lstat(target)
	exists := statErr == nil

	if !opts.yes {
		if !e.interactive {
			return fmt.Errorf("%w: -write needs confirmation, and stdin is not a terminal; pass -yes to confirm non-interactively", errNotConfirmed)
		}
		verb := "Write"
		if exists {
			verb = "Overwrite existing"
		}
		fmt.Fprintf(e.stderr, "%s %s? [y/N] ", verb, target)
		answer, _ := bufio.NewReader(e.stdin).ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			return errNotConfirmed
		}
	}

	if exists {
		info, err := os.Lstat(target)
		if err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to overwrite %s: not a regular file", target)
		}
	}

	// Write to a temp file and rename, so an interrupted write never leaves
	// a truncated Dockerfile.optimized behind.
	tmp, err := os.CreateTemp(root, ".doiq-*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", target, err)
	}
	// CreateTemp makes the file 0600; a Dockerfile is not secret.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", target, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}
	fmt.Fprintf(e.stderr, "Wrote %s\nBuild it with: docker build -f %s %s\n", target, constants.DockerfileOutputName, root)
	return nil
}

func describeExisting(df *dockerfile.Dockerfile) string {
	stages := "1 stage"
	if n := len(df.Stages); n > 1 {
		stages = fmt.Sprintf("%d stages", n)
	}
	name := df.RelPath
	if name == "" {
		name = constants.DockerfileName
	}
	return fmt.Sprintf("%s (%s, runtime base %s)", name, stages, df.RuntimeImage().Ref)
}

func stackDetail(det *detector.Result) string {
	switch {
	case det.Node != nil:
		d := det.Node.PackageManager
		if det.Node.Lockfile != "" {
			d += ", " + det.Node.Lockfile
		} else {
			d += ", no lockfile"
		}
		return d
	case det.Go != nil:
		d := "module " + det.Go.Module
		if det.Go.GoVersion != "" {
			d += ", go " + det.Go.GoVersion
		}
		return d
	case det.Python != nil:
		d := det.Python.Tool
		if det.Python.Version != "" {
			d += ", python " + det.Python.Version
		}
		return d
	}
	return ""
}
