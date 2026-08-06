// Package report formats the CLI's terminal output (Phase 4.1, diff.go).
//
// Last module in the pipeline, and the only one the user actually sees: the
// detected stack, a diff of the current Dockerfile against the generated one,
// and the estimated before/after size (FR-4).
//
// Constraints:
//
//   - Report output goes to stdout, and stdout carries nothing else. Logs go to
//     stderr (see pkg/logger) so `doiq . > out` stays pipeable.
//   - Size figures are always labelled as estimates (Phase 1.5).
//   - Printing a generated Dockerfile is not writing one. The default mode is
//     --dry-run and nothing reaches disk without --write or an interactive
//     confirmation (FR-5, FR-6, US-2).
//
// This is glue code by Phase 10.3's classification: correctness matters far less
// here than in detector/dockerfile/rules, and chasing a coverage number on
// formatting is not worth the effort.
//
// Empty until Phase 2 of the build plan (MVP).
package report
