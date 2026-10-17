package rules

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/analyzer"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// DefaultGoVersion is used when go.mod names no usable version.
const DefaultGoVersion = "1.24"

const defaultGoPort = "8080"

// Runtime images for Go binaries. Debian 12 based throughout, so a cgo binary
// built on golang:*-bookworm links against the glibc it will run on.
const (
	goDistrolessStatic = "gcr.io/distroless/static-debian12"
	goDistrolessBase   = "gcr.io/distroless/base-debian12"
	goAlpineRuntime    = "alpine:3.20"
	goDebianRuntime    = "debian:bookworm-slim"
)

// goAssetDirs are conventional locations of files a Go server reads at
// runtime. A single-stage image had them because it had everything; a binary-
// only image does not, so they are copied unless the repo embeds its assets.
var goAssetDirs = []string{"templates", "static", "public", "assets", "web", "views", "migrations", "config", "configs", "locales", "i18n"}

type goGenerator struct{}

func (goGenerator) Stack() string { return constants.StackGo }

func (goGenerator) Generate(ctx *Context) (*Result, error) {
	g := ctx.Detection.Go
	c, err := carry(ctx, "/app")
	if err != nil {
		return nil, err
	}
	var notes []string
	had := map[string]bool{}

	// --- what to build --------------------------------------------------------
	existingBuild := existingGoBuild(c)
	pkg := ""
	if existingBuild != "" {
		pkg = goBuildPackage(existingBuild)
	}
	if pkg == "" {
		pkg = g.MainPackage()
	}
	if pkg == "" {
		if len(g.MainPackages) == 0 {
			return nil, unsupported("no package main found within %d directory levels of the repository root", 3)
		}
		return nil, unsupported("several main packages found (%s) and none is the obvious one; add a Dockerfile whose `go build` names the package to build", strings.Join(g.MainPackages, ", "))
	}
	if !strings.HasPrefix(pkg, ".") {
		pkg = "./" + pkg
	}
	name := binaryName(g.Module, pkg)

	// --- version --------------------------------------------------------------
	goVersion := g.GoVersion
	if v := existingGoImageVersion(ctx.Existing); v != "" && versionLess(goVersion, v) {
		goVersion = v
	}
	if goVersion == "" {
		goVersion = DefaultGoVersion
		notes = append(notes, "go.mod names no Go version; building with Go "+DefaultGoVersion+".")
	}

	// An existing multi-stage build whose runtime is already a lean OS or
	// distroless image keeps that runtime stage verbatim: it is where pinned
	// packages, users and config files live, and rewriting it is all risk
	// and no saving. Only the build stage is improved.
	setup := collectBuildSetup(c)
	if keepsRuntime(c) {
		return goKeepRuntime(c, g, setup, name, pkg, goVersion, existingBuild, notes)
	}

	// --- how it starts ----------------------------------------------------------
	start, err := goStart(c, name)
	if err != nil {
		return nil, err
	}
	notes = append(notes, start.notes...)

	ports := c.ports
	if len(ports) == 0 {
		ports = []string{defaultGoPort}
		notes = append(notes, "No EXPOSE found; documenting port "+defaultGoPort+". EXPOSE does not publish anything, so a wrong guess is harmless.")
	}

	// --- runtime image ------------------------------------------------------------
	runtimePkgs := c.runtimePackages()
	carriedPkgs := len(runtimePkgs) > 0
	tools := c.requiredTools(ctx.Root)
	needOS := start.needsShell || len(c.custom) > 0 || carriedPkgs || len(tools) > 0

	var runtimeImage, osFamily string
	switch {
	case needOS && (g.UsesCgo || c.packageManager == "apt"):
		runtimeImage, osFamily = goDebianRuntime, "debian"
		runtimePkgs = addMissing(runtimePkgs, "ca-certificates", "tzdata")
	case needOS:
		runtimeImage, osFamily = goAlpineRuntime, "alpine"
		if c.packageManager == "dnf" && len(runtimePkgs) > 0 {
			return nil, unsupported("the existing image installs RHEL-family packages (%s); translating them is not supported", strings.Join(runtimePkgs, " "))
		}
		runtimePkgs = addMissing(runtimePkgs, "ca-certificates", "tzdata")
	case g.UsesCgo:
		runtimeImage = goDistrolessBase
	default:
		runtimeImage = goDistrolessStatic
	}
	if needOS {
		var why []string
		if start.needsShell {
			why = append(why, "the start command needs a shell")
		}
		if len(c.custom) > 0 {
			why = append(why, "carried RUN steps need one")
		}
		if carriedPkgs {
			why = append(why, "the existing image installs runtime OS packages")
		}
		if len(tools) > 0 {
			runtimePkgs = addMissing(runtimePkgs, tools...)
			why = append(why, "the start command or health check uses "+strings.Join(tools, ", "))
		}
		notes = append(notes, "Using "+runtimeImage+" rather than distroless: "+strings.Join(why, "; ")+".")
	}
	if g.UsesCgo {
		notes = append(notes, "cgo is in use (import \"C\"), so the binary is dynamically linked against glibc; built on Debian 12 to match the runtime.")
	}

	distroless := runtimeImage == goDistrolessStatic || runtimeImage == goDistrolessBase
	nonRoot := false
	switch {
	case lowPort(ports):
		notes = append(notes, "The app exposes a port below 1024, which an unprivileged user cannot bind; the container keeps running as root.")
	case c.user != "" && userIsRoot(c.user):
		notes = append(notes, "The existing Dockerfile explicitly runs as root; that is kept.")
	default:
		nonRoot = true
		if c.user != "" {
			had[RuleNonRootUser] = true
		}
	}
	if distroless && nonRoot {
		runtimeImage += ":nonroot"
	}

	// --- build stage ----------------------------------------------------------------
	f := &dockerfile.File{Header: header(constants.StackGo, c.existing)}
	notes = append(notes, goBuildStage(f, c, g, setup, name, pkg, goVersion, existingBuild, c.buildPackages())...)
	buildPkgs := c.buildPackages()

	// --- runtime stage ------------------------------------------------------------------
	rt := f.AddStage(runtimeImage, "", "--- runtime stage ---")
	rt.Add("WORKDIR", c.workdir)
	if len(runtimePkgs) > 0 {
		rt.Note("Runtime OS packages (build-only tools removed).", "RUN", osInstall(osFamily, runtimePkgs))
	}
	if len(c.env) > 0 {
		rt.Add("ENV", envLine(c.env))
	}
	rt.Note("", "COPY", "--from=build /out/"+name+" "+start.binary)

	assetDirs, assetNotes := goAssets(ctx, c, g)
	notes = append(notes, assetNotes...)
	for _, d := range assetDirs {
		rt.Add("COPY", d+" ./"+d)
	}
	for _, in := range c.passthrough {
		rt.Raw("Carried over from the existing Dockerfile.", in)
	}
	for _, cc := range c.custom {
		rt.Raw("Carried over unchanged: DeployIQ does not regenerate this step.", cc.Instruction)
	}
	if c.stopSignal != nil {
		rt.Raw("", *c.stopSignal)
	}
	if c.health != nil {
		rt.Raw("Health check from the existing Dockerfile.", *c.health)
	}
	switch {
	case nonRoot && distroless:
		rt.Note("distroless :nonroot images run as uid 65532.", "USER", "nonroot:nonroot")
	case nonRoot && c.user != "":
		rt.Add("USER", c.user)
	case nonRoot:
		if osFamily == "alpine" {
			rt.Note("Unprivileged user for the app.", "RUN", "adduser -D -H -u 10001 app")
		} else {
			rt.Note("Unprivileged user for the app.", "RUN", "useradd --system --no-create-home --uid 10001 app")
		}
		rt.Add("USER", "app")
	case c.user != "":
		rt.Add("USER", c.user)
	}
	rt.Add("EXPOSE", strings.Join(ports, " "))
	for _, in := range start.instructions {
		rt.Raw("", in)
	}

	// --- rules and estimate ----------------------------------------------------------------
	applied := []string{RuleMultiStage, RuleStaticBinary, RuleDropBuildTools}
	if !g.HasVendor {
		applied = append(applied, RuleLayerCaching)
	}
	if distroless {
		applied = append(applied, RuleDistrolessBase)
	} else if osFamily == "alpine" {
		applied = append(applied, RuleAlpineBase)
	} else {
		applied = append(applied, RuleSlimBase)
	}
	if nonRoot {
		applied = append(applied, RuleNonRootUser)
	}
	if len(runtimePkgs) > 0 || len(buildPkgs) > 0 {
		applied = append(applied, RuleNoPackageCache)
	}
	if g.UsesCgo {
		// A cgo binary is not static; do not claim the rule.
		applied = removeRule(applied, RuleStaticBinary)
	}
	if c.existing {
		had[RuleMultiStage] = ctx.Existing.MultiStage()
		rtFam := c.runtime.Family()
		had[RuleDistrolessBase] = rtFam == dockerfile.FamilyDistroless || rtFam == dockerfile.FamilyScratch
		had[RuleAlpineBase] = rtFam == dockerfile.FamilyAlpine
		had[RuleSlimBase] = rtFam == dockerfile.FamilyDebian && strings.Contains(c.runtime.Tag, "slim")
		had[RuleDropBuildTools] = rtFam != dockerfile.FamilyGolang
		had[RuleLayerCaching] = hadLayerCaching(ctx.Existing, "go.mod", "go.sum")
		had[RuleStaticBinary] = strings.Contains(existingBuild, "CGO_ENABLED=0") && strings.Contains(existingBuild, "-s") ||
			envSetsCGOOff(ctx.Existing) && strings.Contains(existingBuild, "-s")
		had[RuleNoPackageCache] = hadNoCache(c.analysis)
	}
	improved, present := split(applied, had)

	binMB := analyzer.GoBinaryMB(g)
	assetMB := 0.0
	if len(assetDirs) > 0 {
		assetMB = analyzer.SourceMB(ctx.Root, assetDirs...)
	}
	after := analyzer.Profile{BaseImage: runtimeImage, SystemPackages: runtimePkgs, ArtifactMB: binMB, SourceMB: assetMB}
	before, label := goBefore(ctx, c, g, goVersion, binMB, assetMB)

	return &Result{
		Dockerfile:     dockerfile.Generate(f),
		RulesApplied:   improved,
		AlreadyPresent: present,
		Notes:          notes,
		Before:         before,
		After:          after,
		BeforeLabel:    label,
	}, nil
}

