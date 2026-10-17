package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/config"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

type result struct {
	code           int
	stdout, stderr string
}

func runCLI(t *testing.T, stdin string, interactive bool, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, env{stdin: strings.NewReader(stdin), stdout: &out, stderr: &errb, interactive: interactive})
	return result{code, out.String(), errb.String()}
}

func nodeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node index.js"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM node:20\nCOPY . .\nRUN npm install\nCMD [\"node\",\"index.js\"]\n"), 0o644)
	return dir
}

// snapshot records every file's content so US-2 can be checked exactly.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			m[p] = string(b)
		}
		return nil
	})
	return m
}

func TestDryRunNeverWrites(t *testing.T) {
	// US-2: without -write, no file on disk is modified, regardless of output.
	dir := nodeRepo(t)
	before := snapshot(t, dir)
	for _, args := range [][]string{{dir}, {"-dry-run", dir}, {"-format", "json", dir}, {"-diff", dir}, {"-yes", dir}} {
		r := runCLI(t, "y\n", true, args...)
		if r.code != constants.ExitOK {
			t.Fatalf("%v: exit %d: %s", args, r.code, r.stderr)
		}
		after := snapshot(t, dir)
		if len(after) != len(before) {
			t.Fatalf("%v: files created: %v", args, after)
		}
		for k, v := range before {
			if after[k] != v {
				t.Fatalf("%v: %s modified", args, k)
			}
		}
	}
}

func TestUS1Output(t *testing.T) {
	r := runCLI(t, "", false, nodeRepo(t))
	for _, want := range []string{"Detected stack:  node", "Estimated reduction", "FROM node:20-alpine AS build", "Dry run: nothing was written"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q", want)
		}
	}
	if r.stderr != "" {
		t.Errorf("stderr should be empty on success: %q", r.stderr)
	}
}

func TestUS3UnsupportedStack(t *testing.T) {
	r := runCLI(t, "", false, t.TempDir())
	if r.code != constants.ExitStackUnknown {
		t.Fatalf("exit %d", r.code)
	}
	if !strings.Contains(r.stderr, "Could not detect a supported stack in this repo") {
		t.Fatalf("stderr: %q", r.stderr)
	}
}

func TestUnsupportedDockerfile(t *testing.T) {
	dir := nodeRepo(t)
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM mycorp/base:1\nCMD [\"node\"]\n"), 0o644)
	r := runCLI(t, "", false, dir)
	if r.code != constants.ExitUnsupported || !strings.Contains(r.stderr, "refuses to guess") {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, constants.DockerfileOutputName)); err == nil {
		t.Fatal("wrote output for an unsupported repo")
	}
}

func TestPathErrors(t *testing.T) {
	if r := runCLI(t, "", false, filepath.Join(t.TempDir(), "missing")); r.code != constants.ExitFailure || !strings.Contains(r.stderr, "missing") {
		t.Fatalf("missing path: %d %s", r.code, r.stderr)
	}
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o644)
	if r := runCLI(t, "", false, f); r.code != constants.ExitFailure || !strings.Contains(r.stderr, "not a directory") {
		t.Fatalf("file path: %d %s", r.code, r.stderr)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"a", "b"}, {"-write", "-dry-run"}, {"-format", "xml"}, {"-nope"}} {
		if r := runCLI(t, "", false, args...); r.code != constants.ExitUsage {
			t.Errorf("%v: exit %d", args, r.code)
		}
	}
	if r := runCLI(t, "", false, "-h"); r.code != constants.ExitOK || !strings.Contains(r.stderr, "Exit codes") {
		t.Errorf("-h: %d", r.code)
	}
	if r := runCLI(t, "", false, "-version"); r.code != constants.ExitOK || !strings.HasPrefix(r.stdout, "doiq ") {
		t.Errorf("-version: %q", r.stdout)
	}
}

