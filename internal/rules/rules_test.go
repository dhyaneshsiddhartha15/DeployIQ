package rules

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/analyzer"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
)

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// generate runs the real pipeline front half over a repo.
func generate(t *testing.T, files map[string]string) (*Result, error) {
	t.Helper()
	dir := repo(t, files)
	det, err := detector.Detect(dir)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	df, err := dockerfile.Parse(dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Generate(&Context{Root: dir, Detection: det, Existing: df})
}

func mustGenerate(t *testing.T, files map[string]string) *Result {
	t.Helper()
	res, err := generate(t, files)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Every generated file must parse as a Dockerfile.
	if _, err := dockerfile.ParseBytes(res.Dockerfile); err != nil {
		t.Fatalf("generated Dockerfile does not parse: %v\n%s", err, res.Dockerfile)
	}
	return res
}

func contains(t *testing.T, res *Result, want ...string) {
	t.Helper()
	out := string(res.Dockerfile)
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func lacks(t *testing.T, res *Result, bad ...string) {
	t.Helper()
	out := string(res.Dockerfile)
	for _, b := range bad {
		if strings.Contains(out, b) {
			t.Errorf("unexpected %q in:\n%s", b, out)
		}
	}
}

func hasRule(res *Result, id string) bool {
	for _, r := range res.RulesApplied {
		if r == id {
			return true
		}
	}
	return false
}

func hasNote(res *Result, sub string) bool {
	for _, n := range res.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func shrinks(t *testing.T, res *Result) {
	t.Helper()
	e := analyzer.Compare(res.Before, res.After)
	if e.SavedMB <= 0 {
		t.Errorf("expected an estimated saving, got before=%.0f after=%.0f", e.Before.TotalMB, e.After.TotalMB)
	}
}

// --- Node -------------------------------------------------------------------------

const lock = `{"lockfileVersion":3,"packages":{"":{},"node_modules/express":{},"node_modules/jest":{"dev":true}}}`

func TestNodeNaiveDockerfile(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"package.json":      `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4"},"devDependencies":{"jest":"29"}}`,
		"package-lock.json": lock,
		"server.js":         "",
		".dockerignore":     "node_modules\n",
		"Dockerfile":        "FROM node:18\nWORKDIR /usr/src/app\nCOPY . .\nRUN npm install\nENV PORT=8080\nEXPOSE 8080\nCMD [\"node\", \"server.js\"]\n",
	})
	contains(t, res,
		"FROM node:18-alpine AS build",
		"WORKDIR /usr/src/app",
		"COPY package.json package-lock.json ./",
		"RUN npm ci",
		"RUN npm prune --omit=dev",
		"ENV NODE_ENV=production PORT=8080",
		"COPY --from=build --chown=node:node /usr/src/app /usr/src/app",
		"USER node",
		"EXPOSE 8080",
		`CMD ["node", "server.js"]`,
	)
	for _, r := range []string{RuleMultiStage, RuleAlpineBase, RuleProdOnlyDeps, RuleLayerCaching, RuleNonRootUser, RuleNoPackageCache} {
		if !hasRule(res, r) {
			t.Errorf("rule %s not applied: %v", r, res.RulesApplied)
		}
	}
	if res.BeforeLabel != "current Dockerfile" {
		t.Error(res.BeforeLabel)
	}
	shrinks(t, res)
}

func TestNodeInferenceWithoutDockerfile(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"package.json": `{"scripts":{"build":"tsc","start":"node dist/index.js"},"engines":{"node":">=20"}}`,
		"yarn.lock":    "",
	})
	contains(t, res, "FROM node:20-alpine AS build", "RUN yarn install --frozen-lockfile", "RUN yarn run build",
		"yarn install --production --frozen-lockfile --ignore-scripts --prefer-offline", `CMD ["node", "dist/index.js"]`, "EXPOSE 3000")
	if !hasNote(res, "Start command inferred") || !hasNote(res, "No .dockerignore") {
		t.Errorf("notes: %v", res.Notes)
	}
	if !strings.Contains(res.BeforeLabel, "baseline") {
		t.Error(res.BeforeLabel)
	}
	shrinks(t, res)
}

