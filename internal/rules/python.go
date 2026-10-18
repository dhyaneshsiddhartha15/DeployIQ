package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/analyzer"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// DefaultPythonVersion is used when nothing names an interpreter version.
const DefaultPythonVersion = "3.12"

const defaultPythonPort = "8000"

// poetrySpec pins the Poetry major versions the generated install is written
// for; `install --only main --no-root` behaves the same across them.
const poetrySpec = "poetry>=1.8,<3"

// pythonSystemDeps maps distributions that compile against system libraries
// to the build-time and runtime packages they need on Debian. These have no
// manylinux wheels (or the project pins a source build), so pip compiles them.
var pythonSystemDeps = map[string]struct{ build, runtime []string }{
	"psycopg2":    {build: []string{"gcc", "libpq-dev"}, runtime: []string{"libpq5"}},
	"mysqlclient": {build: []string{"gcc", "pkg-config", "default-libmysqlclient-dev"}, runtime: []string{"libmariadb3"}},
}

type pythonGenerator struct{}

func (pythonGenerator) Stack() string { return constants.StackPython }

func (pythonGenerator) Generate(ctx *Context) (*Result, error) {
	p := ctx.Detection.Python
	c, err := carry(ctx, "/app")
	if err != nil {
		return nil, err
	}
	var notes []string
	had := map[string]bool{}

	// --- version and variant ------------------------------------------------
	version := ""
	if c.existing && c.runtime.Family() == dockerfile.FamilyPython {
		if maj, minor := c.runtime.Version(); maj == 3 && minor >= 0 {
			version = fmt.Sprintf("3.%d", minor)
		}
	}
	if version == "" {
		version = p.Version
	}
	if version == "" {
		version = DefaultPythonVersion
		notes = append(notes, "No Python version found (Dockerfile, .python-version, requires-python); using "+DefaultPythonVersion+".")
	}

	// slim, not Alpine: most wheels on PyPI are built for glibc, and on musl
	// pip falls back to compiling from source — slower, larger, and a common
	// way for a "smaller" image to stop building. An image that is already
	// Alpine stays Alpine.
	variant, osFamily := "slim", "debian"
	if c.existing && c.runtime.Family() == dockerfile.FamilyPython && c.runtime.Variant() == dockerfile.VariantAlpine {
		variant, osFamily = "alpine", "alpine"
		had[RuleAlpineBase] = true
	}
	image := "python:" + version + "-" + variant
	if image, variant, osFamily = keepCustomBase(c, dockerfile.FamilyPython, image, variant, osFamily, &notes); c.runtime.Custom() != nil {
		had[RuleSlimBase], had[RuleAlpineBase] = true, true
	}
	if c.packageManager == "dnf" && len(c.runtimePackages()) > 0 && osFamily != "rhel" {
		return nil, unsupported("the existing image installs RHEL-family packages (%s); translating them is not supported", strings.Join(c.runtimePackages(), " "))
	}
	if osFamily == "debian" && c.packageManager == "apk" && len(c.packages) > 0 {
		return nil, unsupported("the existing image installs Alpine packages (%s) but the runtime would be Debian; translating package names is not supported", strings.Join(c.packages, " "))
	}
	if c.existing && c.runtime.Family() == dockerfile.FamilyPython && c.runtime.Variant() == dockerfile.VariantSlim {
		had[RuleSlimBase] = true
	}

	buildPkgs := c.buildPackages()
	runtimePkgs := c.runtimePackages()
	if osFamily == "debian" {
		dists := make([]string, 0, len(pythonSystemDeps))
		for d := range pythonSystemDeps {
			dists = append(dists, d)
		}
		sort.Strings(dists) // deterministic output
		for _, dist := range dists {
			deps := pythonSystemDeps[dist]
			if p.Has(dist) {
				buildPkgs = addMissing(buildPkgs, deps.build...)
				runtimePkgs = addMissing(runtimePkgs, deps.runtime...)
				notes = append(notes, dist+" compiles against system libraries: "+strings.Join(deps.build, " ")+" in the build stage, "+strings.Join(deps.runtime, " ")+" at runtime.")
			}
		}
	}
	tools := c.requiredTools(ctx.Root)
	var needed []string
	for _, t := range tools {
		if (t != "bash" || osFamily != "debian") && (t != "wget" || osFamily != "alpine") {
			needed = append(needed, t)
		}
	}
	if len(needed) > 0 {
		runtimePkgs = addMissing(runtimePkgs, needed...)
		notes = append(notes, "Added "+strings.Join(needed, ", ")+" to the runtime image: the start command, health check or a carried step uses it.")
	}

	// --- start command ------------------------------------------------------------
	var inferred []string
	if !c.hasStartCommand() {
		inferred, err = inferPythonStart(p)
		if err != nil {
			return nil, err
		}
		notes = append(notes, "Start command inferred: "+strings.Join(inferred, " ")+".")
		if len(inferred) > 2 && inferred[1] == "manage.py" {
			notes = append(notes, "Django's runserver is a development server. Add gunicorn to requirements to get a production command.")
		}
	}
	ports := c.ports
	if len(ports) == 0 {
		ports = []string{defaultPythonPort}
		notes = append(notes, "No EXPOSE found; documenting port "+defaultPythonPort+". EXPOSE does not publish anything, so a wrong guess is harmless.")
	}

	nonRoot := false
	switch {
	case c.user != "" && !userIsRoot(c.user):
		had[RuleNonRootUser] = true
	case lowPort(ports):
		notes = append(notes, "The app exposes a port below 1024, which an unprivileged user cannot bind; the container keeps running as root.")
	case c.user != "":
		notes = append(notes, "The existing Dockerfile explicitly runs as root; that is kept.")
	default:
		nonRoot = true
	}

	// --- build stage --------------------------------------------------------------
	f := &dockerfile.File{Header: header(constants.StackPython, c.existing)}
	build := f.AddStage(image, "build", "--- build stage: compilers and pip stay here; only the virtualenv moves on ---")
	build.Add("ENV", "PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1")
	if idx := ctx.Packages.PyPI; idx != "" {
		build.Note("Internal Python package index, from .doiq.yml.", "ENV", "PIP_INDEX_URL="+idx+" POETRY_REPOSITORIES_INTERNAL_URL="+idx)
	}
	if len(buildPkgs) > 0 {
		build.Note("Build-time OS packages.", "RUN", osInstall(osFamily, buildPkgs))
	}
	build.Note("Dependencies go into a virtualenv so the runtime stage can copy them as one directory.", "RUN", "python -m venv /opt/venv")
	build.Add("ENV", `VIRTUAL_ENV=/opt/venv PATH="/opt/venv/bin:$PATH"`)
	build.Add("WORKDIR", c.workdir)

	cachedLayer := true
	switch {
	case p.Tool == detector.PythonToolPoetry:
		files := "pyproject.toml"
		if p.HasPoetryLock {
			files += " poetry.lock"
		}
		build.Note("Poetry lives in its own venv so it does not end up in the app's.", "RUN",
			"python -m venv /opt/poetry && /opt/poetry/bin/pip install \""+poetrySpec+"\"")
		build.Note("Manifests first: the install layer is reused until dependencies change.", "COPY", files+" ./")
		build.Add("RUN", "/opt/poetry/bin/poetry install --only main --no-root --no-interaction --no-ansi")
		if !p.HasPoetryLock {
			notes = append(notes, "No poetry.lock: dependency versions are resolved at build time, so builds are not reproducible. Commit poetry.lock.")
		}
	case p.RequirementsFile != "" && !p.LocalReferences:
		files := append([]string{p.RequirementsFile}, p.IncludedFiles...)
		build.Note("Requirements first: the install layer is reused until dependencies change.", "COPY", strings.Join(files, " ")+" ./")
		build.Add("RUN", "pip install -r "+p.RequirementsFile)
	case p.RequirementsFile != "":
		cachedLayer = false
		build.Note("requirements.txt references local paths, so the whole project is needed to install it.", "COPY", ". .")
		build.Add("RUN", "pip install -r "+p.RequirementsFile)
		notes = append(notes, "requirements.txt installs from local paths (-e . or ./pkg); dependency caching across code changes is not possible.")
	case len(p.RequirementSpecs) > 0:
		build.Note("Dependencies from pyproject.toml [project].dependencies.", "COPY", "pyproject.toml ./")
		build.Exec("RUN", append([]string{"pip", "install"}, p.RequirementSpecs...))
	default:
		cachedLayer = false
		build.Note("No static dependency list; installing the project itself.", "COPY", ". .")
		build.Add("RUN", "pip install .")
	}

	// --- runtime stage --------------------------------------------------------------
	rt := f.AddStage(image, "", "--- runtime stage ---")
	env := [][2]string{{"PYTHONDONTWRITEBYTECODE", "1"}, {"PYTHONUNBUFFERED", "1"}, {"VIRTUAL_ENV", "/opt/venv"}, {"PATH", `"/opt/venv/bin:$PATH"`}}
	rt.Add("ENV", envLine(env))
	if len(c.env) > 0 {
		rt.Add("ENV", envLine(c.env))
	}
	if len(runtimePkgs) > 0 {
		rt.Note("Runtime OS packages (build-only tools removed).", "RUN", osInstall(osFamily, runtimePkgs))
	}
	owner := ""
	if nonRoot {
		if osFamily == "alpine" {
			rt.Note("Unprivileged user for the app.", "RUN", "adduser -D -u 10001 app")
		} else {
			rt.Note("Unprivileged user for the app.", "RUN", "useradd --create-home --uid 10001 app")
		}
		owner = "--chown=app:app "
	}
	rt.Add("WORKDIR", c.workdir)
	rt.Note("", "COPY", "--from=build /opt/venv /opt/venv")
	rt.Add("COPY", owner+". .")
	for _, in := range c.passthrough {
		rt.Raw("Carried over from the existing Dockerfile.", in)
	}
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
	case nonRoot:
		rt.Add("USER", "app")
	case c.user != "":
		rt.Add("USER", c.user)
	}
	rt.Add("EXPOSE", strings.Join(ports, " "))
	if c.entrypoint != nil {
		rt.Raw("", *c.entrypoint)
	}
	if c.cmd != nil {
		rt.Raw("", *c.cmd)
	}
	if inferred != nil {
		rt.Exec("CMD", inferred)
	}

	// --- rules and estimate ------------------------------------------------------------
	applied := []string{RuleMultiStage, RuleNoPackageCache}
	if variant == "alpine" {
		applied = append(applied, RuleAlpineBase)
	} else {
		applied = append(applied, RuleSlimBase)
	}
	if cachedLayer {
		applied = append(applied, RuleLayerCaching)
	}
	if nonRoot || had[RuleNonRootUser] {
		applied = append(applied, RuleNonRootUser)
	}
	if len(buildPkgs) > 0 {
		applied = append(applied, RuleDropBuildTools)
	}
	if p.Tool == detector.PythonToolPoetry {
		applied = append(applied, RuleProdOnlyDeps)
	}
	if c.existing {
		had[RuleMultiStage] = ctx.Existing.MultiStage()
		had[RuleLayerCaching] = hadLayerCaching(ctx.Existing, "requirements.txt", "pyproject.toml", "poetry.lock")
		had[RuleNoPackageCache] = c.analysis.PipCacheDisabled && hadNoCache(c.analysis)
		had[RuleDropBuildTools] = len(c.droppedBuildTools()) == 0 && ctx.Existing.MultiStage()
		had[RuleProdOnlyDeps] = !c.analysis.DevDepsInstalled
	}
	improved, present := split(applied, had)

	depsMB := analyzer.PythonDependenciesMB(p)
	srcMB := analyzer.SourceMB(ctx.Root)
	after := analyzer.Profile{BaseImage: image, SystemPackages: runtimePkgs, DependenciesMB: depsMB, SourceMB: srcMB}
	before, label := pythonBefore(c, version, depsMB, srcMB)

	if !analyzer.HasDockerignore(ctx.Root) {
		notes = append(notes, "No .dockerignore: `COPY . .` will send .git, virtualenvs and caches into the image. Add a .dockerignore.")
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

// inferPythonStart picks a start command from what the repository contains,
// preferring production servers that are already dependencies. It never adds
// a dependency: a command that needs a package the project does not install
// fails at runtime, which is worse than refusing now.
func inferPythonStart(p *detector.PythonInfo) ([]string, error) {
	bind := "0.0.0.0:" + defaultPythonPort
	if _, ok := p.Entrypoints["manage.py"]; ok {
		if p.DjangoWSGI != "" && p.Has("gunicorn") {
			return []string{"gunicorn", "--bind", bind, p.DjangoWSGI + ":application"}, nil
		}
		return []string{"python", "manage.py", "runserver", bind}, nil
	}
	for _, file := range []string{"app.py", "main.py", "server.py", "run.py", "wsgi.py", "asgi.py"} {
		h, ok := p.Entrypoints[file]
		if !ok {
			continue
		}
		module := strings.TrimSuffix(file, ".py")
		appVar := h.AppVar
		if appVar == "" {
			appVar = "app"
		}
		switch {
		case h.FastAPI && p.Has("uvicorn"):
			return []string{"uvicorn", module + ":" + appVar, "--host", "0.0.0.0", "--port", defaultPythonPort}, nil
		case h.Flask && p.Has("gunicorn"):
			return []string{"gunicorn", "--bind", bind, module + ":" + appVar}, nil
		case h.MainGuard:
			return []string{"python", file}, nil
		case h.Flask:
			return []string{"python", "-m", "flask", "--app", module, "run", "--host", "0.0.0.0", "--port", defaultPythonPort}, nil
		}
	}
	return nil, unsupported("could not infer how to start this app: no app.py/main.py with a __main__ guard, Flask or FastAPI app was found. Add a Dockerfile with a CMD")
}

func pythonBefore(c *carried, version string, depsMB, srcMB float64) (analyzer.Profile, string) {
	if !c.existing {
		return analyzer.Profile{
			BaseImage:      "python:" + version,
			DependenciesMB: depsMB,
			CacheMB:        analyzer.PipCacheMB(depsMB),
			SourceMB:       srcMB,
		}, "conventional single-stage baseline (no Dockerfile found)"
	}
	p := analyzer.Profile{BaseImage: c.runtime.Ref, DependenciesMB: depsMB, SourceMB: srcMB}
	if c.multiStage {
		p.SystemPackages = c.runtimePackages()
		return p, "current Dockerfile"
	}
	p.SystemPackages = c.packages
	if !c.analysis.PipCacheDisabled {
		p.CacheMB = analyzer.PipCacheMB(depsMB)
	}
	if c.packageManager == "apt" && !c.analysis.AptListsCleaned {
		p.CacheMB += aptListsMB
	}
	return p, "current Dockerfile"
}
