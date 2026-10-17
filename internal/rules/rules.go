package rules

import (
	"errors"
	"fmt"
	"sort"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/analyzer"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/version"
)

// ErrUnsupported marks a repository the engine refuses to generate for. The
// wrapped message says why, in terms the user can act on. Phase 1.4: fail
// safely with a clear message rather than guessing.
var ErrUnsupported = errors.New("not supported in this version")

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, args...))
}

// Rule identifiers. Reported to the API and stored in the optimizations
// collection (Phase 4.5), so they are a persisted contract: renaming one
// silently reinterprets every historical record. Add, never rename.
const (
	RuleMultiStage     = "multi-stage-build"
	RuleAlpineBase     = "alpine-base"
	RuleSlimBase       = "slim-base"
	RuleDistrolessBase = "distroless-base"
	RuleProdOnlyDeps   = "prod-only-deps"
	RuleLayerCaching   = "layer-caching"
	RuleNonRootUser    = "non-root-user"
	RuleStaticBinary   = "static-binary"
	RuleNoPackageCache = "no-package-cache"
	RuleDropBuildTools = "drop-build-tools"
)

// Descriptions explain each rule in one line for the terminal report.
var Descriptions = map[string]string{
	RuleMultiStage:     "build happens in a separate stage; only what the app needs at runtime ships",
	RuleAlpineBase:     "runtime uses the Alpine variant of the base image",
	RuleSlimBase:       "runtime uses the slim Debian variant of the base image",
	RuleDistrolessBase: "runtime is distroless: no shell, no package manager, minimal attack surface",
	RuleProdOnlyDeps:   "only production dependencies are present in the final image",
	RuleLayerCaching:   "dependency manifests are copied before source, so code edits reuse the dependency layer",
	RuleNonRootUser:    "the container runs as an unprivileged user",
	RuleStaticBinary:   "binary is built with CGO disabled, stripped and path-trimmed",
	RuleNoPackageCache: "package-manager caches are disabled or removed in the same layer",
	RuleDropBuildTools: "compilers and build-only OS packages stay out of the runtime image",
}

// order is the presentation order of rules in reports and in RulesApplied.
var order = []string{
	RuleMultiStage, RuleDistrolessBase, RuleAlpineBase, RuleSlimBase, RuleProdOnlyDeps,
	RuleDropBuildTools, RuleStaticBinary, RuleLayerCaching, RuleNoPackageCache, RuleNonRootUser,
}

// Context is everything a stack generator works from.
type Context struct {
	// Root is the absolute repository path.
	Root string

	// Detection is the detector's result.
	Detection *detector.Result

	// Existing is the parsed Dockerfile, or nil when the repo has none.
	Existing *dockerfile.Dockerfile
}

// Result is a generated Dockerfile and everything the report needs about it.
type Result struct {
	Stack string

	// Dockerfile is the rendered output.
	Dockerfile []byte

	// RulesApplied are improvements over the existing Dockerfile (or over a
	// conventional baseline when there is none), in presentation order.
	RulesApplied []string

	// AlreadyPresent are rules the generated file uses that the existing
	// Dockerfile already applied.
	AlreadyPresent []string

	// Notes are things the user should know before trusting the output:
	// carried-over steps, assumptions, defaults chosen.
	Notes []string

	// Before and After are the size-estimate inputs. BeforeLabel names what
	// "before" means: the existing Dockerfile, or a baseline.
	Before, After analyzer.Profile
	BeforeLabel   string
}

// Generator produces an optimized Dockerfile for one stack.
//
// Phase 13.2 records the known ceiling of this design: v1 is one generator per
// stack, each applying its rules in a fixed order. As coverage grows this must
// become multiple applicable rules, ranked and composed. The seam is here — a
// generator already reports rules individually, so splitting one into
// composable steps changes no caller.
type Generator interface {
	Stack() string
	Generate(ctx *Context) (*Result, error)
}

var registry = map[string]Generator{}

func register(g Generator) { registry[g.Stack()] = g }

func init() {
	register(nodeGenerator{})
	register(goGenerator{})
	register(pythonGenerator{})
}

// Generate dispatches to the stack's generator (FR-3).
func Generate(ctx *Context) (*Result, error) {
	if ctx == nil || ctx.Detection == nil {
		return nil, errors.New("rules: nil context")
	}
	g, ok := registry[ctx.Detection.Stack]
	if !ok {
		return nil, unsupported("no generator for stack %q", ctx.Detection.Stack)
	}
	res, err := g.Generate(ctx)
	if err != nil {
		return nil, err
	}
	res.Stack = ctx.Detection.Stack
	return res, nil
}

// header is the comment block every generated file starts with. It says what
// produced the file and that it needs review, because it will be read by
// someone deciding whether to trust it.
func header(stack string, existing bool) []string {
	src := "inferred from repository conventions (no Dockerfile was found)"
	if existing {
		src = "derived from the existing Dockerfile"
	}
	return []string{
		"Generated by DeployIQ Optimizer " + version.Version + " for a " + stack + " project,",
		src + ".",
		"Review it before use. Build with:  docker build -f Dockerfile.optimized .",
	}
}

// split partitions the generated file's rules into improvements and rules the
// existing Dockerfile already had.
func split(applied []string, had map[string]bool) (improved, present []string) {
	seen := map[string]bool{}
	for _, r := range applied {
		if seen[r] {
			continue
		}
		seen[r] = true
		if had[r] {
			present = append(present, r)
		} else {
			improved = append(improved, r)
		}
	}
	sortRules(improved)
	sortRules(present)
	return improved, present
}

func sortRules(rs []string) {
	rank := map[string]int{}
	for i, r := range order {
		rank[r] = i
	}
	sort.SliceStable(rs, func(i, j int) bool { return rank[rs[i]] < rank[rs[j]] })
}