// goStartPlan says where the binary lives in the runtime image and which
// instructions start it.
type goStartPlan struct {
	binary       string
	instructions []dockerfile.Instruction
	needsShell   bool
	notes        []string
}

var shellMeta = regexp.MustCompile("[$&|;<>`*(){}]")

// goStart works out how the existing image starts the binary, so the new image
// puts it at the same path and starts it the same way. With no existing
// Dockerfile the binary goes at <workdir>/<name>.
func goStart(c *carried, name string) (*goStartPlan, error) {
	plan := &goStartPlan{binary: path.Join(c.workdir, name)}
	if !c.hasStartCommand() {
		plan.instructions = []dockerfile.Instruction{{Cmd: "ENTRYPOINT", Args: dockerfile.ExecForm([]string{plan.binary}), Exec: []string{plan.binary}}}
		return plan, nil
	}

	lead := c.entrypoint
	if lead == nil {
		lead = c.cmd
	}
	argv := lead.Exec
	if argv == nil {
		if shellMeta.MatchString(lead.Args) {
			plan.needsShell = true
		}
		argv = strings.Fields(lead.Args)
	}
	if len(argv) == 0 {
		return nil, unsupported("the existing %s is empty", lead.Cmd)
	}

	// `go run` in the runtime image is the classic naive Dockerfile. The
	// toolchain will not exist in the new image, so the run becomes the
	// compiled binary with the same program arguments.
	if argv[0] == "go" && len(argv) > 1 && argv[1] == "run" {
		progArgs := goRunProgramArgs(argv[2:])
		newArgv := append([]string{plan.binary}, progArgs...)
		in := dockerfile.Instruction{Cmd: lead.Cmd, Args: dockerfile.ExecForm(newArgv), Exec: newArgv}
		plan.instructions = []dockerfile.Instruction{in}
		if lead == c.entrypoint && c.cmd != nil {
			plan.instructions = append(plan.instructions, *c.cmd)
		}
		plan.needsShell = false
		plan.notes = append(plan.notes, "`go run` replaced by the compiled binary at "+plan.binary+"; program arguments kept.")
		return plan, nil
	}

	first := argv[0]
	switch {
	case strings.HasSuffix(first, ".sh") || first == "sh" || first == "/bin/sh" || first == "bash" || first == "/bin/bash":
		// A wrapper script starts the binary; it must find it where the
		// existing build put it.
		plan.needsShell = true
		out := c.analysis.GoBuildOutput
		if out == "" {
			return nil, unsupported("the image starts through %s, and the binary's location cannot be determined from the existing `go build`", first)
		}
		plan.binary = resolveDir(c.workdir, out)
	case strings.Contains(first, "/"):
		plan.binary = resolveDir(c.workdir, first)
	default:
		// A bare name is resolved on PATH: put the binary somewhere on it.
		plan.binary = "/usr/local/bin/" + first
	}

	if lead.Exec == nil && !plan.needsShell {
		// Shell form needs /bin/sh, which distroless does not have. Simple
		// commands convert to exec form losslessly.
		for _, in := range []*dockerfile.Instruction{c.entrypoint, c.cmd} {
			if in == nil {
				continue
			}
			words := strings.Fields(in.Args)
			if in.Exec != nil {
				words = in.Exec
			}
			plan.instructions = append(plan.instructions, dockerfile.Instruction{Cmd: in.Cmd, Args: dockerfile.ExecForm(words), Exec: words})
		}
		plan.notes = append(plan.notes, "Shell-form start command converted to exec form (the runtime image has no shell).")
		return plan, nil
	}
	for _, in := range []*dockerfile.Instruction{c.entrypoint, c.cmd} {
		if in != nil {
			plan.instructions = append(plan.instructions, *in)
		}
	}
	return plan, nil
}

