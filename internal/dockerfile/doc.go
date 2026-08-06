// Package dockerfile reads existing Dockerfiles and writes optimized ones
// (Phase 4.1; FR-2, FR-3).
//
// Two files, two jobs, per Phase 4.3:
//
//	parser.go     Parse  — read the repo's Dockerfile into a structure
//	generator.go  Generate — render the rule engine's decisions back out
//
// A hand-rolled parser, not BuildKit's (Phase 3.1). BuildKit's is heavier than
// read-only analysis needs, and shelling out to docker would mean requiring
// Docker just to *analyse* a Dockerfile — an unacceptable dependency for a tool
// whose pitch is zero setup.
//
// Rules this module owns:
//
//   - A missing Dockerfile is not an error. Infer one from stack conventions
//     instead of demanding a file to optimize (Phase 1.4).
//   - Unusual or unsupported syntax fails safely with a clear message rather
//     than a guess (Phase 1.4). Same reasoning as the detector: a wrong answer
//     is worse than no answer.
//   - Parsing never executes anything (Phase 9.1).
//   - Generate returns bytes. It does not write files — that decision belongs
//     to the CLI, gated behind --write or an interactive confirmation (FR-5).
//     Keeping the write out of here means no code path can accidentally acquire
//     one.
//
// Phase 10.3 puts this package in the 80%+ coverage band.
//
// Empty until Phase 1 of the build plan (MVP).
package dockerfile
