package main

import (
	"context"
	"fmt"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/measure"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/report"
)

// measureReport builds the current and the optimized image for real and fills
// in rep.Measured (and rep.Vulns with -scan-vulns). The images are deleted
// afterwards unless -keep-images is set.
func measureReport(root string, rep *report.Report, opts *options, e env) error {
	ctx := context.Background()
	d, err := measure.New(ctx, opts.buildArgs)
	if err != nil {
		return err
	}
	beforeTag, afterTag := measure.Tag()
	m := &report.Measurement{}

	if rep.ExistingPath != "" {
		fmt.Fprintf(e.stderr, "Building the current image (%s)...\n", rep.ExistingPath)
		size, err := d.Build(ctx, root, root+"/"+rep.ExistingPath, beforeTag)
		if err != nil {
			// The current Dockerfile not building is the user's existing
			// state, not a failure of the optimized one: say so and go on.
			fmt.Fprintf(e.stderr, "The current Dockerfile did not build, so there is nothing to compare against.\n%v\n", err)
		} else {
			m.BeforeMB, m.BeforeImage = size, beforeTag
		}
	}
	fmt.Fprintf(e.stderr, "Building the optimized image...\n")
	size, err := d.BuildBytes(ctx, root, []byte(rep.Dockerfile), afterTag)
	if err != nil {
		d.Remove(m.BeforeImage)
		return fmt.Errorf("the optimized Dockerfile did not build; please report this: %w", err)
	}
	m.AfterMB, m.AfterImage = size, afterTag
	rep.Measured = m

	if opts.scanVulns {
		fmt.Fprintf(e.stderr, "Scanning for known vulnerabilities (the first scan downloads Trivy's database)...\n")
		v := &report.VulnComparison{}
		if m.BeforeImage != "" {
			if v.Before, v.Scanner, err = d.Scan(ctx, m.BeforeImage); err != nil {
				return err
			}
			v.HasBefore = true
		}
		if v.After, v.Scanner, err = d.Scan(ctx, m.AfterImage); err != nil {
			return err
		}
		rep.Vulns = v
	}

	if opts.keepImages {
		fmt.Fprintf(e.stderr, "Kept images: %s %s\n", m.BeforeImage, m.AfterImage)
	} else {
		d.Remove(m.BeforeImage, m.AfterImage)
		m.BeforeImage, m.AfterImage = "", ""
	}
	return nil
}
