package dockerfile

import (
	"regexp"
	"strings"
)

// CommandKind classifies one shell command inside a RUN instruction.
type CommandKind int

// Command kinds. The rule engine regenerates everything except CommandCustom,
// which it carries over verbatim — dropping a step it does not understand is
// how a generated Dockerfile silently breaks a build (Phase 0.10).
const (
	CommandCustom        CommandKind = iota
	CommandHousekeeping              // apt-get update, cache cleanup, repo setup
	CommandSystemInstall             // apt-get/apk/yum/dnf install
	CommandDepsInstall               // npm ci, pip install, go mod download, ...
	CommandBuild                     // npm run build, go build, ...
)

// Command is one classified shell command.
type Command struct {
	Kind CommandKind
	Text string

	// Packages is set for CommandSystemInstall.
	Packages []string

	// ProdOnly is set for a Node or pip install that already excludes dev
	// dependencies.
	ProdOnly bool

	// NoCache is set for a pip install with --no-cache-dir or an apk add
	// with --no-cache.
	NoCache bool

	// Output is the -o target of a go build, if any.
	Output string
}

// RunCommand is a classified RUN instruction.
type RunCommand struct {
	Stage       int
	Instruction Instruction
	Commands    []Command
}

// Analysis is what the rule engine needs to know about an existing
// Dockerfile, extracted once.
type Analysis struct {
	Runs []RunCommand

	// SystemPackages are all OS packages installed, in order, de-duplicated.
	SystemPackages []string

	// PackageManager is apt, apk, dnf or "" when no system install happens.
	PackageManager string

	// DevDepsInstalled is true when a Node or pip install did not restrict
	// itself to production dependencies.
	DevDepsInstalled bool

	// PipCacheDisabled is true when pip's cache is off (flag or env).
	PipCacheDisabled bool

	// AptListsCleaned is true when /var/lib/apt/lists is removed.
	AptListsCleaned bool

	// GoBuildOutput is the -o path of the last go build, if any.
	GoBuildOutput string

	// GoBuildStage is the stage index of that go build.
	GoBuildStage int

	// Custom lists commands the rule engine does not regenerate, with the
	// stage they ran in.
	Custom []CustomCommand
}

// CustomCommand is a RUN instruction carried over unchanged because it does
// something the rule engine does not regenerate.
type CustomCommand struct {
	Stage       int
	Instruction Instruction
}

// trivialRe matches commands with no lasting effect on the image; a RUN made
// only of these and recognised steps is not worth carrying over.
var trivialRe = regexp.MustCompile(`^(cd|echo|ls|pwd|true|set|whoami|env|printenv|cat|mkdir\s+-p\s+/app)\b`)

var splitRe = regexp.MustCompile(`\s*(?:&&|;|\|\|)\s*`)

// Analyze classifies every RUN in the Dockerfile.
func Analyze(df *Dockerfile) *Analysis {
	a := &Analysis{GoBuildStage: -1}
	seen := map[string]bool{}

	for si, st := range df.Stages {
		for _, kv := range st.Env() {
			if strings.EqualFold(kv[0], "PIP_NO_CACHE_DIR") && kv[1] != "" && kv[1] != "0" && !strings.EqualFold(kv[1], "false") {
				a.PipCacheDisabled = true
			}
		}
		for _, in := range st.All("RUN") {
			text := in.Args
			if in.IsExec() {
				text = strings.Join(in.Exec, " ")
				// ["sh","-c","..."] is a shell command wearing exec form.
				if len(in.Exec) >= 3 && (in.Exec[0] == "sh" || in.Exec[0] == "/bin/sh" || in.Exec[0] == "bash" || in.Exec[0] == "/bin/bash") && in.Exec[1] == "-c" {
					text = in.Exec[2]
				}
			}
			rc := RunCommand{Stage: si, Instruction: in}
			hasCustom := false
			for _, part := range splitRe.Split(text, -1) {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				c := classify(part)
				rc.Commands = append(rc.Commands, c)

				switch c.Kind {
				case CommandSystemInstall:
					for _, p := range c.Packages {
						if !seen[p] {
							seen[p] = true
							a.SystemPackages = append(a.SystemPackages, p)
						}
					}
					if a.PackageManager == "" {
						a.PackageManager = packageManagerOf(part)
					}
				case CommandDepsInstall:
					if hasDevDependencyConcept(part) && !c.ProdOnly {
						a.DevDepsInstalled = true
					}
					if c.NoCache {
						a.PipCacheDisabled = true
					}
				case CommandBuild:
					if c.Output != "" {
						a.GoBuildOutput = c.Output
						a.GoBuildStage = si
					}
				case CommandCustom:
					if !trivialRe.MatchString(part) {
						hasCustom = true
					}
				case CommandHousekeeping:
					if strings.Contains(part, "/var/lib/apt/lists") {
						a.AptListsCleaned = true
					}
				}
			}
			// The whole instruction is carried, not the fragment: a custom
			// step usually depends on its neighbours (`cd scripts &&
			// ./setup.sh`), and re-running an install it also contains is
			// idempotent, while dropping its context is not.
			if hasCustom {
				a.Custom = append(a.Custom, CustomCommand{Stage: si, Instruction: in})
			}
			a.Runs = append(a.Runs, rc)
		}
	}
	return a
}

