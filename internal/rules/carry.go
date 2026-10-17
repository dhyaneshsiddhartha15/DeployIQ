package rules

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
)

// carried is what the generated Dockerfile preserves from the existing one.
//
// The principle: the engine changes how the image is built, never how the app
// runs. Ports, environment, working directory, start command, health check and
// any build step it does not understand all come across unchanged. A smaller
// image that starts differently is a broken build by another name.
type carried struct {
	existing   bool
	multiStage bool

	// runtime is the existing final image; zero when there is none.
	runtime dockerfile.Image

	ports   []string
	env     [][2]string
	workdir string

	cmd        *dockerfile.Instruction
	entrypoint *dockerfile.Instruction
	user       string
	health     *dockerfile.Instruction
	stopSignal *dockerfile.Instruction

	// passthrough are LABEL, VOLUME and COPY/ADD instructions that place
	// files the generic `COPY . .` would not (URLs, absolute destinations
	// outside the workdir).
	passthrough []dockerfile.Instruction

	// custom are RUN instructions the engine does not regenerate.
	custom []dockerfile.CustomCommand

	// packages are OS packages the existing build installed, and the
	// manager it used.
	packages       []string
	packageManager string

	analysis *dockerfile.Analysis
}

// buildOnlyPackages never belong in a runtime image: compilers, headers-only
// toolchains and VCS clients. Anything else the existing Dockerfile installed is
// assumed to be needed at runtime and is kept — guessing that a library is
// unused is the dependency-pruning risk Phase 0.8 defers.
var buildOnlyPackages = map[string]bool{
	"build-essential": true, "gcc": true, "g++": true, "make": true, "cmake": true,
	"git": true, "pkg-config": true, "pkgconf": true, "autoconf": true, "automake": true,
	"libtool": true, "musl-dev": true, "linux-headers": true, "build-base": true,
	"python3-dev": true, "gfortran": true, "clang": true, "golang": true, "golang-go": true,
}

// languagePackages are runtimes the existing image installed by hand (node on
// ubuntu, for instance). The generated image starts from the official language
// image instead, so these are dropped rather than carried.
var languagePackages = map[string]bool{
	"nodejs": true, "npm": true, "yarn": true, "python3": true, "python3-pip": true,
	"python3-venv": true, "python": true, "python-pip": true, "py3-pip": true, "go": true,
	"python3-setuptools": true, "python3-wheel": true,
}

func isBuildOnly(pkg string) bool { return buildOnlyPackages[pkgName(pkg)] }

func pkgName(p string) string {
	if i := strings.IndexAny(p, "=<>@:"); i > 0 {
		p = p[:i]
	}
	return strings.ToLower(p)
}

// runtimePackages filters the existing packages to those worth carrying into
// the runtime image.
func (c *carried) runtimePackages() []string {
	var out []string
	for _, p := range c.packages {
		if !isBuildOnly(p) && !languagePackages[pkgName(p)] {
			out = append(out, p)
		}
	}
	return out
}

// buildPackages filters the existing packages to those the build stage needs:
// everything except hand-installed language runtimes.
func (c *carried) buildPackages() []string {
	var out []string
	for _, p := range c.packages {
		if !languagePackages[pkgName(p)] {
			out = append(out, p)
		}
	}
	return out
}

// droppedBuildTools lists existing packages that will not reach the runtime.
func (c *carried) droppedBuildTools() []string {
	var out []string
	for _, p := range c.packages {
		if isBuildOnly(p) {
			out = append(out, p)
		}
	}
	return out
}

// carry extracts what must survive from the existing Dockerfile. With none, it
// returns an empty carried and the generator infers everything.
func carry(ctx *Context, defaultWorkdir string) (*carried, error) {
	c := &carried{workdir: defaultWorkdir}
	df := ctx.Existing
	if df == nil {
		return c, nil
	}
	c.existing = true
	c.multiStage = df.MultiStage()
	c.runtime = df.RuntimeImage()
	if c.runtime.Family() == dockerfile.FamilyUnknown {
		return nil, unsupported("base image %q is not one this version recognises; v1 will not guess what a custom base image contains", c.runtime.Ref)
	}
	c.analysis = dockerfile.Analyze(df)

	final := df.Final()
	finalIdx := len(df.Stages) - 1
	if err := checkStages(df); err != nil {
		return nil, err
	}

	// Only the stages the runtime is built from describe the runtime.
	runtimeStages := lineage(df)
	for _, si := range runtimeStages {
		st := &df.Stages[si]
		c.env = append(c.env, st.Env()...)
		c.ports = append(c.ports, st.Ports()...)
		for _, in := range st.Instructions {
			switch in.Cmd {
			case "WORKDIR":
				c.workdir = resolveDir(c.workdir, in.Args)
			case "LABEL", "VOLUME":
				c.passthrough = append(c.passthrough, in)
			case "STOPSIGNAL":
				in := in
				c.stopSignal = &in
			case "COPY", "ADD":
				if keep, _ := keepCopy(in, c.workdir); keep {
					c.passthrough = append(c.passthrough, in)
				}
			}
		}
		if u := st.Last("USER"); u != nil {
			c.user = strings.TrimSpace(u.Args)
		}
		if h := st.Last("HEALTHCHECK"); h != nil {
			c.health = h
		}
	}
	c.cmd = final.Last("CMD")
	c.entrypoint = final.Last("ENTRYPOINT")
	if c.cmd == nil && c.entrypoint == nil && final.FromStage != "" {
		for _, si := range runtimeStages {
			if cmd := df.Stages[si].Last("CMD"); cmd != nil {
				c.cmd = cmd
			}
			if ep := df.Stages[si].Last("ENTRYPOINT"); ep != nil {
				c.entrypoint = ep
			}
		}
	}

	runtimeSet := map[int]bool{}
	for _, si := range runtimeStages {
		runtimeSet[si] = true
	}
	for _, cc := range c.analysis.Custom {
		if runtimeSet[cc.Stage] || cc.Stage == finalIdx {
			c.custom = append(c.custom, cc)
		}
	}
	c.packages = c.analysis.SystemPackages
	c.packageManager = c.analysis.PackageManager
	return c, nil
}