func TestNodeStartInference(t *testing.T) {
	cases := []struct {
		files map[string]string
		want  string
	}{
		{map[string]string{"package.json": `{"scripts":{"start":"next start"}}`}, `CMD ["npm", "start"]`},
		{map[string]string{"package.json": `{"scripts":{"start":"next start"}}`, "pnpm-lock.yaml": ""}, `CMD ["pnpm", "start"]`},
		{map[string]string{"package.json": `{"main":"lib/main.js"}`, "lib/main.js": ""}, `CMD ["node", "lib/main.js"]`},
		{map[string]string{"package.json": `{}`, "index.js": ""}, `CMD ["node", "index.js"]`},
	}
	for _, c := range cases {
		res := mustGenerate(t, c.files)
		contains(t, res, c.want)
	}
	_, err := generate(t, map[string]string{"package.json": `{}`})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestNodePackageManagers(t *testing.T) {
	res := mustGenerate(t, map[string]string{"package.json": `{"main":"a.js"}`, "a.js": "", "pnpm-lock.yaml": ""})
	contains(t, res, "corepack enable && pnpm install --frozen-lockfile", "pnpm prune --prod", "COREPACK_ENABLE_DOWNLOAD_PROMPT=0")

	res = mustGenerate(t, map[string]string{"package.json": `{"main":"a.js"}`, "a.js": "", "yarn.lock": "", ".yarnrc.yml": "", ".yarn/releases/x": ""})
	contains(t, res, "corepack enable && yarn install --immutable", "COPY package.json yarn.lock .yarnrc.yml ./", "COPY .yarn ./.yarn")
	if hasRule(res, RuleProdOnlyDeps) || !hasNote(res, "Yarn Berry") {
		t.Errorf("berry cannot prune: rules=%v", res.RulesApplied)
	}

	res = mustGenerate(t, map[string]string{"package.json": `{"main":"a.js","engines":{"node":"14"}}`, "a.js": ""})
	contains(t, res, "RUN npm install", "npm prune --production")
}

func TestNodeNativeModulesAndPackages(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"package.json":      `{"main":"a.js","dependencies":{"bcrypt":"5"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{"":{},"node_modules/bcrypt":{"hasInstallScript":true}}}`,
		"a.js":              "",
		"Dockerfile":        "FROM node:20\nRUN apt-get update && apt-get install -y imagemagick build-essential\nCOPY . .\nRUN npm install\nHEALTHCHECK CMD curl -f http://localhost:3000/ || exit 1\nCMD node a.js\n",
	})
	contains(t, res, "FROM node:20-slim AS build", "python3 make g++", "apt-get install -y --no-install-recommends imagemagick curl", "HEALTHCHECK CMD curl", "CMD node a.js")
	lacks(t, res, "build-essential curl")
	if !hasRule(res, RuleDropBuildTools) || !hasRule(res, RuleSlimBase) {
		t.Errorf("rules: %v", res.RulesApplied)
	}
}

func TestNodeCarriesCustomStepsAndCopies(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"package.json": `{"main":"a.js"}`,
		"a.js":         "",
		"Dockerfile": `FROM node:20-alpine
WORKDIR /app
COPY . .
COPY nginx.conf /etc/nginx/nginx.conf
ADD https://example.com/x.tgz /opt/x.tgz
LABEL org.opencontainers.image.source=x
RUN npm ci --omit=dev
RUN node scripts/migrate.js && echo ok
USER 1000
EXPOSE 80
CMD ["node","a.js"]
`,
	})
	contains(t, res, "COPY nginx.conf /etc/nginx/nginx.conf", "ADD https://example.com/x.tgz /opt/x.tgz",
		"LABEL org.opencontainers.image.source=x", "RUN node scripts/migrate.js && echo ok", "USER 1000", "EXPOSE 80")
	if !hasNote(res, "carried over unchanged") {
		t.Errorf("notes: %v", res.Notes)
	}
	// Alpine and non-root were already there; the report must not claim them.
	for _, r := range []string{RuleAlpineBase, RuleNonRootUser, RuleProdOnlyDeps} {
		if hasRule(res, r) {
			t.Errorf("%s claimed as new: %v (present %v)", r, res.RulesApplied, res.AlreadyPresent)
		}
	}
}

