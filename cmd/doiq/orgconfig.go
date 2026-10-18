package main

import (
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/orgconfig"
)

// applyOrgConfig installs declared base images and registry mirrors for the
// analysis that follows.
func applyOrgConfig(cfg *orgconfig.Config) {
	var customs []dockerfile.CustomImage
	for _, b := range cfg.BaseImages {
		customs = append(customs, dockerfile.CustomImage{
			Match: b.Match, Family: b.Family, Variant: b.Variant, Version: b.Version, SizeMB: b.SizeMB,
		})
	}
	dockerfile.SetCustomImages(customs)
	dockerfile.SetMirrors(cfg.Mirrors, cfg.KeepLibrary())
}
