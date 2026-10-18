package analyzer

import (
	"math"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
)

// Profile describes the contents of one image, in the terms the estimate is
// built from. The rule engine fills one in for the current image and one for
// the generated image; it knows what each contains, this package knows what
// each part weighs.
type Profile struct {
	// BaseImage is the runtime stage's base reference.
	BaseImage string

	// SystemPackages are OS packages installed on top of the base.
	SystemPackages []string

	// DependenciesMB is installed language dependencies.
	DependenciesMB float64

	// CacheMB is package-manager cache left in the image.
	CacheMB float64

	// SourceMB is application files copied in.
	SourceMB float64

	// ArtifactMB is build output: a compiled binary, a bundle.
	ArtifactMB float64

	// ToolchainMB is a compiler or SDK installed on top of the base (a Go
	// build cache, for example) that the runtime does not need.
	ToolchainMB float64
}

// Breakdown is an estimated image size by component, in MB.
type Breakdown struct {
	BaseMB         float64 `json:"baseMB"`
	PackagesMB     float64 `json:"packagesMB"`
	DependenciesMB float64 `json:"dependenciesMB"`
	CacheMB        float64 `json:"cacheMB"`
	SourceMB       float64 `json:"sourceMB"`
	ArtifactMB     float64 `json:"artifactMB"`
	ToolchainMB    float64 `json:"toolchainMB"`
	TotalMB        float64 `json:"totalMB"`

	// BaseKnown is false when the base image is not in the size table and
	// a conservative default was used.
	BaseKnown bool `json:"baseKnown"`
}

// Estimate is the before/after comparison (FR-4). Every figure is an estimate:
// Phase 1.5 requires they be labelled as such wherever they are shown.
type Estimate struct {
	Before Breakdown `json:"before"`
	After  Breakdown `json:"after"`

	// SavedMB and SavedPercent are never negative.
	SavedMB      float64 `json:"savedMB"`
	SavedPercent float64 `json:"savedPercent"`
}

// Compare estimates both profiles.
//
// The rounding is deliberately asymmetric: "before" rounds down and "after"
// rounds up, so the reported saving is never larger than the arithmetic
// supports. Phase 0.5.1 tracks reported savings against a 50% target; an
// estimate that flatters the tool corrupts the metric it is judged on.
func Compare(before, after Profile) Estimate {
	b := Size(before)
	a := Size(after)
	b.TotalMB = math.Floor(b.TotalMB)
	a.TotalMB = math.Ceil(a.TotalMB)

	e := Estimate{Before: b, After: a}
	if b.TotalMB > a.TotalMB && b.TotalMB > 0 {
		e.SavedMB = b.TotalMB - a.TotalMB
		e.SavedPercent = math.Floor(e.SavedMB/b.TotalMB*1000) / 10
	}
	return e
}

// Size estimates one profile.
func Size(p Profile) Breakdown {
	base, known := BaseImageMB(p.BaseImage)
	img := dockerfile.ParseImage(p.BaseImage)
	b := Breakdown{
		BaseMB:         base,
		BaseKnown:      known,
		PackagesMB:     packagesMB(p.SystemPackages, img.OSFamily()),
		DependenciesMB: p.DependenciesMB,
		CacheMB:        p.CacheMB,
		SourceMB:       p.SourceMB,
		ArtifactMB:     p.ArtifactMB,
		ToolchainMB:    p.ToolchainMB,
	}
	b.TotalMB = b.BaseMB + b.PackagesMB + b.DependenciesMB + b.CacheMB + b.SourceMB + b.ArtifactMB + b.ToolchainMB
	return b
}

// unknownBaseMB is used for a base image missing from the table. The rule
// engine refuses unrecognised families outright, so this is reached only for
// an unusual tag of a known family; 100 MB is a middle-of-the-road guess that
// neither inflates nor erases the saving.
const unknownBaseMB = 100

