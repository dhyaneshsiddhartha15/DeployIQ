package orgconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func isolateOrgDefault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS
	p, err := orgDefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoConfigIsFine(t *testing.T) {
	isolateOrgDefault(t)
	c, sources, err := Load(t.TempDir(), "")
	if err != nil || len(sources) != 0 || len(c.BaseImages) != 0 || !c.KeepLibrary() {
		t.Fatalf("%+v %v %v", c, sources, err)
	}
}

func TestRepoOverridesOrgDefault(t *testing.T) {
	org := isolateOrgDefault(t)
	write(t, org, `baseImages:
  - {match: "acme/node:*", family: node, sizeMB: 300}
mirrors: {docker.io: org-mirror, gcr.io: org-gcr}
packages: {npm: "https://org-npm", pypi: "https://org-pypi"}
`)
	repo := t.TempDir()
	write(t, filepath.Join(repo, FileName), `baseImages:
  - {match: "acme/node:*", family: node, variant: alpine}
mirrors: {docker.io: team-mirror}
mirrorKeepLibrary: false
packages: {npm: "https://team-npm"}
`)
	c, sources, err := Load(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources %v", sources)
	}
	if c.BaseImages[0].Variant != "alpine" || len(c.BaseImages) != 2 {
		t.Fatalf("repo image must be checked first: %+v", c.BaseImages)
	}
	if c.Mirrors["docker.io"] != "team-mirror" || c.Mirrors["gcr.io"] != "org-gcr" || c.KeepLibrary() {
		t.Fatalf("mirrors %+v", c.Mirrors)
	}
	if c.Packages.NPM != "https://team-npm" || c.Packages.PyPI != "https://org-pypi" {
		t.Fatalf("packages %+v", c.Packages)
	}
}

func TestValidation(t *testing.T) {
	isolateOrgDefault(t)
	repo := t.TempDir()
	write(t, filepath.Join(repo, FileName), `baseImages:
  - {match: "", family: cobol, variant: huge}
  - {match: "[", family: node}
mirrors: {docker.io: "https://x"}
packages: {npm: "ftp://x"}
`)
	_, _, err := Load(repo, "")
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"match is required", "family must be", "variant must be", "not a valid glob", "not a URL", "packages.npm"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	write(t, filepath.Join(repo, FileName), "baseImage: []\n") // typo
	if _, _, err := Load(repo, ""); err == nil {
		t.Fatal("unknown key accepted")
	}
	if _, _, err := Load(repo, filepath.Join(repo, "missing.yml")); err == nil {
		t.Fatal("explicit missing file accepted")
	}
}
