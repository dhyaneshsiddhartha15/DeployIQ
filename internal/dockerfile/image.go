package dockerfile

import (
	"path"
	"regexp"
	"strings"
)

// Image is a parsed image reference: [registry/]repository[:tag][@digest].
type Image struct {
	// Ref is the reference as written (after ARG substitution).
	Ref string

	// Registry is the registry host, "" for Docker Hub.
	Registry string

	// Repository is the path without registry, with Docker Hub's implicit
	// "library/" removed: "node", "bitnami/node", "distroless/static-debian12".
	Repository string

	// Tag is the tag, "latest" when omitted and no digest is given.
	Tag string

	// Digest is the @sha256:... part, if any.
	Digest string
}

// ParseImage splits an image reference. It never fails: an odd reference just
// yields an Image whose Family is unknown, and callers refuse it there.
func ParseImage(ref string) Image {
	img := Image{Ref: ref}
	rest := ref
	if at := strings.IndexByte(rest, '@'); at >= 0 {
		img.Digest = rest[at+1:]
		rest = rest[:at]
	}
	// A registry is the first path component when it contains a dot or a
	// colon, or is localhost — the same rule the Docker CLI applies.
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		first := rest[:slash]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			img.Registry = first
			rest = rest[slash+1:]
		}
	}
	if colon := strings.LastIndexByte(rest, ':'); colon >= 0 && !strings.Contains(rest[colon:], "/") {
		img.Tag = rest[colon+1:]
		rest = rest[:colon]
	}
	if img.Tag == "" && img.Digest == "" {
		img.Tag = "latest"
	}
	img.Repository = strings.TrimPrefix(strings.ToLower(rest), "library/")
	if img.Registry == "docker.io" || img.Registry == "index.docker.io" || img.Registry == "registry-1.docker.io" {
		img.Registry = ""
	}
	return img
}

// Name is the last path element of the repository: "node" for "bitnami/node".
func (i Image) Name() string {
	if s := strings.LastIndexByte(i.Repository, '/'); s >= 0 {
		return i.Repository[s+1:]
	}
	return i.Repository
}

// Base image families the rule engine knows how to reason about.
const (
	FamilyNode       = "node"
	FamilyPython     = "python"
	FamilyGolang     = "golang"
	FamilyUbuntu     = "ubuntu"
	FamilyDebian     = "debian"
	FamilyAlpine     = "alpine"
	FamilyDistroless = "distroless"
	FamilyScratch    = "scratch"
	FamilyRHEL       = "rhel" // ubi, centos, fedora, amazonlinux, rockylinux, almalinux
	FamilyBuildpack  = "buildpack-deps"
	FamilyJava       = "java" // eclipse-temurin, openjdk, amazoncorretto, maven, gradle
	FamilyUnknown    = ""
)

// mirrors maps a registry host ("docker.io", "gcr.io") to a mirror prefix.
var (
	mirrors           map[string]string
	mirrorKeepLibrary = true
)

// SetMirrors installs registry mirrors for generated images.
func SetMirrors(m map[string]string, keepLibrary bool) {
	mirrors, mirrorKeepLibrary = m, keepLibrary
}

// Mirror rewrites a public image reference through the configured mirror for
// its registry. References with no mirror, and internal images, are returned
// unchanged.
func Mirror(ref string) string {
	if len(mirrors) == 0 {
		return ref
	}
	img := ParseImage(ref)
	host := img.Registry
	if host == "" {
		host = "docker.io"
	}
	m, ok := mirrors[host]
	if !ok || img.Custom() != nil {
		return ref
	}
	repo := img.Repository
	if host == "docker.io" && !strings.Contains(repo, "/") && mirrorKeepLibrary {
		repo = "library/" + repo
	}
	out := strings.TrimRight(m, "/") + "/" + repo
	if img.Tag != "" {
		out += ":" + img.Tag
	}
	if img.Digest != "" {
		out += "@" + img.Digest
	}
	return out
}

// Variants of an official language image.
const (
	VariantFull   = "full"
	VariantSlim   = "slim"
	VariantAlpine = "alpine"
)

// CustomImage is an internal image declared in .doiq.yml, so it can be
// classified instead of refused.
type CustomImage struct {
	Match   string // glob on the full reference
	Family  string
	Variant string
	Version string
	SizeMB  float64
}

// customImages is set once per run from configuration. Process-wide state is
// acceptable here: the CLI analyses one configuration per process, and the
// alternative is threading it through every Family() call.
var customImages []CustomImage

