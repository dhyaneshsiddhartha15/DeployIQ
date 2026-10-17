package dockerfile

import (
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
	FamilyUnknown    = ""
)

// Variants of an official language image.
const (
	VariantFull   = "full"
	VariantSlim   = "slim"
	VariantAlpine = "alpine"
)

// Family classifies the image. Only official images and well-known vendor
// repositories are recognised: a private "company/base" image could contain
// anything, and v1 refuses to guess what (Phase 1.4).
func (i Image) Family() string {
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
	m := versionRe.FindStringSubmatch(i.Tag)
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
	case FamilyNode, FamilyPython, FamilyGolang:
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