// goRunProgramArgs drops `go run`'s own flags and package arguments, keeping
// what follows for the program.
func goRunProgramArgs(args []string) []string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i++
	}
	for i < len(args) && (strings.HasSuffix(args[i], ".go") || args[i] == "." || strings.HasPrefix(args[i], "./")) {
		i++
	}
	return args[i:]
}

// existingGoBuild returns the existing `go build` command text, if any.
func existingGoBuild(c *carried) string {
	if c.analysis == nil {
		return ""
	}
	last := ""
	for _, r := range c.analysis.Runs {
		for _, cmd := range r.Commands {
			if cmd.Kind == dockerfile.CommandBuild && strings.Contains(cmd.Text, "go build") {
				last = cmd.Text
			}
		}
	}
	return last
}

// goBuildPackage extracts the package argument of a go build command.
func goBuildPackage(cmd string) string {
	fields := strings.Fields(cmd)
	// Walk from the end: the package is the last argument not consumed by
	// a flag.
	for i := len(fields) - 1; i > 0; i-- {
		f := fields[i]
		prev := fields[i-1]
		if strings.HasPrefix(f, "-") || strings.HasSuffix(prev, "-o") || prev == "-ldflags" || prev == "-tags" || prev == "-gcflags" {
			continue
		}
		if f == "build" {
			return ""
		}
		if strings.HasSuffix(f, ".go") {
			return "."
		}
		if strings.HasPrefix(f, "./") || f == "." {
			return strings.TrimSuffix(f, "/...")
		}
		return ""
	}
	return ""
}