func TestNodeLowPortKeepsRoot(t *testing.T) {
	res := mustGenerate(t, map[string]string{"package.json": `{"main":"a.js"}`, "a.js": "", "Dockerfile": "FROM node:20\nEXPOSE 80\nCMD [\"node\",\"a.js\"]\n"})
	lacks(t, res, "USER node")
	if !hasNote(res, "below 1024") || hasRule(res, RuleNonRootUser) {
		t.Errorf("notes=%v rules=%v", res.Notes, res.RulesApplied)
	}
}

func TestNodeRefusals(t *testing.T) {
	cases := map[string]map[string]string{
		"workspaces":    {"package.json": `{"workspaces":["a"]}`},
		"custom base":   {"package.json": `{"main":"a.js"}`, "a.js": "", "Dockerfile": "FROM mycorp/node-base:1\n"},
		"multi-lang":    {"package.json": `{"main":"a.js"}`, "a.js": "", "Dockerfile": "FROM python:3.12 AS py\nFROM node:20\nCOPY --from=py /x /x\n"},
		"rhel packages": {"package.json": `{"main":"a.js"}`, "a.js": "", "Dockerfile": "FROM node:20\nRUN yum install -y libfoo\n"},
	}
	for name, files := range cases {
		_, err := generate(t, files)
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: want ErrUnsupported, got %v", name, err)
		}
	}
}

// --- Go ----------------------------------------------------------------------------

func TestGoNoDockerfile(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"go.mod":               "module example.com/acme/api\n\ngo 1.23\n\nrequire github.com/x/y v1.0.0\n",
		"go.sum":               "",
		"main.go":              "package main\nfunc main(){}\n",
		"templates/index.html": "x",
		"static/app.go":        "package static\n",
	})
	contains(t, res,
		"FROM golang:1.23-alpine AS build",
		"COPY go.mod go.sum ./",
		"RUN go mod download",
		`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api .`,
		"FROM gcr.io/distroless/static-debian12:nonroot",
		"COPY --from=build /out/api /app/api",
		"COPY templates ./templates",
		"USER nonroot:nonroot",
		`ENTRYPOINT ["/app/api"]`,
	)
	lacks(t, res, "COPY static") // only Go files there
	for _, r := range []string{RuleMultiStage, RuleDistrolessBase, RuleStaticBinary, RuleNonRootUser, RuleDropBuildTools} {
		if !hasRule(res, r) {
			t.Errorf("missing %s: %v", r, res.RulesApplied)
		}
	}
	shrinks(t, res)
}

func TestGoExistingBuildIsHonoured(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"go.mod":            "module example.com/svc\n\ngo 1.21\n",
		"cmd/svc/main.go":   "package main\nfunc main(){}\n",
		"cmd/other/main.go": "package main\nfunc main(){}\n",
		"Dockerfile": `FROM golang:1.22
WORKDIR /go/src/app
COPY . .
RUN go build -tags netgo -ldflags "-X main.version=1.2.3" -o bin/svc ./cmd/svc
CMD ./bin/svc --port 8080
`,
	})
	contains(t, res,
		"FROM golang:1.22 AS build", // the existing builder, newer than go.mod asks for
		`-tags=netgo -ldflags="-s -w -X main.version=1.2.3" -o /out/svc ./cmd/svc`,
		"COPY --from=build /out/svc /go/src/app/bin/svc",
		`CMD ["./bin/svc", "--port", "8080"]`,
	)
	if !hasNote(res, "converted to exec form") {
		t.Errorf("notes: %v", res.Notes)
	}
}

func TestGoRunAndPathBinary(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"go.mod":     "module m\n\ngo 1.22\n",
		"main.go":    "package main\nfunc main(){}\n",
		"Dockerfile": "FROM golang:1.22\nWORKDIR /app\nCOPY . .\nENTRYPOINT [\"go\", \"run\", \"main.go\"]\nCMD [\"-v\"]\n",
	})
	contains(t, res, `ENTRYPOINT ["/app/m"]`, `CMD ["-v"]`)

	res = mustGenerate(t, map[string]string{
		"go.mod":     "module m\n\ngo 1.22\n",
		"main.go":    "package main\nfunc main(){}\n",
		"Dockerfile": "FROM golang:1.22\nRUN go install .\nCMD [\"m\"]\n",
	})
	contains(t, res, "COPY --from=build /out/m /usr/local/bin/m", `CMD ["m"]`)
}

