// Package rules holds the stack-specific optimization rules (Phase 4.1) — the
// engine that turns a detected stack and a parsed Dockerfile into an optimized
// one (FR-3).
//
// Layout from Phase 4.3:
//
//	rules.go   the Rule interface and the registry
//	node.go    Node.js
//	go.go      Go
//	python.go  Python
//
// Deterministic and rule-based, never LLM-inferred. Phase 3.2 rules AI out of
// the v1 engine outright: this code writes build files, and a plausible-but-wrong
// suggestion breaks a user's build — the one failure Phase 0.10 rates as
// severe. AI-assisted pruning is gated in Phase 13.1 on a long track record of
// zero build-breaking output.
//
// Rule identifiers ("multi-stage-build", "alpine-base", "prod-only-deps") are
// reported to the API and stored in the optimizations collection (Phase 4.5).
// They are part of a persisted contract — renaming one silently reinterprets
// every historical record.
//
// Two constraints on adding a stack:
//
//   - Phase 1.5: no stack may be advertised until it has fixture coverage under
//     testdata/fixtures/. Documentation claims must match what is tested.
//   - Phase 10.2: every rule is verified in CI by building the Dockerfile it
//     produces. Nothing ships if that fails.
//
// Phase 13.2 flags the known ceiling: v1 applies one rule per stack. As
// coverage grows this must become multiple applicable rules, ranked and
// composed. Designing the Rule interface with that in mind now is cheap;
// building the ranking machinery before it is needed is not.
//
// Phase 10.3 puts this package in the 80%+ coverage band.
//
// Empty until Phase 2 of the build plan (MVP).
package rules
