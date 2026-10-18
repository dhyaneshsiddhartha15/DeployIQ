package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/client"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/report"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/config"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// cliConfigPath is overridable in tests so they never touch the real user
// config directory.
var cliConfigPath = config.CLIConfigPath

// openBrowser is overridable in tests.
var openBrowser = defaultOpenBrowser

// loginTimeout bounds how long `doiq login` waits for the browser round trip.
const loginTimeout = 5 * time.Minute

// runAccount handles the login, logout and whoami subcommands — the opt-in
// half of the CLI (Phase 5.3). None of them is ever invoked implicitly.
func runAccount(cmd string, args []string, e env) int {
	fs := flag.NewFlagSet(constants.AppName+" "+cmd, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	api := fs.String("api", "", "API endpoint (default "+constants.DefaultAPIEndpoint+")")
	noBrowser := fs.Bool("no-browser", false, "print the login URL instead of opening a browser")
	sso := fs.Bool("sso", false, "sign in with your company identity provider (OIDC) instead of GitHub")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return constants.ExitOK
		}
		return constants.ExitUsage
	}

	path, err := cliConfigPath()
	if err != nil {
		fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
		return constants.ExitFailure
	}
	cfg, err := config.LoadCLIConfig(path)
	if err != nil {
		fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
		return constants.ExitFailure
	}
	endpoint := resolveEndpoint(*api, cfg)

	switch cmd {
	case "logout":
		cfg.SessionToken = ""
		if err := config.SaveCLIConfig(path, cfg); err != nil {
			fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
			return constants.ExitFailure
		}
		fmt.Fprintln(e.stdout, "Logged out. The local session token was removed.")
		return constants.ExitOK

	case "whoami":
		if cfg.SessionToken == "" {
			fmt.Fprintln(e.stderr, client.ErrUnauthenticated)
			return constants.ExitFailure
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		me, err := client.New(endpoint, cfg.SessionToken).Me(ctx)
		if err != nil {
			fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
			return constants.ExitFailure
		}
		who := "GitHub user " + me.GitHubID
		if me.Login != "" {
			who = "@" + me.Login
		}
		if me.Email != "" {
			who += " <" + me.Email + ">"
		}
		fmt.Fprintf(e.stdout, "Logged in to %s as %s (plan: %s)\n", endpoint, who, me.Plan)
		return constants.ExitOK

	default: // login
		ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
		defer cancel()
		opener := openBrowser
		if *noBrowser {
			opener = nil
		}
		provider := "github"
		if *sso {
			provider = "oidc"
		}
		token, err := client.LoginWith(ctx, endpoint, provider, opener, func(u string) {
			if u == "" {
				fmt.Fprintln(e.stderr, "Could not open a browser; open the URL above manually.")
				return
			}
			fmt.Fprintf(e.stderr, "Opening the login page in your browser. If it does not open, visit:\n  %s\n", u)
		})
		if err != nil {
			fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
			return constants.ExitFailure
		}
		cfg.SessionToken = token
		if *api != "" {
			cfg.APIEndpoint = *api
		}
		if err := config.SaveCLIConfig(path, cfg); err != nil {
			fmt.Fprintf(e.stderr, "%s: %v\n", constants.AppName, err)
			return constants.ExitFailure
		}
		fmt.Fprintf(e.stdout, "Logged in. Session stored in %s\nAdd -report to a run to send its result to your dashboard.\n", path)
		return constants.ExitOK
	}
}

// resolveEndpoint picks the API endpoint: flag, then DOIQ_API_URL, then the
// endpoint saved at login, then the default.
func resolveEndpoint(flagValue string, cfg *config.CLIConfig) string {
	switch {
	case flagValue != "":
		return flagValue
	case os.Getenv("DOIQ_API_URL") != "":
		return os.Getenv("DOIQ_API_URL")
	case cfg != nil && cfg.APIEndpoint != "":
		return cfg.APIEndpoint
	}
	return constants.DefaultAPIEndpoint
}

// sendReport posts the run's result to the API. Only reached with -report.
// What is sent is exactly the Phase 4.5 body — repo name, stack, the two size
// estimates and rule ids. No source, no Dockerfile, no paths.
func sendReport(root string, rep *report.Report, opts *options, e env) error {
	path, err := cliConfigPath()
	if err != nil {
		return err
	}
	cfg, err := config.LoadCLIConfig(path)
	if err != nil {
		return err
	}
	if cfg.SessionToken == "" {
		return client.ErrUnauthenticated
	}
	name := opts.repoName
	if name == "" {
		name = repoName(root)
	}
	body := model.OptimizationReport{
		RepoName:           name,
		DetectedStack:      rep.Stack,
		OriginalEstSizeMB:  int64(rep.Estimate.Before.TotalMB),
		OptimizedEstSizeMB: int64(rep.Estimate.After.TotalMB),
		RulesApplied:       rep.RulesApplied,
		Org:                opts.org,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	saved, err := client.New(resolveEndpoint(opts.api, cfg), cfg.SessionToken).ReportOptimization(ctx, body)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stderr, "Reported to your dashboard as %s (id %s).\n", saved.RepoName, saved.ID)
	return nil
}

var remoteRe = regexp.MustCompile(`(?:github\.com|gitlab\.com|bitbucket\.org|[\w.-]+)[:/]([\w.-]+/[\w.-]+?)(?:\.git)?/?$`)

// repoName derives "owner/repo" from the origin remote in .git/config, read as
// a file — git is never executed (Phase 9.1). Falls back to the directory name.
func repoName(root string) string {
	f, err := os.Open(filepath.Join(root, ".git", "config"))
	if err == nil {
		defer f.Close()
		inOrigin := false
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "[") {
				inOrigin = line == `[remote "origin"]`
				continue
			}
			if inOrigin && strings.HasPrefix(line, "url") {
				_, u, _ := strings.Cut(line, "=")
				if m := remoteRe.FindStringSubmatch(strings.TrimSpace(u)); m != nil {
					return m[1]
				}
			}
		}
	}
	return filepath.Base(root)
}

// defaultOpenBrowser launches the platform's URL handler with a fixed program
// and our own URL as its only argument — no shell, nothing from the repository.
func defaultOpenBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}