// SetCustomImages installs the declared internal images; nil clears them.
func SetCustomImages(c []CustomImage) { customImages = c }

// Custom returns the declaration matching this image, or nil.
func (i Image) Custom() *CustomImage {
	for k := range customImages {
		c := &customImages[k]
		if ok, _ := path.Match(c.Match, i.Ref); ok {
			return c
		}
		// Also match the reference with an implicit tag spelled out.
		if ok, _ := path.Match(c.Match, i.Ref+":"+i.Tag); ok && !strings.Contains(i.Ref, ":") {
			return c
		}
	}
	return nil
}

// Family classifies the image. Official images, well-known vendor
// repositories and images declared in .doiq.yml are recognised; anything else
// could contain anything, and doiq refuses to guess what (Phase 1.4).
func (i Image) Family() string {
	if c := i.Custom(); c != nil {
		return c.Family
	}
	if i.Repository == "scratch" {
		return FamilyScratch
	}
	switch i.Registry {
	case "gcr.io", "cgr.dev":
		if strings.HasPrefix(i.Repository, "distroless/") || strings.HasPrefix(i.Repository, "chainguard/static") {
			return FamilyDistroless
		}
		return FamilyUnknown
	case "public.ecr.aws", "mcr.microsoft.com", "registry.access.redhat.com", "quay.io":
		// Mirrors of official images keep the official semantics.
	case "":
	default:
		return FamilyUnknown
	}

	repo := i.Repository
	// Docker Hub vendor namespaces that republish the official images
	// unchanged (Amazon ECR Public: docker/library/<name>).
	repo = strings.TrimPrefix(repo, "docker/library/")
	switch repo {
	case "node", "circleci/node", "cimg/node":
		return FamilyNode
	case "python", "circleci/python", "cimg/python":
		return FamilyPython
	case "golang", "circleci/golang", "cimg/go":
		return FamilyGolang
	case "eclipse-temurin", "openjdk", "amazoncorretto", "maven", "gradle", "ibm-semeru-runtimes", "sapmachine", "bellsoft/liberica-openjdk-alpine", "bellsoft/liberica-openjre-alpine":
		return FamilyJava
	case "ubuntu":
		return FamilyUbuntu
	case "debian":
		return FamilyDebian
	case "alpine":
		return FamilyAlpine
	case "buildpack-deps":
		return FamilyBuildpack
	case "centos", "fedora", "amazonlinux", "rockylinux", "almalinux", "oraclelinux",
		"ubi8", "ubi9", "ubi8/ubi", "ubi9/ubi", "ubi8/ubi-minimal", "ubi9/ubi-minimal",
		"redhat/ubi8", "redhat/ubi9", "redhat/ubi8-minimal", "redhat/ubi9-minimal":
		return FamilyRHEL
	}
	return FamilyUnknown
}

// Variant classifies an official language image's tag as full, slim or
// alpine. Non-language families report their own name.
func (i Image) Variant() string {
	if c := i.Custom(); c != nil && c.Variant != "" {
		return c.Variant
	}
	tag := strings.ToLower(i.Tag)
	switch {
	case strings.Contains(tag, "alpine"):
		return VariantAlpine
	case strings.Contains(tag, "slim"):
		return VariantSlim
	}
	return VariantFull
}

var versionRe = regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?`)

// Version extracts major and minor from the tag ("22-alpine" -> 22, -1;
// "3.12-slim" -> 3, 12). Missing parts are -1.
func (i Image) Version() (major, minor int) {
	tag := i.Tag
	if c := i.Custom(); c != nil {
		// A declared version wins; an internal tag ("2024.06-hardened")
		// rarely names the language version.
		tag = c.Version
	}
	m := versionRe.FindStringSubmatch(tag)
	if m == nil {
		return -1, -1
	}
	major = atoi(m[1])
	minor = -1
	if m[2] != "" {
		minor = atoi(m[2])
	}
	return major, minor
}

// OSFamily reports the package-manager world the image lives in: "debian"
// (apt), "alpine" (apk), "rhel" (dnf/yum) or "" when it has none.
func (i Image) OSFamily() string {
	switch i.Family() {
	case FamilyAlpine:
		return "alpine"
	case FamilyUbuntu, FamilyDebian, FamilyBuildpack:
		return "debian"
	case FamilyRHEL:
		return "rhel"
	case FamilyNode, FamilyPython, FamilyGolang, FamilyJava:
		if i.Variant() == VariantAlpine {
			return "alpine"
		}
		return "debian"
	}
	return ""
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
