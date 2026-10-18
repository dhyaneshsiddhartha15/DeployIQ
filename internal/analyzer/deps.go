package analyzer

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
)

// nodePackageMB is the average installed size of one npm package. Small
// utilities dominate by count, a few large packages dominate by bytes; 0.25 MB
// lands within a factor of two for typical service repositories, which is the
// precision this estimate claims.
const nodePackageMB = 0.25

// nodeTransitiveFactor approximates how many packages one direct dependency
// pulls in, for repos without a lockfile to count from.
const nodeTransitiveFactor = 8

// NodeDependenciesMB estimates installed node_modules size: production only,
// and everything including dev dependencies.
func NodeDependenciesMB(n *detector.NodeInfo) (prodMB, allMB float64) {
	if n.LockPackages > 0 {
		all := float64(n.LockPackages) * nodePackageMB
		prod := float64(n.LockPackages-n.LockDevPackages) * nodePackageMB
		return prod, all
	}
	prod := float64(len(n.Dependencies)*nodeTransitiveFactor) * nodePackageMB
	all := float64((len(n.Dependencies)+len(n.DevDependencies))*nodeTransitiveFactor) * nodePackageMB
	return prod, all
}

// pythonPackageMB is the approximate installed size of well-known heavy
// distributions, including the dependencies they bring.
var pythonPackageMB = map[string]float64{
	"numpy": 40, "pandas": 70, "scipy": 110, "scikit-learn": 45, "matplotlib": 30,
	"torch": 900, "tensorflow": 1100, "tensorflow-cpu": 600, "opencv-python": 90,
	"opencv-python-headless": 70, "pillow": 12, "boto3": 90, "botocore": 85,
	"django": 30, "flask": 4, "fastapi": 12, "uvicorn": 6, "gunicorn": 3,
	"sqlalchemy": 15, "psycopg2": 6, "psycopg2-binary": 10, "psycopg": 4,
	"pydantic": 8, "celery": 12, "redis": 2, "requests": 2, "grpcio": 25,
	"cryptography": 10, "lxml": 20, "pyarrow": 120, "polars": 90, "transformers": 80,
	"mysqlclient": 2, "aiohttp": 8, "httpx": 3, "jinja2": 2, "werkzeug": 2,
	"google-cloud-storage": 30, "azure-storage-blob": 20, "kubernetes": 20,
}

// defaultPythonPackageMB covers an unlisted distribution and its share of
// shared dependencies.
const defaultPythonPackageMB = 3

// PythonDependenciesMB estimates installed site-packages size.
func PythonDependenciesMB(p *detector.PythonInfo) float64 {
	total := 0.0
	for _, r := range p.Requirements {
		if mb, ok := pythonPackageMB[r]; ok {
			total += mb
		} else {
			total += defaultPythonPackageMB
		}
	}
	// pip, setuptools and wheel live in every venv/site-packages.
	return total + 15
}

// PipCacheMB estimates the wheel cache pip leaves behind without
// --no-cache-dir: wheels are compressed, roughly a third of installed size.
func PipCacheMB(depsMB float64) float64 { return depsMB / 3 }

// NodeCacheMB estimates the npm/yarn cache an install leaves in the image when
// it is not cleaned: compressed tarballs, about half the installed size.
func NodeCacheMB(depsMB float64) float64 { return depsMB / 2 }

// GoBinaryMB estimates a stripped static binary: a small runtime floor plus a
// share per required module, capped where real services top out.
func GoBinaryMB(g *detector.GoInfo) float64 {
	mb := 6 + 0.3*float64(g.Requires)
	return minFloat(mb, 80)
}

// GoModCacheMB estimates the module cache plus build cache a single-stage
// build leaves in the image.
func GoModCacheMB(g *detector.GoInfo) float64 {
	return 3*float64(g.Requires) + 40
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// JavaArtifactMB estimates the runnable artifact: a Spring Boot fat jar carries
// its dependencies (typically 20–60 MB for a web service); a plain jar with its
// runtime classpath is smaller.
func JavaArtifactMB(j *detector.JavaInfo) float64 {
	if j.SpringBoot {
		return 25
	}
	return 8
}

// JavaDepsCacheMB estimates the local repository (~/.m2 or the Gradle cache) a
// single-stage build leaves behind: every plugin and dependency, with sources
// of the build tool itself.
func JavaDepsCacheMB(j *detector.JavaInfo) float64 {
	if j.SpringBoot {
		return 180
	}
	if j.Tool == detector.JavaToolGradle {
		return 120
	}
	return 60
}

// SourceExclusions are never counted as application source: they are either
// rebuilt in the image (node_modules) or not application files.
var SourceExclusions = []string{".git", "node_modules", "target", ".gradle", "__pycache__", ".venv", "venv", ".mypy_cache", ".pytest_cache", ".tox", ".idea", ".vscode"}

// maxSourceFiles bounds the walk; beyond it the remainder is extrapolated. A
// 10,000-file repo must finish analysis in under five seconds (Phase 1.2).
const maxSourceFiles = 20000

// SourceMB sums the files a `COPY . .` would send, honouring top-level
// .dockerignore patterns and SourceExclusions. Directories named in only (if
// non-empty) are the only ones counted, for runtimes that copy just those.
func SourceMB(root string, only ...string) float64 {
	ignore := readDockerignore(root)
	excluded := map[string]bool{}
	for _, e := range SourceExclusions {
		excluded[e] = true
	}
	var total int64
	files := 0

	walkRoot := func(start string) {
		_ = filepath.WalkDir(start, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if rel == "." {
				return nil
			}
			if excluded[d.Name()] || matchesAny(ignore, rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			files++
			if files > maxSourceFiles {
				return filepath.SkipAll
			}
			if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
				total += info.Size()
			}
			return nil
		})
	}

	if len(only) == 0 {
		walkRoot(root)
	} else {
		for _, dir := range only {
			walkRoot(filepath.Join(root, dir))
		}
	}
	return float64(total) / (1 << 20)
}

// readDockerignore returns the patterns in <root>/.dockerignore. Negations
// (!pattern) are ignored: treating a re-included file as excluded only makes
// the estimate smaller on both sides, never inflates the saving.
func readDockerignore(root string) []string {
	f, err := os.Open(filepath.Join(root, ".dockerignore"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(line), "/"), "/"))
	}
	return out
}

// HasDockerignore reports whether the repo has a .dockerignore.
func HasDockerignore(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".dockerignore"))
	return err == nil
}

func matchesAny(patterns []string, rel string) bool {
	for _, p := range patterns {
		p = strings.TrimPrefix(p, "**/")
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
		if ok, _ := path.Match(p, path.Base(rel)); ok && !strings.Contains(p, "/") {
			return true
		}
	}
	return false
}