func TestWriteRequiresConfirmation(t *testing.T) {
	dir := nodeRepo(t)
	target := filepath.Join(dir, constants.DockerfileOutputName)

	// Non-interactive without -yes: refuse, exit cleanly, write nothing.
	r := runCLI(t, "", false, "-write", dir)
	if _, err := os.Stat(target); err == nil {
		t.Fatal("wrote without confirmation")
	}
	if !strings.Contains(r.stderr, "pass -yes") {
		t.Fatalf("stderr: %s", r.stderr)
	}

	// Interactive, answered no.
	runCLI(t, "n\n", true, "-write", dir)
	if _, err := os.Stat(target); err == nil {
		t.Fatal("wrote after 'n'")
	}

	// Interactive, answered yes.
	r = runCLI(t, "yes\n", true, "-write", "-quiet", dir)
	b, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(b), "FROM node:20-alpine") {
		t.Fatalf("not written: %v %s", err, r.stderr)
	}
	// The original Dockerfile is never touched.
	if orig, _ := os.ReadFile(filepath.Join(dir, "Dockerfile")); !strings.HasPrefix(string(orig), "FROM node:20\n") {
		t.Fatal("existing Dockerfile modified")
	}

	// Overwrite prompts differently and respects the answer.
	os.WriteFile(target, []byte("mine"), 0o644)
	r = runCLI(t, "\n", true, "-write", dir)
	if got, _ := os.ReadFile(target); string(got) != "mine" || !strings.Contains(r.stderr, "Overwrite existing") {
		t.Fatalf("overwrote on empty answer: %q", got)
	}
	runCLI(t, "", false, "-write", "-yes", dir)
	if got, _ := os.ReadFile(target); string(got) == "mine" {
		t.Fatal("-yes did not overwrite")
	}
	// No temp files left behind.
	matches, _ := filepath.Glob(filepath.Join(dir, ".doiq-*"))
	if len(matches) != 0 {
		t.Fatalf("temp files left: %v", matches)
	}
}

func TestJSONOutput(t *testing.T) {
	r := runCLI(t, "", false, "-format", "json", nodeRepo(t))
	var rep struct {
		Stack        string   `json:"stack"`
		RulesApplied []string `json:"rulesApplied"`
		Dockerfile   string   `json:"dockerfile"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, r.stdout)
	}
	if rep.Stack != "node" || len(rep.RulesApplied) == 0 || rep.Dockerfile == "" {
		t.Fatalf("%+v", rep)
	}
}

func TestReportRequiresLogin(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yml")
	cliConfigPath = func() (string, error) { return cfgPath, nil }
	t.Cleanup(func() { cliConfigPath = config.CLIConfigPath })

	r := runCLI(t, "", false, "-report", "-quiet", nodeRepo(t))
	if r.code != constants.ExitFailure || !strings.Contains(r.stderr, "doiq login") {
		t.Fatalf("%d %s", r.code, r.stderr)
	}
	if r := runCLI(t, "", false, "whoami"); r.code != constants.ExitFailure {
		t.Fatal("whoami without login")
	}

	// logout on a fresh machine is harmless and leaves a 0600 file.
	if r := runCLI(t, "", false, "logout"); r.code != constants.ExitOK {
		t.Fatalf("logout: %s", r.stderr)
	}
}

func TestRepoName(t *testing.T) {
	dir := t.TempDir()
	if got := repoName(dir); got != filepath.Base(dir) {
		t.Fatal(got)
	}
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	for url, want := range map[string]string{
		"git@github.com:acme/api.git":         "acme/api",
		"https://github.com/acme/web":         "acme/web",
		"https://gitlab.com/group/proj.git/":  "group/proj",
		"ssh://git@git.corp.example/team/svc": "team/svc",
	} {
		os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[core]\n\tbare = false\n[remote \"upstream\"]\n\turl = x\n[remote \"origin\"]\n\turl = "+url+"\n"), 0o644)
		if got := repoName(dir); got != want {
			t.Errorf("%s: %s", url, got)
		}
	}
}

func TestResolveEndpoint(t *testing.T) {
	t.Setenv("DOIQ_API_URL", "")
	if got := resolveEndpoint("", &config.CLIConfig{}); got != constants.DefaultAPIEndpoint {
		t.Fatal(got)
	}
	if got := resolveEndpoint("", &config.CLIConfig{APIEndpoint: "https://saved"}); got != "https://saved" {
		t.Fatal(got)
	}
	t.Setenv("DOIQ_API_URL", "https://env")
	if got := resolveEndpoint("", &config.CLIConfig{APIEndpoint: "https://saved"}); got != "https://env" {
		t.Fatal(got)
	}
	if got := resolveEndpoint("https://flag", nil); got != "https://flag" {
		t.Fatal(got)
	}
}
