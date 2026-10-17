package dockerfile

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Dockerfile {
	t.Helper()
	df, err := ParseBytes([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return df
}

func TestParseMissingFileIsNotAnError(t *testing.T) {
	df, err := Parse(t.TempDir())
	if err != nil || df != nil {
		t.Fatalf("want (nil, nil), got (%v, %v)", df, err)
	}
}

func TestParseFromDisk(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM node:20\nCMD [\"node\",\"a.js\"]\n"), 0o644)
	df, err := Parse(dir)
	if err != nil {
		t.Fatal(err)
	}
	if df.Path == "" || df.RuntimeImage().Repository != "node" {
		t.Fatalf("%+v", df)
	}

	// A directory named Dockerfile is refused, not read.
	dir2 := t.TempDir()
	os.Mkdir(filepath.Join(dir2, "Dockerfile"), 0o755)
	if _, err := Parse(dir2); err == nil {
		t.Fatal("want error")
	}
}

func TestParseBasics(t *testing.T) {
	df := mustParse(t, string(rune(0xFEFF))+"# syntax=docker/dockerfile:1\r\n"+`
# comment
ARG BASE=python:3.12
FROM ${BASE} AS build
RUN apt-get update \
    # a comment inside a continuation
    && apt-get install -y gcc
WORKDIR /src
COPY --chown=app:app --from=x a b
ENV A=1 B="two words"
ENV LEGACY value with spaces
EXPOSE 8000/tcp 9000 $PORT
CMD ["python", "app.py"]
`)
	if len(df.Stages) != 1 || df.Stages[0].Name != "build" {
		t.Fatalf("stages = %+v", df.Stages)
	}
	st := df.Final()
	if st.Image.Repository != "python" || st.Image.Tag != "3.12" {
		t.Fatalf("image = %+v", st.Image)
	}
	run := st.All("RUN")[0]
	if !strings.Contains(run.Args, "apt-get install -y gcc") || strings.Contains(run.Args, "comment") {
		t.Fatalf("continuation: %q", run.Args)
	}
	cp := st.All("COPY")[0]
	if cp.Flags["chown"] != "app:app" || cp.Flags["from"] != "x" || cp.Args != "a b" {
		t.Fatalf("flags: %+v", cp)
	}
	env := st.Env()
	want := [][2]string{{"A", "1"}, {"B", "two words"}, {"LEGACY", "value with spaces"}}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env = %v", env)
	}
	if p := st.Ports(); !reflect.DeepEqual(p, []string{"8000", "9000"}) {
		t.Fatalf("ports = %v", p)
	}
	cmd := st.Last("CMD")
	if !cmd.IsExec() || !reflect.DeepEqual(cmd.Exec, []string{"python", "app.py"}) {
		t.Fatalf("cmd = %+v", cmd)
	}
	if st.Last("USER") != nil {
		t.Fatal("no USER expected")
	}
}

func TestParseEscapeDirective(t *testing.T) {
	df := mustParse(t, "# escape=`\nFROM mcr.microsoft.com/windows:ltsc AS a\nRUN echo a `\n  b\n")
	if got := df.Final().All("RUN")[0].Args; got != "echo a b" {
		t.Fatalf("got %q", got)
	}
}

func TestParseMultiStage(t *testing.T) {
	df := mustParse(t, `
FROM golang:1.22 AS build
RUN go build -o /out/app .
FROM build AS test
RUN go test ./...
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/app /app
`)
	if !df.MultiStage() || len(df.Stages) != 3 {
		t.Fatal("want 3 stages")
	}
	if df.Stages[1].FromStage != "build" {
		t.Fatalf("FROM <stage> not resolved: %+v", df.Stages[1])
	}
	if df.RuntimeImage().Family() != FamilyDistroless {
		t.Fatalf("runtime = %+v", df.RuntimeImage())
	}
	if len(df.All("RUN")) != 2 {
		t.Fatal("All(RUN)")
	}
}

func TestRuntimeImageFollowsStageChain(t *testing.T) {
	df := mustParse(t, "FROM node:20-alpine AS base\nFROM base AS deps\nFROM deps\n")
	if got := df.RuntimeImage(); got.Repository != "node" || got.Tag != "20-alpine" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseUnsupported(t *testing.T) {
	cases := map[string]string{
		"heredoc":        "FROM alpine\nRUN <<EOF\necho hi\nEOF\n",
		"onbuild":        "FROM alpine\nONBUILD RUN echo\n",
		"unknown":        "FROM alpine\nFROBNICATE x\n",
		"no from":        "# only a comment\n",
		"before from":    "RUN echo\nFROM alpine\n",
		"unresolved arg": "ARG IMAGE\nFROM $IMAGE\n",
		"bad escape":     "# escape=x\nFROM alpine\n",
		"empty from":     "FROM\n",
	}
	for name, src := range cases {
		_, err := ParseBytes([]byte(src))
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: want ErrUnsupported, got %v", name, err)
		}
	}
}