func TestGoNeedsOSRuntime(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"go.mod":        "module m\n\ngo 1.22\n",
		"main.go":       "package main\nfunc main(){}\n",
		"entrypoint.sh": "#!/bin/bash\nexec /usr/local/bin/m\n",
		"Dockerfile": `FROM golang:1.22
RUN apt-get update && apt-get install -y ffmpeg git
WORKDIR /app
COPY . .
RUN go build -o /usr/local/bin/m .
RUN mkdir -p /data && chown 1000 /data
ENTRYPOINT ["./entrypoint.sh"]
`,
	})
	contains(t, res,
		"FROM golang:1.22 AS build", // the existing builder is reused
		"FROM debian:bookworm-slim",
		"apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata bash",
		"COPY --from=build /out/m /usr/local/bin/m",
		"RUN mkdir -p /data && chown 1000 /data",
		"useradd --system",
		`ENTRYPOINT ["./entrypoint.sh"]`,
	)
	if !hasNote(res, "rather than distroless") || hasRule(res, RuleDistrolessBase) {
		t.Errorf("notes=%v rules=%v", res.Notes, res.RulesApplied)
	}

	// Without an apt history, Alpine is the smaller choice.
	res = mustGenerate(t, map[string]string{
		"go.mod":     "module m\n\ngo 1.22\n",
		"main.go":    "package main\nfunc main(){}\n",
		"Dockerfile": "FROM golang:1.22\nWORKDIR /app\nCOPY . .\nRUN go build -o server .\nCMD ./server > /var/log/app.log\n",
	})
	contains(t, res, "FROM alpine:3.20", "adduser -D -H -u 10001 app", "CMD ./server > /var/log/app.log")
}

func TestGoCgo(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"go.mod":  "module m\n\ngo 1.22\n",
		"main.go": "package main\nimport \"C\"\nfunc main(){}\n",
	})
	contains(t, res, "golang:1.22-bookworm", "CGO_ENABLED=1", "gcr.io/distroless/base-debian12:nonroot")
	if hasRule(res, RuleStaticBinary) {
		t.Error("a cgo binary is not static")
	}
}

func TestGoRefusals(t *testing.T) {
	cases := map[string]map[string]string{
		"no main":    {"go.mod": "module m\n", "lib.go": "package lib\n"},
		"many mains": {"go.mod": "module m\n", "cmd/a/main.go": "package main\n", "cmd/b/main.go": "package main\n"},
		"script with unknown binary": {"go.mod": "module m\n", "main.go": "package main\n",
			"Dockerfile": "FROM golang:1.22\nCMD [\"sh\", \"-c\", \"run\"]\n"},
	}
	for name, files := range cases {
		_, err := generate(t, files)
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: want ErrUnsupported, got %v", name, err)
		}
	}
}

func TestGoAlreadyOptimized(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"go.mod":  "module m\n\ngo 1.22\n",
		"go.sum":  "",
		"main.go": "package main\nfunc main(){}\n",
		"Dockerfile": `FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/app .
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
`,
	})
	if len(res.RulesApplied) != 0 {
		t.Errorf("nothing should be new: %v", res.RulesApplied)
	}
	if len(res.AlreadyPresent) < 5 {
		t.Errorf("present: %v", res.AlreadyPresent)
	}
	contains(t, res, "COPY --from=build /out/app /app", `ENTRYPOINT ["/app"]`)
}

// --- Python ------------------------------------------------------------------------

