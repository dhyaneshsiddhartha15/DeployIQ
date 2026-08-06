package model

import "time"

// Optimization is one recorded optimization run, reported by a CLI whose user
// explicitly opted in (Phase 4.6). Backs the `optimizations` collection
// (Phase 7.1).
//
// Size fields are named ...EstSizeMB because they are estimates, not
// measurements. Phase 1.5 makes this a business rule: estimates are
// directionally accurate and must be labelled as estimates everywhere they
// appear, including here.
//
// Phase 13.2 flags this shape as deliberately loose: a schema-tightening pass
// is expected once the dashboard's real query patterns are known. New optional
// fields can be added without a migration (Phase 7.5).
type Optimization struct {
	// ID is the store-assigned identifier, as an opaque string.
	ID string `json:"id"`

	// UserID owns this record. Indexed with CreatedAt descending, which is
	// the dashboard's main query pattern (Phase 7.2).
	UserID string `json:"userId"`

	// RepoName is the reported repository, e.g. "company/backend".
	// Separately indexed for the repo-detail page (Phase 7.2).
	RepoName string `json:"repoName"`

	// DetectedStack is one of constants.SupportedStacks.
	DetectedStack string `json:"detectedStack"`

	// OriginalEstSizeMB is the estimated size of the image the repo builds
	// today.
	OriginalEstSizeMB int64 `json:"originalEstSizeMB"`

	// OptimizedEstSizeMB is the estimated size of the image the generated
	// Dockerfile would build.
	OptimizedEstSizeMB int64 `json:"optimizedEstSizeMB"`

	// RulesApplied lists the optimization rule identifiers that fired,
	// e.g. ["multi-stage-build", "alpine-base", "prod-only-deps"].
	RulesApplied []string `json:"rulesApplied"`

	// CreatedAt is when the report was accepted.
	CreatedAt time.Time `json:"createdAt"`
}

// SavingsPercent returns the estimated size reduction as a percentage, or 0 when
// the original estimate is not positive. The dashboard's headline number and the
// metric Phase 0.5.1 tracks against a 50%+ target.
func (o Optimization) SavingsPercent() float64 {
	if o.OriginalEstSizeMB <= 0 {
		return 0
	}
	saved := o.OriginalEstSizeMB - o.OptimizedEstSizeMB
	if saved <= 0 {
		return 0
	}
	return float64(saved) / float64(o.OriginalEstSizeMB) * 100
}

// OptimizationReport is the inbound body of POST /api/v1/optimizations
// (Phase 4.5). Kept separate from Optimization so the wire contract and the
// stored document can diverge without one silently changing the other: the
// client never supplies ID, UserID or CreatedAt, and must not be able to.
type OptimizationReport struct {
	RepoName           string   `json:"repoName"`
	DetectedStack      string   `json:"detectedStack"`
	OriginalEstSizeMB  int64    `json:"originalEstSizeMB"`
	OptimizedEstSizeMB int64    `json:"optimizedEstSizeMB"`
	RulesApplied       []string `json:"rulesApplied"`
}
