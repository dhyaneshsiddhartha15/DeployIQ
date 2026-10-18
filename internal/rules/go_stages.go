package rules

import (
	"path"
	"regexp"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/analyzer"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// moduleAccessRe matches build steps that give the Go toolchain access to
// private modules. They must run in the build stage before `go mod download`,
// or every private dependency fails to fetch.
var moduleAccessRe = regexp.MustCompile(`git\s+config|go\s+env\s+-w|\.netrc|GOPRIVATE|GONOSUMDB|GONOSUMCHECK|GOPROXY|GOFLAGS|ssh-keyscan|known_hosts|GIT_TOKEN|GITHUB_TOKEN`)

// buildSetup is what the existing build stage did beyond downloading and
// compiling: build arguments, environment, private-module access and other
// steps. Dropping any of it can stop the build working at all.
type buildSetup struct {
	args  []dockerfile.Instruction
	env   [][2]string
	early []dockerfile.Instruction // before `go mod download`
	late  []dockerfile.Instruction // after the source copy, before `go build`
	git   bool                     // the setup needs git in the builder

	// goproxy is an internal module proxy from .doiq.yml.
	goproxy string
}

// collectBuildSetup gathers build-stage setup from the existing Dockerfile and
// removes module-access steps from the runtime carry-over: configuring git
// credentials in the runtime image is both useless and a secret leak.
func collectBuildSetup(c *carried) buildSetup {
	var s buildSetup
	if c.df == nil {
		return s
	}
	inRuntime := map[int]bool{}
	for _, si := range lineage(c.df) {
		inRuntime[si] = true
	}

	var keepRuntime []dockerfile.CustomCommand
	for _, cc := range c.analysis.Custom {
		text := instructionText(cc.Instruction)
		switch {
		case moduleAccessRe.MatchString(text):
			s.early = append(s.early, cc.Instruction)
		case !inRuntime[cc.Stage]:
			s.late = append(s.late, cc.Instruction)
		}
	}
	for _, cc := range c.custom {
		if !moduleAccessRe.MatchString(instructionText(cc.Instruction)) {
			keepRuntime = append(keepRuntime, cc)
		}
	}
	c.custom = keepRuntime

	setupText := ""
	for _, in := range s.early {
		setupText += " " + instructionText(in)
	}
	s.git = strings.Contains(setupText, "git ") || strings.Contains(setupText, "GOPRIVATE") || strings.Contains(setupText, ".netrc")

	for si, st := range c.df.Stages {
		for _, in := range st.Instructions {
			switch {
			case in.Cmd == "ARG" && (!inRuntime[si] || argUsed(in, setupText)):
				s.args = append(s.args, in)
			case in.Cmd == "ENV" && !inRuntime[si]:
				s.env = append(s.env, dockerfile.ParseEnv(in.Args)...)
			}
		}
	}
	return s
}

func argUsed(in dockerfile.Instruction, text string) bool {
	for _, f := range strings.Fields(in.Args) {
		name, _, _ := strings.Cut(f, "=")
		if strings.Contains(text, "$"+name) || strings.Contains(text, "${"+name) {
			return true
		}
	}
	return false
}

// goBuildStage writes the build stage. It reuses the existing golang builder
// image when there is one (a pinned toolchain is a decision, not an accident),
// and keeps the existing build setup.
func goBuildStage(f *dockerfile.File, c *carried, g *detector.GoInfo, setup buildSetup, name, pkg, goVersion, existingBuild string, buildPkgs []string) []string {
	var notes []string

	builderOS := "alpine"
	builder := "golang:" + goVersion + "-alpine"
	if g.UsesCgo || c.packageManager == "apt" || setup.git {
		builderOS, builder = "debian", "golang:"+goVersion+"-bookworm"
	}
	if img, ok := existingGoBuilder(c.df); ok {
		maj, minor := img.Version()
		if v := itoa(maj) + "." + itoa(minor); maj > 0 && minor >= 0 && !versionLess(v, goVersion) {
			builder, builderOS = img.Ref, img.OSFamily()
		}
	}

	build := f.AddStage(builder, "build", "--- build stage: toolchain, module cache and sources never reach the runtime ---")
	for _, in := range setup.args {
		build.Keep(in)
	}
	build.Add("WORKDIR", "/src")
	if setup.goproxy != "" {
		build.Note("Internal Go module proxy, from .doiq.yml.", "ENV", "GOPROXY="+setup.goproxy)
	}
	if len(setup.env) > 0 {
		build.Add("ENV", envLine(setup.env))
	}
	if len(buildPkgs) > 0 {
		build.Note("Build-time OS packages from the existing Dockerfile.", "RUN", osInstall(builderOS, buildPkgs))
	}
	if len(setup.early) > 0 {
		build.Raw("Private module access, kept from the existing build stage. Build with the same --build-arg values as before.", setup.early[0])
		for _, in := range setup.early[1:] {
			build.Keep(in)
		}
		notes = append(notes, "Kept the existing private-module setup ("+strings.Join(argNames(setup.args), ", ")+") in the build stage; pass the same --build-arg values when building.")
	}
	if !g.HasVendor {
		mods := "go.mod"
		if g.HasGoSum {
			mods = "go.mod go.sum"
		}
		build.Note("Module files first: the download layer is reused until dependencies change.", "COPY", mods+" ./")
		if g.Requires > 0 {
			build.Add("RUN", "go mod download")
		}
	}
	build.Note("", "COPY", ". .")
	for _, in := range setup.late {
		build.Keep(in)
	}

	cgo := "0"
	if g.UsesCgo {
		cgo = "1"
	}
	ldflags := mergeLdflags(existingBuild)
	tags := goBuildTags(existingBuild)
	if strings.Contains(existingBuild, "$") {
		notes = append(notes, "The existing `go build` uses build arguments; its -ldflags/-tags were not carried. Compare with: "+existingBuild)
	}
	build.Note("", "RUN", "CGO_ENABLED="+cgo+" go build -trimpath "+tags+"-ldflags=\""+ldflags+"\" -o /out/"+name+" "+pkg)
	return notes
}

func argNames(args []dockerfile.Instruction) []string {
	var out []string
	for _, in := range args {
		for _, f := range strings.Fields(in.Args) {
			n, _, _ := strings.Cut(f, "=")
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return []string{"git and GOPRIVATE configuration"}
	}
	return out
}

func existingGoBuilder(df *dockerfile.Dockerfile) (dockerfile.Image, bool) {
	if df == nil {
		return dockerfile.Image{}, false
	}
	for _, st := range df.Stages {
		if st.Image.Family() == dockerfile.FamilyGolang {
			return st.Image, true
		}
	}
	return dockerfile.Image{}, false
}

// keepsRuntime reports whether the existing runtime stage is already lean and
// should be kept as written.
func keepsRuntime(c *carried) bool {
	if !c.existing || !c.multiStage || c.df.Final().FromStage != "" {
		return false
	}
	switch c.runtime.Family() {
	case dockerfile.FamilyDebian, dockerfile.FamilyUbuntu, dockerfile.FamilyAlpine,
		dockerfile.FamilyRHEL, dockerfile.FamilyDistroless, dockerfile.FamilyScratch:
		return true
	}
	return false
}

// goKeepRuntime regenerates only the build stage and replays the existing
// runtime stage instruction for instruction, pointing its binary copy at the
// new build.
func goKeepRuntime(c *carried, g *detector.GoInfo, setup buildSetup, name, pkg, goVersion, existingBuild string, notes []string) (*Result, error) {
	df := c.df
	final := df.Final()
	finalIdx := len(df.Stages) - 1

	builderNames := map[string]int{}
	for i, st := range df.Stages[:finalIdx] {
		builderNames[strings.ToLower(st.Name)] = i
		builderNames[itoa(i)] = i
	}
	outAbs := ""
	if c.analysis.GoBuildOutput != "" && c.analysis.GoBuildStage >= 0 {
		outAbs = resolveDir(stageWorkdir(df.Stages[c.analysis.GoBuildStage]), c.analysis.GoBuildOutput)
		// Keep the existing file name: a `COPY --from=builder <bin> .` into a
		// directory names the file after its source, and the ENTRYPOINT
		// expects that name.
		name = path.Base(outAbs)
	}

	f := &dockerfile.File{Header: header(constants.StackGo, true)}
	notes = append(notes, goBuildStage(f, c, g, setup, name, pkg, goVersion, existingBuild, packagesIn(c, false))...)

	src := c.df.RelPath
	if src == "" {
		src = constants.DockerfileName
	}
	rt := f.AddStage(c.runtime.Ref, "", "--- runtime stage: kept exactly as in "+src+"; only the binary it copies is rebuilt ---")
	copiedBinary := false
	for _, in := range final.Instructions {
		from, isStageCopy := in.Flags["from"]
		if in.Cmd != "COPY" || !isStageCopy {
			rt.Keep(in)
			continue
		}
		si, ok := builderNames[strings.ToLower(from)]
		if !ok {
			rt.Keep(in) // --from an external image: unchanged
			continue
		}
		parts := in.Exec
		if parts == nil {
			parts = strings.Fields(in.Args)
		}
		if len(parts) < 2 {
			rt.Keep(in)
			continue
		}
		dest := parts[len(parts)-1]
		wd := stageWorkdir(df.Stages[si])
		var srcs []string
		for _, s := range parts[:len(parts)-1] {
			abs := resolveDir(wd, s)
			switch {
			case outAbs != "" && abs == outAbs, outAbs == "" && len(parts) == 2 && !copiedBinary && looksLikeBinary(abs, name):
				srcs = append(srcs, "/out/"+name)
				copiedBinary = true
			case strings.HasPrefix(abs, wd+"/") || abs == wd:
				srcs = append(srcs, path.Join("/src", strings.TrimPrefix(abs, wd)))
			default:
				srcs = append(srcs, abs) // a system path in the builder, e.g. /etc/ssl/certs
			}
		}
		flags := map[string]string{}
		for k, v := range in.Flags {
			flags[k] = v
		}
		flags["from"] = "build"
		rt.Keep(dockerfile.Instruction{Cmd: "COPY", Flags: flags, Args: strings.Join(srcs, " ") + " " + dest})
	}
	if !copiedBinary {
		return nil, unsupported("the runtime stage of %s copies nothing recognisable as the built binary from the build stage", src)
	}
	notes = append(notes, "The runtime stage ("+c.runtime.Ref+") is already lean, so it is kept exactly as written: its pinned packages, users and files are what the app runs with today. Only the build stage changed.")

	applied := []string{RuleMultiStage, RuleDropBuildTools}
	if !g.HasVendor {
		applied = append(applied, RuleLayerCaching)
	}
	if !g.UsesCgo {
		applied = append(applied, RuleStaticBinary)
	}
	switch c.runtime.Family() {
	case dockerfile.FamilyDistroless, dockerfile.FamilyScratch:
		applied = append(applied, RuleDistrolessBase)
	case dockerfile.FamilyAlpine:
		applied = append(applied, RuleAlpineBase)
	default:
		if strings.Contains(c.runtime.Tag, "slim") || strings.Contains(c.runtime.Repository, "minimal") {
			applied = append(applied, RuleSlimBase)
		}
	}
	had := map[string]bool{
		RuleMultiStage: true, RuleDropBuildTools: true,
		RuleDistrolessBase: true, RuleAlpineBase: true, RuleSlimBase: true,
		RuleLayerCaching: hadLayerCaching(df, "go.mod", "go.sum"),
		RuleStaticBinary: strings.Contains(existingBuild, "CGO_ENABLED=0") && strings.Contains(existingBuild, "-s") ||
			envSetsCGOOff(df) && strings.Contains(existingBuild, "-s"),
	}
	if c.user != "" && !userIsRoot(c.user) {
		applied = append(applied, RuleNonRootUser)
		had[RuleNonRootUser] = true
	}
	improved, present := split(applied, had)

	// The runtime is identical on both sides; the difference is the binary.
	// An unstripped binary carries its symbol table and DWARF data, roughly a
	// quarter of its size — the low end, so the saving is not overstated.
	binMB := analyzer.GoBinaryMB(g)
	beforeBin := binMB
	if !strings.Contains(existingBuild, "-s") {
		beforeBin = binMB * 1.25
	}
	pkgs := packagesIn(c, true)
	before := analyzer.Profile{BaseImage: c.runtime.Ref, SystemPackages: pkgs, ArtifactMB: beforeBin}
	after := analyzer.Profile{BaseImage: c.runtime.Ref, SystemPackages: pkgs, ArtifactMB: binMB}

	return &Result{
		Dockerfile:     dockerfile.Generate(f),
		RulesApplied:   improved,
		AlreadyPresent: present,
		Notes:          notes,
		Before:         before,
		After:          after,
		BeforeLabel:    "current Dockerfile",
	}, nil
}

// packagesIn returns OS packages installed in the runtime lineage (runtime
// true) or in the build stages (false), without hand-installed language
// runtimes.
func packagesIn(c *carried, runtime bool) []string {
	inRuntime := map[int]bool{}
	for _, si := range lineage(c.df) {
		inRuntime[si] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range c.analysis.Runs {
		if inRuntime[r.Stage] != runtime {
			continue
		}
		for _, cmd := range r.Commands {
			for _, p := range cmd.Packages {
				if !seen[p] && !languagePackages[pkgName(p)] {
					seen[p] = true
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// stageWorkdir is the stage's final working directory ("/" when unset).
func stageWorkdir(st dockerfile.Stage) string {
	wd := "/"
	for _, in := range st.Instructions {
		if in.Cmd == "WORKDIR" {
			wd = resolveDir(wd, in.Args)
		}
	}
	return wd
}

// looksLikeBinary is the fallback when the existing build's -o is unknown: a
// single-file copy whose name matches the module or has no extension.
func looksLikeBinary(p, name string) bool {
	base := path.Base(p)
	return base == name || !strings.Contains(base, ".")
}

func itoa(n int) string {
	if n < 0 {
		return "-1"
	}
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
