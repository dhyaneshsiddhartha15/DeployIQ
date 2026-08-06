// Package analyzer estimates image sizes before and after optimization
// (Phase 4.1, sizeestimate.go; FR-4).
//
// Estimates, not measurements — and the distinction is a business rule, not a
// caveat. Phase 1.5: size figures are directionally accurate, never
// byte-for-byte guarantees, and must be labelled as estimates everywhere they
// surface. The model types name their fields ...EstSizeMB for the same reason.
//
// Estimation is static: base-image sizes from a known table, plus the layers
// the Dockerfile would add. Nothing is pulled and nothing is built. Phase 0.6
// requires the CLI to run fully offline with no network calls, and Phase 0.7
// gives the whole run a five-second budget for a typical repo — neither
// survives a docker pull.
//
// Phase 0.5.1 tracks average reported size reduction against a 50%+ target, so
// a systematically optimistic estimate here quietly corrupts the metric the
// product is judged on. Bias toward under-promising.
//
// Phase 10.3 puts the size-estimate maths in the high-coverage band.
//
// Empty until Phase 2 of the build plan (MVP).
package analyzer