// BaseImageMB returns the approximate uncompressed size of an image, as
// `docker images` reports it, for linux/amd64. Values are rounded and current
// as of the default tags the generator emits; they drift slowly and are
// revisited whenever a default base image changes (Phase 12.3).
func BaseImageMB(ref string) (float64, bool) {
	img := dockerfile.ParseImage(ref)
	tag := strings.ToLower(img.Tag)
	variant := img.Variant()
	if c := img.Custom(); c != nil && c.SizeMB > 0 {
		return c.SizeMB, true
	}

	switch img.Family() {
	case dockerfile.FamilyJava:
		return javaImageMB(img), true
	case dockerfile.FamilyScratch:
		return 0, true
	case dockerfile.FamilyDistroless:
		switch {
		case strings.Contains(img.Repository, "static"):
			return 2, true
		case strings.Contains(img.Repository, "base-nossl"):
			return 15, true
		case strings.Contains(img.Repository, "base"):
			return 21, true
		case strings.Contains(img.Repository, "cc"):
			return 24, true
		case strings.Contains(img.Repository, "nodejs"):
			return 130, true
		case strings.Contains(img.Repository, "python"):
			return 55, true
		}
		return 20, false
	case dockerfile.FamilyNode:
		switch variant {
		case dockerfile.VariantAlpine:
			return 160, true
		case dockerfile.VariantSlim:
			return 220, true
		}
		return 1100, true
	case dockerfile.FamilyPython:
		switch variant {
		case dockerfile.VariantAlpine:
			return 60, true
		case dockerfile.VariantSlim:
			return 150, true
		}
		return 1020, true
	case dockerfile.FamilyGolang:
		if variant == dockerfile.VariantAlpine {
			return 260, true
		}
		return 850, true
	case dockerfile.FamilyUbuntu:
		return 78, true
	case dockerfile.FamilyDebian:
		if strings.Contains(tag, "slim") {
			return 75, true
		}
		return 117, true
	case dockerfile.FamilyAlpine:
		return 8, true
	case dockerfile.FamilyBuildpack:
		switch {
		case strings.Contains(tag, "curl"):
			return 140, true
		case strings.Contains(tag, "scm"):
			return 290, true
		}
		return 820, true
	case dockerfile.FamilyRHEL:
		switch {
		case strings.Contains(img.Repository, "minimal"):
			return 100, true
		case img.Repository == "amazonlinux":
			return 145, true
		case img.Repository == "centos":
			return 204, true
		}
		return 215, true
	}
	return unknownBaseMB, false
}

// javaImageMB sizes the Java images: build images (Maven, Gradle) carry a JDK
// plus the build tool; runtime images a JRE. Alpine variants are smaller.
func javaImageMB(img dockerfile.Image) float64 {
	tag := strings.ToLower(img.Tag)
	alpine := strings.Contains(tag, "alpine")
	switch img.Name() {
	case "maven":
		if alpine {
			return 380
		}
		return 520
	case "gradle":
		if alpine {
			return 560
		}
		return 720
	}
	jre := strings.Contains(tag, "jre") || strings.Contains(img.Repository, "jre")
	switch {
	case jre && alpine:
		return 200
	case jre:
		return 315
	case alpine:
		return 340
	}
	return 470
}

// packageMB is the approximate installed size of common OS packages, including
// the dependencies they pull in on a slim Debian base with
// --no-install-recommends. Only packages worth distinguishing are listed;
// anything else uses the default.
var packageMB = map[string]float64{
	"build-essential": 230, "gcc": 110, "g++": 60, "make": 1, "cmake": 30,
	"git": 45, "curl": 8, "wget": 4, "ca-certificates": 1, "gnupg": 10,
	"python3": 50, "python3-pip": 280, "python3-dev": 40, "python3-venv": 6,
	"nodejs": 90, "npm": 50, "yarn": 6,
	"libpq-dev": 25, "libpq5": 3, "default-libmysqlclient-dev": 15, "libmariadb3": 3,
	"pkg-config": 2, "vim": 40, "nano": 3, "procps": 2, "tzdata": 3,
	"ffmpeg": 300, "imagemagick": 80, "chromium": 400, "openjdk-17-jdk": 450,
	"libffi-dev": 1, "libssl-dev": 10, "musl-dev": 10, "linux-headers": 10,
	"bash": 2, "openssh-client": 5, "unzip": 1, "zip": 1, "jq": 2,
}

// defaultPackageMB is used for an unlisted package: 10 MB on apt, 3 MB on apk
// (Alpine packages are split finer and pull fewer dependencies).
func defaultPackageMB(osFamily string) float64 {
	if osFamily == "alpine" {
		return 3
	}
	return 10
}

func packagesMB(pkgs []string, osFamily string) float64 {
	total := 0.0
	for _, p := range pkgs {
		name := strings.ToLower(p)
		// Strip version pins: pkg=1.2, pkg@edge, pkg>=1.
		if i := strings.IndexAny(name, "=<>@:"); i > 0 {
			name = name[:i]
		}
		mb, ok := packageMB[name]
		if !ok {
			mb = defaultPackageMB(osFamily)
		}
		if osFamily == "alpine" {
			// Alpine equivalents are reliably smaller (musl, no docs).
			mb *= 0.6
		}
		total += mb
	}
	return total
}