func TestPythonNaiveDockerfile(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"requirements.txt": "django\ngunicorn\npsycopg2\n-r extra.txt\n",
		"extra.txt":        "requests\n",
		"Dockerfile": `FROM python:3.11
RUN apt-get update && apt-get install -y gcc libpq-dev
WORKDIR /code
COPY . .
RUN pip install -r requirements.txt
EXPOSE 8000
CMD ["gunicorn", "proj.wsgi"]
`,
	})
	contains(t, res,
		"FROM python:3.11-slim AS build",
		"apt-get install -y --no-install-recommends gcc libpq-dev",
		"python -m venv /opt/venv",
		"COPY requirements.txt extra.txt ./",
		"RUN pip install -r requirements.txt",
		"FROM python:3.11-slim\n",
		"apt-get install -y --no-install-recommends libpq-dev libpq5",
		"COPY --from=build /opt/venv /opt/venv",
		"COPY --chown=app:app . .",
		"WORKDIR /code",
		"USER app",
		`CMD ["gunicorn", "proj.wsgi"]`,
	)
	if !hasRule(res, RuleDropBuildTools) || !hasRule(res, RuleSlimBase) {
		t.Errorf("rules %v", res.RulesApplied)
	}
	shrinks(t, res)
}

func TestPythonStartInference(t *testing.T) {
	cases := []struct {
		files map[string]string
		want  string
	}{
		{map[string]string{"requirements.txt": "fastapi\nuvicorn\n", "main.py": "app = FastAPI()\n"},
			`CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]`},
		{map[string]string{"requirements.txt": "flask\ngunicorn\n", "app.py": "server = Flask(__name__)\n"},
			`CMD ["gunicorn", "--bind", "0.0.0.0:8000", "app:server"]`},
		{map[string]string{"requirements.txt": "flask\n", "app.py": "app = Flask(__name__)\n"},
			`CMD ["python", "-m", "flask", "--app", "app", "run", "--host", "0.0.0.0", "--port", "8000"]`},
		{map[string]string{"requirements.txt": "click\n", "run.py": "if __name__ == '__main__':\n    main()\n"},
			`CMD ["python", "run.py"]`},
		{map[string]string{"requirements.txt": "django\ngunicorn\n", "manage.py": "", "site/wsgi.py": "", "site/settings.py": ""},
			`CMD ["gunicorn", "--bind", "0.0.0.0:8000", "site.wsgi:application"]`},
		{map[string]string{"requirements.txt": "django\n", "manage.py": ""},
			`CMD ["python", "manage.py", "runserver", "0.0.0.0:8000"]`},
	}
	for _, c := range cases {
		res := mustGenerate(t, c.files)
		contains(t, res, c.want)
	}
	if _, err := generate(t, map[string]string{"requirements.txt": "x\n", "lib.py": ""}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestPythonInstallModes(t *testing.T) {
	res := mustGenerate(t, map[string]string{"requirements.txt": "-e .\n", "app.py": "if __name__ == '__main__': pass\n"})
	contains(t, res, "COPY . .\nRUN pip install -r requirements.txt")
	if hasRule(res, RuleLayerCaching) {
		t.Error("local references defeat layer caching")
	}

	res = mustGenerate(t, map[string]string{"pyproject.toml": "[project]\nname='x'\ndependencies=['a; python_version < \"3.12\"']\n", "app.py": "if __name__ == '__main__': pass\n"})
	contains(t, res, `RUN ["pip", "install", "a; python_version < \"3.12\""]`)

	res = mustGenerate(t, map[string]string{"pyproject.toml": "[project]\nname='x'\n", "app.py": "if __name__ == '__main__': pass\n"})
	contains(t, res, "RUN pip install .")

	res = mustGenerate(t, map[string]string{
		"pyproject.toml": "[tool.poetry]\nname='x'\n[tool.poetry.dependencies]\npython='^3.10'\nflask='*'\n",
		"poetry.lock":    "",
		"app.py":         "if __name__ == '__main__': pass\n",
	})
	contains(t, res, "python:3.10-slim", "COPY pyproject.toml poetry.lock ./", "poetry install --only main --no-root")
	if !hasRule(res, RuleProdOnlyDeps) {
		t.Error(res.RulesApplied)
	}
}

func TestPythonAlpineStaysAlpine(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"requirements.txt": "flask\n",
		"Dockerfile":       "FROM python:3.12-alpine\nRUN apk add --no-cache gcc musl-dev libffi\nCOPY . .\nRUN pip install --no-cache-dir -r requirements.txt\nCMD [\"python\",\"app.py\"]\n",
	})
	contains(t, res, "FROM python:3.12-alpine AS build", "apk add --no-cache gcc musl-dev libffi", "apk add --no-cache libffi", "adduser -D -u 10001 app")
	if hasRule(res, RuleAlpineBase) {
		t.Error("alpine was already there")
	}
}