// lineage returns the indexes of the final stage and every stage it is FROM,
// final first.
func lineage(df *dockerfile.Dockerfile) []int {
	idx := len(df.Stages) - 1
	var out []int
	for guard := 0; guard < len(df.Stages); guard++ {
		out = append(out, idx)
		from := df.Stages[idx].FromStage
		if from == "" {
			break
		}
		next := -1
		for i := range df.Stages {
			if strings.EqualFold(df.Stages[i].Name, from) {
				next = i
			}
		}
		if next < 0 {
			break
		}
		idx = next
	}
	// Reverse so instructions apply in the order Docker applies them.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// checkStages refuses multi-stage builds that mix stacks, such as a Node
// frontend stage feeding a Python backend. That is the multi-service case
// Phase 0.8 defers; regenerating only one half would silently drop the other.
func checkStages(df *dockerfile.Dockerfile) error {
	if !df.MultiStage() {
		return nil
	}
	runtimeFamily := df.RuntimeImage().Family()
	for i, st := range df.Stages[:len(df.Stages)-1] {
		fam := st.Image.Family()
		if st.FromStage != "" || fam == runtimeFamily {
			continue
		}
		langs := map[string]bool{dockerfile.FamilyNode: true, dockerfile.FamilyPython: true, dockerfile.FamilyGolang: true}
		if langs[fam] && langs[runtimeFamily] && fam != runtimeFamily {
			return unsupported("stage %d builds with %s but the runtime is %s; multi-language builds are not supported yet", i+1, fam, runtimeFamily)
		}
	}
	return nil
}

// keepCopy decides whether a COPY/ADD in the runtime lineage must be carried.
// Copies of build-context paths into the workdir are covered by the
// generator's own `COPY . .`; copies that place files elsewhere, fetch URLs or
// pull from external images are not, and dropping them would change what the
// app finds at runtime.
func keepCopy(in dockerfile.Instruction, workdir string) (bool, string) {
	if from, ok := in.Flags["from"]; ok {
		// --from=<image> with a registry path or tag is external; a bare
		// name is an earlier stage, which the generator replaces.
		if strings.ContainsAny(from, "/:") {
			return true, "copies from an external image"
		}
		return false, ""
	}
	parts := in.Exec
	if parts == nil {
		parts = strings.Fields(in.Args)
	}
	if len(parts) < 2 {
		return false, ""
	}
	dest := parts[len(parts)-1]
	for _, src := range parts[:len(parts)-1] {
		if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
			return true, "fetches a URL"
		}
	}
	if path.IsAbs(dest) {
		clean := path.Clean(dest)
		wd := path.Clean(workdir)
		if clean != wd && !strings.HasPrefix(clean, wd+"/") {
			return true, "places files outside the working directory"
		}
	}
	return false, ""
}

func resolveDir(cur, next string) string {
	next = strings.Trim(strings.TrimSpace(next), `"`)
	if path.IsAbs(next) {
		return path.Clean(next)
	}
	return path.Clean(path.Join(cur, next))
}

// startCommand returns the instructions that start the app, carried as-is.
func (c *carried) hasStartCommand() bool { return c.cmd != nil || c.entrypoint != nil }

// lowPort reports whether any exposed port is privileged (<1024). A non-root
// user cannot bind one, so the non-root rule is skipped rather than producing an
// image that fails at startup.
func lowPort(ports []string) bool {
	for _, p := range ports {
		n, err := strconv.Atoi(p)
		if err == nil && n > 0 && n < 1024 {
			return true
		}
	}
	return false
}

// userIsRoot reports whether an existing USER value means root.
func userIsRoot(u string) bool {
	u = strings.TrimSpace(u)
	return u == "" || u == "root" || u == "0" || strings.HasPrefix(u, "0:") || strings.HasPrefix(u, "root:")
}

var toolRe = regexp.MustCompile(`\b(curl|wget|bash|git|nc|jq)\b`)