func TestExpand(t *testing.T) {
	df := &Dockerfile{GlobalArgs: map[string]string{"V": "3.12", "EMPTY": ""}}
	cases := map[string]string{
		"python:${V}":         "python:3.12",
		"python:$V-slim":      "python:3.12-slim",
		"node:${MISSING:-20}": "node:20",
		"node:${MISSING-21}":  "node:21",
		"x${V:+-alt}":         "x-alt",
		"x${EMPTY:+-alt}":     "x",
		"node:${EMPTY:-22}":   "node:22",
	}
	for in, want := range cases {
		got, err := df.expand(in, 1)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestParseImage(t *testing.T) {
	cases := []struct {
		ref                              string
		registry, repo, tag, digest, fam string
	}{
		{"node", "", "node", "latest", "", FamilyNode},
		{"node:20-alpine", "", "node", "20-alpine", "", FamilyNode},
		{"library/python:3.12-slim", "", "python", "3.12-slim", "", FamilyPython},
		{"docker.io/library/golang:1.22", "", "golang", "1.22", "", FamilyGolang},
		{"gcr.io/distroless/static-debian12:nonroot", "gcr.io", "distroless/static-debian12", "nonroot", "", FamilyDistroless},
		{"localhost:5000/team/base:1", "localhost:5000", "team/base", "1", "", FamilyUnknown},
		{"ubuntu@sha256:abc", "", "ubuntu", "", "sha256:abc", FamilyUbuntu},
		{"mycompany/base:2", "", "mycompany/base", "2", "", FamilyUnknown},
		{"public.ecr.aws/docker/library/node:20", "public.ecr.aws", "docker/library/node", "20", "", FamilyNode},
		{"scratch", "", "scratch", "latest", "", FamilyScratch},
		{"redhat/ubi9-minimal", "", "redhat/ubi9-minimal", "latest", "", FamilyRHEL},
		{"quay.io/x/y", "quay.io", "x/y", "latest", "", FamilyUnknown},
		{"ghcr.io/x/y", "ghcr.io", "x/y", "latest", "", FamilyUnknown},
	}
	for _, c := range cases {
		img := ParseImage(c.ref)
		if img.Registry != c.registry || img.Repository != c.repo || img.Tag != c.tag || img.Digest != c.digest {
			t.Errorf("%s: %+v", c.ref, img)
		}
		if img.Family() != c.fam {
			t.Errorf("%s: family %q want %q", c.ref, img.Family(), c.fam)
		}
	}
}

func TestImageVariantVersionOS(t *testing.T) {
	cases := []struct {
		ref          string
		variant      string
		major, minor int
		os           string
	}{
		{"node:20-alpine3.19", VariantAlpine, 20, -1, "alpine"},
		{"python:3.12-slim-bookworm", VariantSlim, 3, 12, "debian"},
		{"golang:1.22", VariantFull, 1, 22, "debian"},
		{"node:lts", VariantFull, -1, -1, "debian"},
		{"alpine:3.20", VariantFull, 3, 20, "alpine"},
		{"ubuntu:22.04", VariantFull, 22, 4, "debian"},
		{"amazonlinux:2023", VariantFull, 2023, -1, "rhel"},
		{"gcr.io/distroless/base-debian12", VariantFull, -1, -1, ""},
	}
	for _, c := range cases {
		img := ParseImage(c.ref)
		maj, minor := img.Version()
		if img.Variant() != c.variant || maj != c.major || minor != c.minor || img.OSFamily() != c.os {
			t.Errorf("%s: variant=%s version=%d.%d os=%s", c.ref, img.Variant(), maj, minor, img.OSFamily())
		}
	}
	if ParseImage("bitnami/node:20").Name() != "node" {
		t.Error("Name")
	}
}

func TestAnalyze(t *testing.T) {
	df := mustParse(t, `
FROM ubuntu:22.04
ENV PIP_NO_CACHE_DIR=1
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends python3 python3-pip libpq-dev=1.2 $EXTRA && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
RUN apk add --no-cache git
WORKDIR /app
COPY . .
RUN npm install && npm run build
RUN pip install -r requirements.txt
RUN cd scripts && ./setup.sh
RUN echo done
RUN ["sh", "-c", "chmod +x /app/run.sh"]
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /bin/server ./cmd/server
`)
	a := Analyze(df)
	if !reflect.DeepEqual(a.SystemPackages, []string{"python3", "python3-pip", "libpq-dev=1.2", "git"}) {
		t.Fatalf("packages = %v", a.SystemPackages)
	}
	if a.PackageManager != "apt" || !a.AptListsCleaned || !a.PipCacheDisabled {
		t.Fatalf("pm=%s lists=%v pip=%v", a.PackageManager, a.AptListsCleaned, a.PipCacheDisabled)
	}
	if !a.DevDepsInstalled {
		t.Fatal("npm install without --omit=dev installs dev dependencies")
	}
	if a.GoBuildOutput != "/bin/server" || a.GoBuildStage != 0 {
		t.Fatalf("go output = %q", a.GoBuildOutput)
	}
	// setup.sh and chmod are carried, whole instruction each; `echo done` is
	// trivial and the nodesource pipe is repository setup, not app logic.
	if len(a.Custom) != 2 {
		for _, c := range a.Custom {
			t.Logf("custom: %s", c.Instruction.Args)
		}
		t.Fatalf("custom count = %d", len(a.Custom))
	}
	if !strings.Contains(a.Custom[0].Instruction.Args, "cd scripts && ./setup.sh") {
		t.Fatalf("custom[0] = %q", a.Custom[0].Instruction.Args)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		cmd  string
		kind CommandKind
		prod bool
	}{
		{"npm ci --omit=dev", CommandDepsInstall, true},
		{"npm install --production", CommandDepsInstall, true},
		{"yarn install --frozen-lockfile", CommandDepsInstall, false},
		{"yarn", CommandDepsInstall, false},
		{"pnpm install --prod", CommandDepsInstall, true},
		{"npm install -g pm2", CommandCustom, false},
		{"pip install --no-cache-dir -r requirements.txt", CommandDepsInstall, true},
		{"python -m pip install flask", CommandDepsInstall, true},
		{"poetry install", CommandDepsInstall, false},
		{"poetry install --only main", CommandDepsInstall, true},
		{"go mod download", CommandDepsInstall, true},
		{"go build -o app .", CommandBuild, false},
		{"yarn build", CommandBuild, false},
		{"apt-get -y install curl", CommandSystemInstall, false},
		{"yum install -y gcc", CommandSystemInstall, false},
		{"apt-get update", CommandHousekeeping, false},
		{"npm cache clean --force", CommandHousekeeping, false},
		{"pip install --upgrade pip", CommandHousekeeping, false},
		{"./configure", CommandCustom, false},
	}
	for _, c := range cases {
		got := classify(c.cmd)
		if got.Kind != c.kind || (c.kind == CommandDepsInstall && got.ProdOnly != c.prod) {
			t.Errorf("%q: kind=%d prod=%v", c.cmd, got.Kind, got.ProdOnly)
		}
	}
	if pm := packageManagerOf("dnf install x"); pm != "dnf" {
		t.Error(pm)
	}
}

func TestGenerate(t *testing.T) {
	f := &File{Syntax: "docker/dockerfile:1", Header: []string{"top", ""}}
	b := f.AddStage("golang:1.22", "build", "build stage")
	b.Add("WORKDIR", "/src").Note("why\ntwo lines", "RUN", "go build").Exec("CMD", []string{"a&b", "<c>"})
	if !b.Has("RUN") || b.Has("USER") {
		t.Fatal("Has")
	}
	r := f.AddStage("alpine", "final")
	r.Raw("carried", Instruction{Cmd: "COPY", Flags: map[string]string{"from": "build", "chown": "a", "link": ""}, Args: "/x /y"})

	out := string(Generate(f))
	want := `# syntax=docker/dockerfile:1
# top
#

# build stage
FROM golang:1.22 AS build
WORKDIR /src

# why
# two lines
RUN go build
CMD ["a&b", "<c>"]

FROM alpine

# carried
COPY --chown=a --from=build --link /x /y
`
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}

	// The output must itself parse: generated files are valid Dockerfiles.
	if _, err := ParseBytes([]byte(out)); err != nil {
		t.Fatalf("generated output does not parse: %v", err)
	}
}

func TestSplitShellWords(t *testing.T) {
	got := splitShellWords(`A=1 B="x y" C='z'  D=`)
	want := []string{"A=1", "B=x y", "C=z", "D="}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestParseFindsConventionalLocations(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "build"), 0o755)
	os.WriteFile(filepath.Join(dir, "build", "Dockerfile"), []byte("FROM alpine:3.20\n"), 0o644)
	df, err := Parse(dir)
	if err != nil || df == nil || df.RelPath != "build/Dockerfile" {
		t.Fatalf("build/Dockerfile not found: %+v %v", df, err)
	}
	// The root wins when both exist.
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM node:20\n"), 0o644)
	if df, _ := Parse(dir); df.RelPath != "Dockerfile" {
		t.Fatalf("root not preferred: %s", df.RelPath)
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "ops"), 0o755)
	os.WriteFile(filepath.Join(dir, "ops", "api.Dockerfile"), []byte("FROM alpine:3.20\nHEREDOC <<EOF\n"), 0o644)
	if _, err := ParseFile(dir, "ops/api.Dockerfile"); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "ops/api.Dockerfile") {
		t.Fatalf("errors must name the file: %v", err)
	}
	for _, bad := range []string{"../x/Dockerfile", "/etc/passwd", "missing"} {
		if _, err := ParseFile(dir, bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