func TestPythonRefusesAlpinePackagesOnDebian(t *testing.T) {
	_, err := generate(t, map[string]string{
		"requirements.txt": "flask\n",
		"Dockerfile":       "FROM python:3.12\nRUN apk add libfoo\nCMD [\"python\",\"app.py\"]\n",
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
}

// --- engine ------------------------------------------------------------------------

func TestGenerateDispatch(t *testing.T) {
	if _, err := Generate(nil); err == nil {
		t.Fatal("nil context")
	}
	_, err := Generate(&Context{Detection: &detector.Result{Stack: "cobol"}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestEveryRuleDescribed(t *testing.T) {
	for _, r := range order {
		if Descriptions[r] == "" {
			t.Errorf("rule %s has no description", r)
		}
	}
	if len(order) != len(Descriptions) {
		t.Errorf("order has %d rules, descriptions %d", len(order), len(Descriptions))
	}
}

func TestOutputIsDeterministic(t *testing.T) {
	files := map[string]string{
		"requirements.txt": "psycopg2\nmysqlclient\n",
		"app.py":           "if __name__ == '__main__': pass\n",
		"Dockerfile":       "FROM python:3.12\nCOPY --chown=a:b --link x /app/x\nCMD [\"python\",\"app.py\"]\n",
	}
	first := string(mustGenerate(t, files).Dockerfile)
	for i := 0; i < 20; i++ {
		if got := string(mustGenerate(t, files).Dockerfile); got != first {
			t.Fatal("same input produced different output")
		}
	}
}

func TestHelpers(t *testing.T) {
	if !lowPort([]string{"8080", "443"}) || lowPort([]string{"8080", "x"}) {
		t.Error("lowPort")
	}
	for u, root := range map[string]bool{"": true, "root": true, "0": true, "0:0": true, "app": false, "1000:1000": false} {
		if userIsRoot(u) != root {
			t.Errorf("userIsRoot(%q)", u)
		}
	}
	if got := resolveDir("/app", "sub/../x"); got != "/app/x" {
		t.Error(got)
	}
	if quoteEnv("a b") != `"a b"` || quoteEnv("plain") != "plain" || quoteEnv(`"pre"`) != `"pre"` {
		t.Error("quoteEnv")
	}
	if got := goRunProgramArgs([]string{"-race", "./cmd/x", "--flag", "v"}); strings.Join(got, " ") != "--flag v" {
		t.Error(got)
	}
	if goBuildPackage("go build -o x") != "" || goBuildPackage("go build main.go") != "." || goBuildPackage("go build ./cmd/x/...") != "./cmd/x" {
		t.Error("goBuildPackage")
	}
	if binaryName("example.com/a/b", ".") != "b" || binaryName("m", "./cmd/my-tool") != "my-tool" {
		t.Error("binaryName")
	}
	if !versionLess("1.21", "1.22") || versionLess("1.22", "1.22") || !versionLess("", "1.0") {
		t.Error("versionLess")
	}
}

// --- Go: existing lean runtime is kept ---------------------------------------------

const leanRuntimeDockerfile = `FROM golang:1.24.2 AS builder
ARG GIT_TOKEN
WORKDIR /workspace
COPY ./ .
RUN git config --global url."https://${GIT_TOKEN}:x-oauth-basic@github.com/acme".insteadOf "https://github.com/acme"
RUN go env -w GOPRIVATE=github.com/acme/*
RUN go mod download
RUN go generate ./...
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o /workspace/storage-plugin main.go

FROM debian:bullseye-20250428-slim
RUN apt-get update && apt-get install -y --no-install-recommends sshpass=1.09-1+b1 bash=5.1-2+deb11u1 && rm -rf /var/lib/apt/lists/*
WORKDIR /
COPY --from=builder /workspace/storage-plugin .
COPY --from=builder /workspace/assets /opt/assets
COPY default.yml /opt/config.yml
RUN useradd -r storage
USER storage
ENTRYPOINT ["/storage-plugin"]
`

func TestGoKeepsLeanRuntimeStage(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"go.mod":           "module github.com/acme/storage\n\ngo 1.24\n\nrequire github.com/acme/private v1.0.0\n",
		"go.sum":           "",
		"main.go":          "package main\nfunc main(){}\n",
		"default.yml":      "",
		"build/Dockerfile": leanRuntimeDockerfile,
	})
	contains(t, res,
		// Build stage: same pinned builder, private access before download.
		"FROM golang:1.24.2 AS build\nARG GIT_TOKEN\n",
		"RUN git config --global url.",
		"RUN go env -w GOPRIVATE=github.com/acme/*\n\n# Module files first",
		"COPY . .\nRUN go generate ./...",
		"-o /out/storage-plugin .",
		// Runtime stage: replayed verbatim, same release and pins.
		"FROM debian:bullseye-20250428-slim\nRUN apt-get update && apt-get install -y --no-install-recommends sshpass=1.09-1+b1",
		"WORKDIR /\nCOPY --from=build /out/storage-plugin .\nCOPY --from=build /src/assets /opt/assets\nCOPY default.yml /opt/config.yml",
		"USER storage\nENTRYPOINT [\"/storage-plugin\"]",
	)
	lacks(t, res, "bookworm", "distroless", "GOARCH")
	// Git credentials never reach the runtime stage.
	rt := string(res.Dockerfile)[strings.Index(string(res.Dockerfile), "FROM debian"):]
	if strings.Contains(rt, "git config") || strings.Contains(rt, "GIT_TOKEN") {
		t.Fatalf("module access leaked into the runtime:\n%s", rt)
	}
	for _, r := range []string{RuleStaticBinary, RuleLayerCaching} {
		if !hasRule(res, r) {
			t.Errorf("missing %s: %v", r, res.RulesApplied)
		}
	}
	for _, r := range []string{RuleMultiStage, RuleSlimBase, RuleNonRootUser} {
		if hasRule(res, r) {
			t.Errorf("%s claimed as new", r)
		}
	}
	if !hasNote(res, "kept exactly as written") || !hasNote(res, "GIT_TOKEN") {
		t.Errorf("notes: %v", res.Notes)
	}
	e := analyzer.Compare(res.Before, res.After)
	if e.SavedPercent > 20 {
		t.Errorf("a kept runtime cannot save %v%%", e.SavedPercent)
	}
}

func TestGoKeepRuntimeRefusesWhenBinaryNotFound(t *testing.T) {
	_, err := generate(t, map[string]string{
		"go.mod":  "module m\n\ngo 1.24\n",
		"main.go": "package main\nfunc main(){}\n",
		"Dockerfile": "FROM golang:1.24 AS b\nRUN go build -o /out/app .\nFROM alpine:3.20\n" +
			"COPY --from=b /etc/passwd /etc/passwd\nENTRYPOINT [\"/app\"]\n",
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestGoPrivateSetupMovesToBuildStageFromSingleStage(t *testing.T) {
	res := mustGenerate(t, map[string]string{
		"go.mod":  "module m\n\ngo 1.24\n\nrequire github.com/acme/x v1.0.0\n",
		"go.sum":  "",
		"main.go": "package main\nfunc main(){}\n",
		"Dockerfile": "FROM golang:1.24\nARG GITHUB_TOKEN\nWORKDIR /app\nCOPY . .\n" +
			"RUN git config --global url.\"https://${GITHUB_TOKEN}@github.com/\".insteadOf \"https://github.com/\"\n" +
			"RUN go build -o server .\nCMD [\"./server\"]\n",
	})
	out := string(res.Dockerfile)
	rtStart := strings.LastIndex(out, "\nFROM ")
	if !strings.Contains(out[:rtStart], "RUN git config") || !strings.Contains(out[:rtStart], "ARG GITHUB_TOKEN") {
		t.Fatalf("setup not in the build stage:\n%s", out)
	}
	if strings.Contains(out[rtStart:], "git config") {
		t.Fatalf("setup leaked into the runtime:\n%s", out)
	}
	contains(t, res, "FROM golang:1.24 AS build", "gcr.io/distroless/static-debian12:nonroot")
}
