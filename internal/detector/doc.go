// Package detector identifies a repository's technology stack from its manifest
// files (Phase 4.1; FR-1).
//
// First module in the CLI pipeline, and the one that decides whether the run
// continues at all:
//
//	detector → dockerfile → rules → analyzer → report
//
// Contract, from the requirements it has to satisfy:
//
//   - Inspect package.json, go.mod, requirements.txt and pyproject.toml
//     (FR-1). Nothing else in v1.
//   - Return one of constants.SupportedStacks, in that priority order. A repo
//     with both package.json and go.mod resolves to the first match and the
//     ambiguity is surfaced to the user (Phase 1.4).
//   - When nothing matches, fail loudly: "Could not detect a supported stack in
//     this repo" and a non-zero exit (Phase 1.3 US-3, Phase 0.10 mitigation).
//     Guessing is the failure mode that destroys trust; refusing is not.
//   - Do not execute anything found in the repo. Reading manifests is
//     read-only analysis (Phase 9.1).
//
// Naming follows Phase 4.4: the entry point is detector.Detect, not
// detector.DetectStack.
//
// Phase 10.3 puts this package in the 80%+ coverage band — correctness here is
// the product.
//
// Empty until Phase 1 of the build plan (MVP).
package detector
