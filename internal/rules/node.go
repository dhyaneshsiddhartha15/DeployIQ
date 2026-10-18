package rules

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/analyzer"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// DefaultNodeMajor is used when neither the existing Dockerfile nor
// engines.node names a version: the oldest Node release line still in active
// LTS, so the output works for the widest range of code. Revisited whenever
// the LTS schedule moves (Phase 12.3).
const DefaultNodeMajor = 22

const defaultNodePort = "3000"

type nodeGenerator struct{}

func (nodeGenerator) Stack() string { return constants.StackNode }

func (nodeGenerator) Generate(ctx *Context) (*Result, error) {
	n := ctx.Detection.Node
	if n.Workspaces {
		return nil, unsupported("package.json declares workspaces; monorepos are not supported yet")
	}
	c, err := carry(ctx, "/app")
	if err != nil {
		return nil, err
	}

	var notes []string
	had := map[string]bool{}

	// --- version and variant ------------------------------------------------
	// The version the app runs on today is kept: changing the runtime
	// version is a behaviour change, not a size optimization.
	major := 0
	if c.existing && c.runtime.Family() == dockerfile.FamilyNode {
		major, _ = c.runtime.Version()
	}
	if major <= 0 {
		major = n.EngineMajor
	}
	if major <= 0 {
		major = DefaultNodeMajor
		notes = append(notes, fmt.Sprintf("No Node version found in the Dockerfile or engines.node; using Node %d (LTS). Pin engines.node to choose.", major))
	}

	runtimePkgs := c.runtimePackages()
	tools := c.requiredTools(ctx.Root)

	// Alpine unless something says the code needs glibc: an existing
	// Debian-family image that compiled native modules (their prebuilt
	// binaries may target glibc), or OS packages named for apt.
	variant := dockerfile.VariantAlpine
	existingOS := ""
	if c.existing {
		existingOS = c.runtime.OSFamily()
	}
	switch {
	case existingOS == "alpine":
	case existingOS != "" && len(n.NativeModules) > 0:
		variant = dockerfile.VariantSlim
		notes = append(notes, "Native modules ("+strings.Join(n.NativeModules, ", ")+") were built against glibc; using the slim (Debian) variant rather than Alpine.")
	case c.packageManager == "apt" && len(runtimePkgs) > 0:
		variant = dockerfile.VariantSlim
	case c.packageManager == "dnf" && len(runtimePkgs) > 0:
		return nil, unsupported("the existing image installs RHEL-family packages (%s); translating them to another distribution is not supported", strings.Join(runtimePkgs, " "))
	}
	if variant == dockerfile.VariantAlpine && c.existing && existingOS == "alpine" {
		had[RuleAlpineBase] = c.runtime.Family() == dockerfile.FamilyNode
	}
	if variant == dockerfile.VariantSlim && c.existing && c.runtime.Family() == dockerfile.FamilyNode && c.runtime.Variant() == dockerfile.VariantSlim {
		had[RuleSlimBase] = true
	}
	osFamily := "debian"
	if variant == dockerfile.VariantAlpine {
		osFamily = "alpine"
	}
	image := fmt.Sprintf("node:%d-%s", major, variant)
	if variant == dockerfile.VariantSlim {
		image = fmt.Sprintf("node:%d-slim", major)
	}
	// A declared internal Node image stays: companies mandate their base
	// images for patching and compliance, and the saving comes from the
	// multi-stage build and pruning, not from swapping the base.
	if image, variant, osFamily = keepCustomBase(c, dockerfile.FamilyNode, image, variant, osFamily, &notes); c.runtime.Custom() != nil {
		had[RuleAlpineBase], had[RuleSlimBase] = true, true
	}

	// Tools the start command or health check relies on. Alpine has
	// busybox wget but no curl or bash; slim has bash but no curl or wget.
	var needed []string
	for _, t := range tools {
		switch {
		case t == "wget" && osFamily == "alpine", t == "bash" && osFamily == "debian":
		default:
			needed = append(needed, t)
		}
	}
	if len(needed) > 0 {
		runtimePkgs = addMissing(runtimePkgs, needed...)
		notes = append(notes, "Added "+strings.Join(needed, ", ")+" to the runtime image: the start command, health check or a carried step uses it.")
	}

	// --- install commands -----------------------------------------------------
	install, prune := nodeInstall(n, major)

	// --- start command --------------------------------------------------------
	cmd := c.cmd
	entry := c.entrypoint
	var inferred []string
	if !c.hasStartCommand() {
		inferred, err = inferNodeStart(ctx.Root, n)
		if err != nil {
			return nil, err
		}
		notes = append(notes, "Start command inferred: "+strings.Join(inferred, " ")+".")
	}

	ports := c.ports
	if len(ports) == 0 {
		ports = []string{defaultNodePort}
		notes = append(notes, "No EXPOSE found; documenting port "+defaultNodePort+". EXPOSE does not publish anything, so a wrong guess is harmless.")
	}

	// --- non-root ---------------------------------------------------------------
	runAsNode := false
	switch {
	case c.user != "" && !userIsRoot(c.user):
		had[RuleNonRootUser] = true
	case lowPort(ports):
		notes = append(notes, "The app exposes a port below 1024, which an unprivileged user cannot bind; the container keeps running as root.")
	case c.user != "":
		notes = append(notes, "The existing Dockerfile explicitly runs as root; that is kept.")
	default:
		runAsNode = true
	}

	// --- build stage --------------------------------------------------------------
	f := &dockerfile.File{Header: header(constants.StackNode, c.existing)}
	build := f.AddStage(image, "build", "--- build stage: full dependencies, compile, then prune to production ---")
	if n.PackageManager != detector.PackageManagerNPM {
		build.Add("ENV", "COREPACK_ENABLE_DOWNLOAD_PROMPT=0")
	}
	build.Add("WORKDIR", c.workdir)
	if reg := ctx.Packages.NPM; reg != "" {
		build.Note("Internal npm registry, from .doiq.yml.", "ENV", "NPM_CONFIG_REGISTRY="+reg+" YARN_NPM_REGISTRY_SERVER="+reg)
	}

	buildPkgs := c.buildPackages()
	if len(n.NativeModules) > 0 {
		// node-gyp's requirements; the package names match on apt and apk.
		buildPkgs = addMissing(buildPkgs, "python3", "make", "g++")
	}
	if len(buildPkgs) > 0 {
		build.Note("Toolchain and libraries for native modules. Build stage only.", "RUN", osInstall(osFamily, buildPkgs))
	}

	manifests := []string{constants.ManifestPackageJSON}
	if n.Lockfile != "" {
		manifests = append(manifests, n.Lockfile)
	}
	for _, extra := range []string{".npmrc", ".yarnrc", ".yarnrc.yml", "pnpm-workspace.yaml"} {
		if fileExists(ctx.Root, extra) {
			manifests = append(manifests, extra)
		}
	}
	copyArgs := strings.Join(manifests, " ") + " ./"
	build.Note("Manifests first: this layer and the install below are reused until dependencies change.", "COPY", copyArgs)
	if n.YarnBerry && dirExists(ctx.Root, ".yarn") {
		build.Add("COPY", ".yarn ./.yarn")
	}
	build.Add("RUN", install)
	build.Note("", "COPY", ". .")
	if n.HasScript("build") {
		build.Add("RUN", runScript(n, "build"))
	}
	if prune != "" {
		build.Note("Drop dev dependencies now that the build is done.", "RUN", prune)
	} else {
		notes = append(notes, "Yarn Berry has no built-in production prune; dev dependencies are kept. The image is still multi-stage.")
	}

	// --- runtime stage --------------------------------------------------------------
	rt := f.AddStage(image, "", "--- runtime stage ---")
	env := [][2]string{}
	if !c.envHas("NODE_ENV") {
		env = append(env, [2]string{"NODE_ENV", "production"})
	}
	env = append(env, c.env...)
	rt.Add("ENV", envLine(env))
	rt.Add("WORKDIR", c.workdir)
	if len(runtimePkgs) > 0 {
		rt.Note("Runtime OS packages from the existing Dockerfile (build-only tools removed).", "RUN", osInstall(osFamily, runtimePkgs))
	}
	for _, in := range c.passthrough {
		rt.Raw("Carried over from the existing Dockerfile.", in)
	}
	chown := ""
	if runAsNode {
		chown = "--chown=node:node "
	}
	rt.Note("App, built output and production node_modules from the build stage.", "COPY", "--from=build "+chown+c.workdir+" "+c.workdir)
	for _, cc := range c.custom {
		rt.Raw("Carried over unchanged: DeployIQ does not regenerate this step.", cc.Instruction)
	}
	if len(c.custom) > 0 {
		notes = append(notes, fmt.Sprintf("%d RUN step(s) the engine does not recognise were carried over unchanged into the runtime stage.", len(c.custom)))
	}
	if c.stopSignal != nil {
		rt.Raw("", *c.stopSignal)
	}
	if c.health != nil {
		rt.Raw("Health check from the existing Dockerfile.", *c.health)
	}
	switch {
	case runAsNode:
		rt.Note("The official image ships an unprivileged `node` user.", "USER", "node")
	case c.user != "":
		rt.Add("USER", c.user)
	}
	rt.Add("EXPOSE", strings.Join(ports, " "))
	if entry != nil {
		rt.Raw("", *entry)
	}
	if cmd != nil {
		rt.Raw("", *cmd)
	}
	if inferred != nil {
		rt.Exec("CMD", inferred)
	}

	// --- rules and estimate ------------------------------------------------------------
	applied := []string{RuleMultiStage, RuleLayerCaching, RuleNoPackageCache}
	if variant == dockerfile.VariantAlpine {
		applied = append(applied, RuleAlpineBase)
	} else {
		applied = append(applied, RuleSlimBase)
	}
	if prune != "" {
		applied = append(applied, RuleProdOnlyDeps)
	}
	if runAsNode || had[RuleNonRootUser] {
		applied = append(applied, RuleNonRootUser)
	}
	if dropped := c.droppedBuildTools(); len(dropped) > 0 || len(n.NativeModules) > 0 {
		applied = append(applied, RuleDropBuildTools)
	}
	if c.existing {
		had[RuleMultiStage] = ctx.Existing.MultiStage()
		had[RuleLayerCaching] = hadLayerCaching(ctx.Existing, constants.ManifestPackageJSON, n.Lockfile)
		had[RuleProdOnlyDeps] = !c.analysis.DevDepsInstalled
		// npm and yarn keep their cache under $HOME; a single-stage image
		// ships it unless the same build cleans it.
		had[RuleNoPackageCache] = hadNoCache(c.analysis) &&
			(ctx.Existing.MultiStage() || cacheCleaned(c.analysis, "npm cache clean", "yarn cache clean", "pnpm store prune"))
		had[RuleDropBuildTools] = len(c.droppedBuildTools()) == 0 && ctx.Existing.MultiStage()
	}
	improved, present := split(applied, had)

	prodMB, allMB := analyzer.NodeDependenciesMB(n)
	srcMB := analyzer.SourceMB(ctx.Root)
	after := analyzer.Profile{BaseImage: image, SystemPackages: runtimePkgs, DependenciesMB: prodMB, SourceMB: srcMB}
	if prune == "" {
		after.DependenciesMB = allMB
	}
	before, label := nodeBefore(c, major, prodMB, allMB, srcMB)

	if !analyzer.HasDockerignore(ctx.Root) {
		notes = append(notes, "No .dockerignore: `COPY . .` will send node_modules and .git into the build. Add a .dockerignore listing node_modules and .git.")
	}

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

// nodeInstall returns the full install command and the prune that follows the
// build. Prune-after-build, rather than a second production-only install, is
// deliberate: lifecycle scripts (prepare, postinstall — husky, prisma
// generate) run once, with dev dependencies available, exactly as they do on
// the developer's machine.
func nodeInstall(n *detector.NodeInfo, major int) (install, prune string) {
	omit := "--omit=dev"
	if major > 0 && major < 16 {
		omit = "--production" // npm 6 predates --omit
	}
	switch n.PackageManager {
	case detector.PackageManagerPNPM:
		frozen := ""
		if n.Lockfile != "" {
			frozen = " --frozen-lockfile"
		}
		return "corepack enable && pnpm install" + frozen, "pnpm prune --prod"
	case detector.PackageManagerYarn:
		if n.YarnBerry {
			immutable := ""
			if n.Lockfile != "" {
				immutable = " --immutable"
			}
			return "corepack enable && yarn install" + immutable, ""
		}
		frozen := ""
		if n.Lockfile != "" {
			frozen = " --frozen-lockfile"
		}
		// The yarn cache lives outside the workdir, so it never reaches the
		// runtime stage; --prefer-offline reuses it for the prune.
		return "yarn install" + frozen,
			"yarn install --production" + frozen + " --ignore-scripts --prefer-offline"
	default:
		if n.Lockfile != "" {
			return "npm ci", "npm prune " + omit
		}
		return "npm install", "npm prune " + omit
	}
}

func runScript(n *detector.NodeInfo, script string) string {
	switch n.PackageManager {
	case detector.PackageManagerPNPM:
		return "pnpm run " + script
	case detector.PackageManagerYarn:
		return "yarn run " + script
	}
	return "npm run " + script
}

// inferNodeStart decides how to start an app with no existing CMD: a simple
// `node <file>` start script is run directly (no npm process in between, so
// signals reach the app); any other start script goes through the package
// manager; then main, then conventional entry files.
func inferNodeStart(root string, n *detector.NodeInfo) ([]string, error) {
	if s := strings.TrimSpace(n.Scripts["start"]); s != "" {
		f := strings.Fields(s)
		if len(f) == 2 && f[0] == "node" && !strings.ContainsAny(f[1], "$&|;<>") {
			return []string{"node", f[1]}, nil
		}
		switch n.PackageManager {
		case detector.PackageManagerYarn:
			return []string{"yarn", "start"}, nil
		case detector.PackageManagerPNPM:
			return []string{"pnpm", "start"}, nil
		}
		return []string{"npm", "start"}, nil
	}
	if n.Main != "" && fileExists(root, n.Main) {
		return []string{"node", n.Main}, nil
	}
	for _, f := range []string{"server.js", "index.js", "app.js", "main.js", "server.mjs", "index.mjs"} {
		if fileExists(root, f) {
			return []string{"node", f}, nil
		}
	}
	return nil, unsupported("could not infer how to start this app: package.json has no start script or main, and no server.js/index.js/app.js exists. Add a \"start\" script, or a Dockerfile with a CMD")
}

// nodeBefore builds the "before" profile: the existing image, or a
// conventional single-stage baseline (full node image, all dependencies, npm
// cache left behind) when there is no Dockerfile.
func nodeBefore(c *carried, major int, prodMB, allMB, srcMB float64) (analyzer.Profile, string) {
	if !c.existing {
		return analyzer.Profile{
			BaseImage:      "node:" + strconv.Itoa(major),
			DependenciesMB: allMB,
			CacheMB:        analyzer.NodeCacheMB(allMB),
			SourceMB:       srcMB,
		}, "conventional single-stage baseline (no Dockerfile found)"
	}
	p := analyzer.Profile{BaseImage: c.runtime.Ref, SourceMB: srcMB, DependenciesMB: prodMB}
	if c.analysis.DevDepsInstalled {
		p.DependenciesMB = allMB
	}
	if !c.multiStage {
		p.SystemPackages = c.packages
		if !cacheCleaned(c.analysis, "npm cache clean", "yarn cache clean") {
			p.CacheMB = analyzer.NodeCacheMB(p.DependenciesMB)
		}
		if c.packageManager == "apt" && !c.analysis.AptListsCleaned {
			p.CacheMB += aptListsMB
		}
	} else {
		p.SystemPackages = c.runtimePackages()
	}
	return p, "current Dockerfile"
}