var (
	ldflagsRe = regexp.MustCompile(`-ldflags[= ](?:"([^"]*)"|'([^']*)'|(\S+))`)
	tagsRe    = regexp.MustCompile(`-tags[= ](?:"([^"]*)"|'([^']*)'|(\S+))`)
)

// mergeLdflags combines -s -w with whatever the existing build passed, so a
// version stamp (-X main.version=...) survives.
func mergeLdflags(existing string) string {
	flags := []string{"-s", "-w"}
	if m := ldflagsRe.FindStringSubmatch(existing); m != nil && !strings.Contains(existing, "$") {
		for _, f := range strings.Fields(m[1] + m[2] + m[3]) {
			if f != "-s" && f != "-w" {
				flags = append(flags, f)
			}
		}
	}
	return strings.Join(flags, " ")
}

func goBuildTags(existing string) string {
	if m := tagsRe.FindStringSubmatch(existing); m != nil && !strings.Contains(existing, "$") {
		return "-tags=" + m[1] + m[2] + m[3] + " "
	}
	return ""
}

func envSetsCGOOff(df *dockerfile.Dockerfile) bool {
	if df == nil {
		return false
	}
	for _, st := range df.Stages {
		for _, kv := range st.Env() {
			if kv[0] == "CGO_ENABLED" && kv[1] == "0" {
				return true
			}
		}
	}
	return false
}