var (
	sysInstallRe = regexp.MustCompile(`^(?:sudo\s+)?(?:DEBIAN_FRONTEND=\S+\s+)?(apt-get|apt|apk|yum|dnf|microdnf)\s+(?:.*\s)?(install|add)\b(.*)$`)
	// Bare `yarn` is an install; `yarn build` or `yarn add x` is not, so for
	// yarn only flags may follow.
	nodeDepsRe  = regexp.MustCompile(`^(npm\s+(ci|install|i)(\s|$)|yarn(\s+install)?(\s+-\S+)*\s*$|pnpm\s+(install|i)(\s|$))`)
	pipRe       = regexp.MustCompile(`^(?:python[0-9.]*\s+-m\s+)?pip[0-9.]*\s+install\b`)
	poetryRe    = regexp.MustCompile(`^poetry\s+install\b`)
	goDepsRe    = regexp.MustCompile(`^go\s+mod\s+(download|tidy|vendor|verify)\b`)
	goBuildRe   = regexp.MustCompile(`^(?:\S+=\S+\s+)*go\s+(build|install)\b`)
	goOutRe     = regexp.MustCompile(`\s-o[\s=]+(\S+)`)
	nodeBuildRe = regexp.MustCompile(`^(npm\s+run|yarn(\s+run)?|pnpm(\s+run)?)\s+build\b`)
	houseRe     = regexp.MustCompile(`^(?:sudo\s+)?(?:apt-get|apt|apk|yum|dnf|microdnf)\s+(update|upgrade|clean|autoremove|-y\s+update)|^rm\s+-[rf]+\s+(/var/lib/apt/lists|/var/cache|/root/\.cache|/tmp|~/.cache)|^(npm|yarn)\s+cache\s+clean|^curl\s.*(nodesource|deb\.nodesource)|^pip[0-9.]*\s+install\s+(--upgrade|-U)\s+pip\s*$|^python[0-9.]*\s+-m\s+pip\s+install\s+(--upgrade|-U)\s+pip\s*$|^corepack\s+enable|^npm\s+(install|i)\s+-g\s+(npm|yarn|pnpm)(@\S+)?\s*$`)
)

func classify(cmd string) Command {
	c := Command{Kind: CommandCustom, Text: cmd}
	switch {
	case houseRe.MatchString(cmd):
		c.Kind = CommandHousekeeping
	case sysInstallRe.MatchString(cmd):
		m := sysInstallRe.FindStringSubmatch(cmd)
		c.Kind = CommandSystemInstall
		for _, f := range strings.Fields(m[3]) {
			if strings.HasPrefix(f, "-") || strings.Contains(f, "$") {
				continue
			}
			c.Packages = append(c.Packages, f)
		}
		c.NoCache = strings.Contains(cmd, "--no-cache")
	case nodeDepsRe.MatchString(cmd) && !strings.Contains(cmd, " -g ") && !strings.Contains(cmd, " --global"):
		c.Kind = CommandDepsInstall
		c.ProdOnly = strings.Contains(cmd, "--production") || strings.Contains(cmd, "--omit=dev") ||
			strings.Contains(cmd, "--only=prod") || strings.Contains(cmd, "--prod")
	case pipRe.MatchString(cmd) || poetryRe.MatchString(cmd):
		c.Kind = CommandDepsInstall
		c.NoCache = strings.Contains(cmd, "--no-cache-dir")
		// pip has no dev-dependency concept; only poetry does.
		c.ProdOnly = !poetryRe.MatchString(cmd) || strings.Contains(cmd, "--only main") || strings.Contains(cmd, "--without dev")
	case goDepsRe.MatchString(cmd):
		c.Kind = CommandDepsInstall
		c.ProdOnly = true
	case goBuildRe.MatchString(cmd):
		c.Kind = CommandBuild
		if m := goOutRe.FindStringSubmatch(cmd); m != nil {
			c.Output = m[1]
		}
	case nodeBuildRe.MatchString(cmd):
		c.Kind = CommandBuild
	}
	return c
}

// hasDevDependencyConcept reports installers that distinguish dev from
// production dependencies. pip does not, so a pip install never counts as
// installing dev dependencies.
func hasDevDependencyConcept(cmd string) bool {
	return nodeDepsRe.MatchString(cmd) || poetryRe.MatchString(cmd)
}

func packageManagerOf(cmd string) string {
	switch {
	case strings.Contains(cmd, "apk "):
		return "apk"
	case strings.Contains(cmd, "dnf ") || strings.Contains(cmd, "yum ") || strings.Contains(cmd, "microdnf "):
		return "dnf"
	}
	return "apt"
}