// requiredTools finds command-line tools the runtime must provide: anything
// the start command, health check or carried steps invoke, plus the
// interpreter of any script they run. The official full images ship curl,
// bash and git; slim and Alpine images do not, so moving to a smaller base
// without this check breaks health checks and entrypoint scripts.
func (c *carried) requiredTools(root string) []string {
	var texts []string
	for _, in := range []*dockerfile.Instruction{c.cmd, c.entrypoint, c.health} {
		if in != nil {
			texts = append(texts, instructionText(*in))
		}
	}
	for _, cc := range c.custom {
		texts = append(texts, instructionText(cc.Instruction))
	}

	found := map[string]bool{}
	for _, t := range texts {
		for _, m := range toolRe.FindAllString(t, -1) {
			found[m] = true
		}
		for _, f := range strings.Fields(t) {
			f = strings.Trim(f, `"',[]`)
			if strings.HasSuffix(f, ".sh") {
				if sh := shebang(root, c.workdir, f); sh != "" {
					found[sh] = true
				}
			}
		}
	}
	var out []string
	for _, t := range []string{"bash", "curl", "wget", "git", "nc", "jq"} {
		if found[t] {
			out = append(out, t)
		}
	}
	return out
}

func instructionText(in dockerfile.Instruction) string {
	if in.Exec != nil {
		return strings.Join(in.Exec, " ")
	}
	return in.Args
}

// shebang reads the interpreter of a script in the repository, mapping the
// in-image path back to the build context by stripping the workdir.
func shebang(root, workdir, script string) string {
	rel := script
	if path.IsAbs(rel) {
		if !strings.HasPrefix(rel, workdir+"/") {
			return ""
		}
		rel = strings.TrimPrefix(rel, workdir+"/")
	}
	rel = strings.TrimPrefix(rel, "./")
	if strings.Contains(rel, "..") {
		return ""
	}
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	if strings.HasPrefix(line, "#!") && strings.Contains(line, "bash") {
		return "bash"
	}
	return ""
}

// addMissing appends tools not already present in pkgs.
func addMissing(pkgs []string, tools ...string) []string {
	have := map[string]bool{}
	for _, p := range pkgs {
		have[pkgName(p)] = true
	}
	for _, t := range tools {
		pkg := t
		if t == "nc" {
			pkg = "netcat-openbsd"
		}
		if !have[pkg] {
			pkgs = append(pkgs, pkg)
			have[pkg] = true
		}
	}
	return pkgs
}

// envHas reports whether the carried env sets key.
func (c *carried) envHas(key string) bool {
	for _, kv := range c.env {
		if kv[0] == key {
			return true
		}
	}
	return false
}

// envLine renders env pairs as one ENV instruction's arguments.
func envLine(pairs [][2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, kv := range pairs {
		parts = append(parts, kv[0]+"="+quoteEnv(kv[1]))
	}
	return strings.Join(parts, " ")
}

func quoteEnv(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\"'$\\") && !strings.HasPrefix(v, `"`) {
		return strconv.Quote(v)
	}
	return v
}

// aptInstall renders a Debian package install that leaves no apt lists behind
// (no-package-cache).
func aptInstall(pkgs []string) string {
	return "apt-get update \\\n    && apt-get install -y --no-install-recommends " + strings.Join(pkgs, " ") +
		" \\\n    && rm -rf /var/lib/apt/lists/*"
}

// apkInstall renders an Alpine install without a package cache.
func apkInstall(pkgs []string) string {
	return "apk add --no-cache " + strings.Join(pkgs, " ")
}

// osInstall picks the installer for the target OS family.
func osInstall(osFamily string, pkgs []string) string {
	if osFamily == "alpine" {
		return apkInstall(pkgs)
	}
	return aptInstall(pkgs)
}

// hadLayerCaching reports whether the existing Dockerfile copied any of the
// given manifests before a whole-context copy.
func hadLayerCaching(df *dockerfile.Dockerfile, manifests ...string) bool {
	if df == nil {
		return false
	}
	for _, st := range df.Stages {
		for _, in := range st.All("COPY") {
			if copiesManifest(in, manifests) {
				return true
			}
		}
	}
	return false
}

// copiesManifest reports whether a COPY names one of the manifests explicitly
// (not as part of a whole-context copy).
func copiesManifest(in dockerfile.Instruction, manifests []string) bool {
	parts := in.Exec
	if parts == nil {
		parts = strings.Fields(in.Args)
	}
	if len(parts) < 2 {
		return false
	}
	for _, src := range parts[:len(parts)-1] {
		if src == "." || src == "./" {
			return false
		}
		base := path.Base(src)
		for _, m := range manifests {
			if base == m || strings.HasPrefix(base, strings.TrimSuffix(m, path.Ext(m))) {
				return true
			}
		}
	}
	return false
}

// hadNoCache reports whether the existing build avoided leaving package caches.
func hadNoCache(a *dockerfile.Analysis) bool {
	if a == nil {
		return false
	}
	if a.PackageManager == "apt" && !a.AptListsCleaned {
		return false
	}
	return true
}