func binaryName(module, pkg string) string {
	name := strings.TrimPrefix(strings.TrimPrefix(pkg, "./"), ".")
	if name == "" || name == "/" {
		name = module
	}
	name = path.Base(name)
	clean := regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(name, "")
	if clean == "" || clean == "." {
		return "server"
	}
	return clean
}

// existingGoImageVersion returns the version of any golang image the existing
// Dockerfile builds with.
func existingGoImageVersion(df *dockerfile.Dockerfile) string {
	if df == nil {
		return ""
	}
	for _, st := range df.Stages {
		if st.Image.Family() == dockerfile.FamilyGolang {
			if maj, minor := st.Image.Version(); maj > 0 && minor >= 0 {
				return fmt.Sprintf("%d.%d", maj, minor)
			}
		}
	}
	return ""
}

// versionLess compares "1.22" < "1.24". An empty a is less than anything.
func versionLess(a, b string) bool {
	if a == "" {
		return true
	}
	var a1, a2, b1, b2 int
	_, _ = fmt.Sscanf(a, "%d.%d", &a1, &a2)
	_, _ = fmt.Sscanf(b, "%d.%d", &b1, &b2)
	return a1 < b1 || a1 == b1 && a2 < b2
}

// goAssets decides which conventional asset directories to copy into the
// runtime image.
func goAssets(ctx *Context, c *carried, g *detector.GoInfo) ([]string, []string) {
	// A multi-stage existing image already chose what to ship; it did not
	// ship the source tree, so it does not need asset directories.
	if c.multiStage {
		return nil, nil
	}
	var dirs []string
	for _, d := range goAssetDirs {
		if dirExists(ctx.Root, d) && hasNonGoFiles(filepath.Join(ctx.Root, d)) {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 {
		return nil, nil
	}
	note := "Copied " + strings.Join(dirs, ", ") + " into the runtime image: a binary-only image would not have files the app may read at runtime."
	if g.Embeds {
		note += " The code uses //go:embed; if these are embedded, the COPY lines can be removed."
	}
	return dirs, []string{note}
}

func hasNonGoFiles(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if !d.IsDir() && !strings.HasSuffix(d.Name(), ".go") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func removeRule(rs []string, id string) []string {
	out := rs[:0]
	for _, r := range rs {
		if r != id {
			out = append(out, r)
		}
	}
	return out
}

func goBefore(ctx *Context, c *carried, g *detector.GoInfo, goVersion string, binMB, assetMB float64) (analyzer.Profile, string) {
	srcMB := analyzer.SourceMB(ctx.Root)
	if !c.existing {
		return analyzer.Profile{
			BaseImage:   "golang:" + goVersion,
			SourceMB:    srcMB,
			ToolchainMB: analyzer.GoModCacheMB(g),
			ArtifactMB:  binMB,
		}, "conventional single-stage baseline (no Dockerfile found)"
	}
	if c.multiStage && c.runtime.Family() != dockerfile.FamilyGolang {
		return analyzer.Profile{
			BaseImage:      c.runtime.Ref,
			SystemPackages: c.runtimePackages(),
			ArtifactMB:     binMB,
			SourceMB:       assetMB,
		}, "current Dockerfile"
	}
	p := analyzer.Profile{
		BaseImage:      c.runtime.Ref,
		SystemPackages: c.packages,
		SourceMB:       srcMB,
		ToolchainMB:    analyzer.GoModCacheMB(g),
		ArtifactMB:     binMB,
	}
	if c.packageManager == "apt" && !c.analysis.AptListsCleaned {
		p.CacheMB = aptListsMB
	}
	return p, "current Dockerfile"
}
