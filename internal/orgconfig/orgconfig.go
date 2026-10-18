// Package orgconfig reads .doiq.yml: company-specific knowledge the CLI cannot
// infer — internal base images, registry mirrors, package-registry mirrors.
//
// Optional by design. Phase 0.7 requires zero configuration for the default
// path, so with no file everything behaves exactly as before. Lookup order:
//
//  1. -config <file>
//  2. <repo>/.doiq.yml
//  3. the organisation default: <user config dir>/doiq/org.yml
//
// A repo file and the organisation default are merged; the repo wins on
// conflicts, so a team can override one image without copying the rest.
package orgconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// FileName is the per-repository config file.
const FileName = ".doiq.yml"

// Config is the whole file.
type Config struct {
	// BaseImages declares internal images so doiq can reason about them
	// instead of refusing (Phase 1.4 refuses images it cannot classify).
	BaseImages []BaseImage `yaml:"baseImages"`

	// Mirrors rewrite the registry of every public image doiq generates.
	// {"docker.io": "registry.acme.com/dockerhub"} turns node:22-alpine into
	// registry.acme.com/dockerhub/library/node:22-alpine (or without the
	// library/ segment, see MirrorKeepLibrary); {"gcr.io": "..."} covers
	// distroless.
	Mirrors map[string]string `yaml:"mirrors"`

	// MirrorKeepLibrary keeps Docker Hub's implicit "library/" path when
	// mirroring official images. Artifactory remote repos usually want it;
	// Harbor proxy caches do too. Default true.
	MirrorKeepLibrary *bool `yaml:"mirrorKeepLibrary"`

	// Packages points package managers at internal registries during the
	// build stage.
	Packages PackageMirrors `yaml:"packages"`
}

// BaseImage declares one internal image (or a family of tags).
type BaseImage struct {
	// Match is a glob on the full image reference, e.g.
	// "registry.acme.com/base/node:*".
	Match string `yaml:"match"`

	// Family is what the image is: node, python, golang, java, debian,
	// ubuntu, alpine, rhel or distroless.
	Family string `yaml:"family"`

	// Variant for language images: full, slim or alpine. Decides which
	// package manager generated steps use.
	Variant string `yaml:"variant"`

	// Version is the language version the image carries ("20", "3.12").
	// Needed when the tag does not say it.
	Version string `yaml:"version"`

	// SizeMB is the image's uncompressed size, for the estimate.
	SizeMB float64 `yaml:"sizeMB"`
}

// PackageMirrors are internal package registries.
type PackageMirrors struct {
	NPM     string `yaml:"npm"`     // NPM_CONFIG_REGISTRY
	PyPI    string `yaml:"pypi"`    // PIP_INDEX_URL
	GoProxy string `yaml:"goproxy"` // GOPROXY
	Maven   string `yaml:"maven"`   // a mirror URL for Maven Central
}

var families = map[string]bool{
	"node": true, "python": true, "golang": true, "java": true, "debian": true,
	"ubuntu": true, "alpine": true, "rhel": true, "distroless": true,
}

// Load resolves and merges configuration for the repository at root.
// explicit is the -config flag ("" when not given).
func Load(root, explicit string) (*Config, []string, error) {
	var sources []string
	merged := &Config{}

	if org, err := orgDefaultPath(); err == nil {
		c, err := readFile(org)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
		if c != nil {
			merged.merge(c)
			sources = append(sources, org)
		}
	}

	repoFile := filepath.Join(root, FileName)
	if explicit != "" {
		repoFile = explicit
	}
	c, err := readFile(repoFile)
	switch {
	case errors.Is(err, fs.ErrNotExist) && explicit == "":
	case err != nil:
		return nil, nil, err
	default:
		merged.merge(c)
		sources = append(sources, repoFile)
	}
	if err := merged.Validate(); err != nil {
		return nil, nil, err
	}
	return merged, sources, nil
}

func orgDefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, constants.AppName, "org.yml"), nil
}

func readFile(p string) (*Config, error) {
	b, err := os.ReadFile(p) //nolint:gosec // config path chosen by the user
	if err != nil {
		return nil, err
	}
	c := &Config{}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true) // a typo is an error, not a silently ignored key
	if err := dec.Decode(c); err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return c, nil
}

// merge overlays o: its base images take precedence (they are checked first),
// its mirrors and package registries replace ours key by key.
func (c *Config) merge(o *Config) {
	c.BaseImages = append(append([]BaseImage{}, o.BaseImages...), c.BaseImages...)
	if len(o.Mirrors) > 0 && c.Mirrors == nil {
		c.Mirrors = map[string]string{}
	}
	for k, v := range o.Mirrors {
		c.Mirrors[k] = v
	}
	if o.MirrorKeepLibrary != nil {
		c.MirrorKeepLibrary = o.MirrorKeepLibrary
	}
	if o.Packages.NPM != "" {
		c.Packages.NPM = o.Packages.NPM
	}
	if o.Packages.PyPI != "" {
		c.Packages.PyPI = o.Packages.PyPI
	}
	if o.Packages.GoProxy != "" {
		c.Packages.GoProxy = o.Packages.GoProxy
	}
	if o.Packages.Maven != "" {
		c.Packages.Maven = o.Packages.Maven
	}
}

// Validate reports every problem at once.
func (c *Config) Validate() error {
	var problems []string
	for i, b := range c.BaseImages {
		if b.Match == "" {
			problems = append(problems, fmt.Sprintf("baseImages[%d]: match is required", i))
		} else if _, err := path.Match(b.Match, ""); err != nil {
			problems = append(problems, fmt.Sprintf("baseImages[%d]: match %q is not a valid glob", i, b.Match))
		}
		if !families[b.Family] {
			problems = append(problems, fmt.Sprintf("baseImages[%d]: family must be one of node, python, golang, java, debian, ubuntu, alpine, rhel, distroless", i))
		}
		switch b.Variant {
		case "", "full", "slim", "alpine":
		default:
			problems = append(problems, fmt.Sprintf("baseImages[%d]: variant must be full, slim or alpine", i))
		}
	}
	for host, m := range c.Mirrors {
		if strings.Contains(m, "://") {
			problems = append(problems, fmt.Sprintf("mirrors[%s]: give a registry path, not a URL (no scheme)", host))
		}
	}
	for name, u := range map[string]string{"npm": c.Packages.NPM, "pypi": c.Packages.PyPI, "maven": c.Packages.Maven} {
		if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			problems = append(problems, "packages."+name+" must be an http(s) URL")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid %s:\n  - %s", FileName, strings.Join(problems, "\n  - "))
	}
	return nil
}

// KeepLibrary reports the effective MirrorKeepLibrary.
func (c *Config) KeepLibrary() bool { return c.MirrorKeepLibrary == nil || *c.MirrorKeepLibrary }
