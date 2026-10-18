package service

import (
	"context"
	"regexp"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// Limits on an inbound report. Generous for any real run, tight enough that a
// hostile client cannot store megabytes per document.
const (
	maxRepoNameLen   = 200
	maxRules         = 32
	maxRuleLen       = 64
	maxEstimateMB    = 1 << 20 // 1 TB: no real image is larger
	DefaultListLimit = 50
	MaxListLimit     = 500
)

var (
	// owner/repo, nested groups (GitLab), or a bare name; no spaces, no
	// control characters, no leading slash.
	repoNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`)
	ruleRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// Optimizations is the business logic behind /api/v1/optimizations.
type Optimizations struct {
	store OptimizationStore

	// orgs resolves the organisation a report is sent into and checks the
	// caller belongs to it; users supplies the reporter's display name.
	// Both are optional (nil disables organisation reports).
	orgs  *Orgs
	users UserStore
}

// NewOptimizations returns the service.
func NewOptimizations(store OptimizationStore) *Optimizations {
	return &Optimizations{store: store}
}

// WithOrgs enables reporting into organisations.
func (s *Optimizations) WithOrgs(orgs *Orgs, users UserStore) *Optimizations {
	s.orgs, s.users = orgs, users
	return s
}

// Create validates and stores one opt-in report for the caller. Ownership comes
// from the authenticated context, never from the request (Phase 9.2).
func (s *Optimizations) Create(ctx context.Context, r model.OptimizationReport) (model.Optimization, error) {
	userID := UserID(ctx)
	if userID == "" {
		return model.Optimization{}, apperrors.Unauthenticated("authentication required")
	}
	if err := ValidateReport(&r); err != nil {
		return model.Optimization{}, err
	}
	rec := model.Optimization{
		UserID:             userID,
		RepoName:           r.RepoName,
		DetectedStack:      r.DetectedStack,
		OriginalEstSizeMB:  r.OriginalEstSizeMB,
		OptimizedEstSizeMB: r.OptimizedEstSizeMB,
		RulesApplied:       r.RulesApplied,
	}
	if slug := strings.TrimSpace(r.Org); slug != "" {
		if s.orgs == nil {
			return model.Optimization{}, apperrors.InvalidField("org", "organizations are not enabled on this server")
		}
		org, err := s.orgs.ResolveForReport(ctx, slug)
		if err != nil {
			return model.Optimization{}, err
		}
		rec.OrgID = org.ID
	}
	if s.users != nil {
		if u, err := s.users.GetUser(ctx, userID); err == nil {
			rec.ReportedBy = u.Display()
		}
	}
	o, err := s.store.InsertOptimization(ctx, rec)
	if err != nil {
		return model.Optimization{}, wrapStore("store optimization", err)
	}
	return o, nil
}

// List returns the caller's history. The user filter is applied here, on every
// query, as Phase 9.2 requires — not left to the handler or the gateway.
func (s *Optimizations) List(ctx context.Context, repo string, limit int) ([]model.Optimization, error) {
	userID := UserID(ctx)
	if userID == "" {
		return nil, apperrors.Unauthenticated("authentication required")
	}
	repo = strings.TrimSpace(repo)
	if repo != "" && !validRepoName(repo) {
		return nil, apperrors.InvalidField("repo", "must be a repository name such as owner/name")
	}
	if limit < 0 {
		return nil, apperrors.InvalidField("limit", "must not be negative")
	}
	limit = clampLimit(limit)
	list, err := s.store.ListOptimizations(ctx, userID, repo, limit)
	if err != nil {
		return nil, wrapStore("list optimizations", err)
	}
	if list == nil {
		list = []model.Optimization{}
	}
	return list, nil
}

// ValidateReport checks an inbound body against the strict schema Phase 5.4
// asks for and normalises it in place. Every message is a fixed string: the
// submitted value is never echoed back (Phase 9.2).
func ValidateReport(r *model.OptimizationReport) error {
	r.RepoName = strings.TrimSpace(r.RepoName)
	switch {
	case r.RepoName == "":
		return apperrors.InvalidField("repoName", "must not be empty")
	case len(r.RepoName) > maxRepoNameLen:
		return apperrors.InvalidField("repoName", "must be at most 200 characters")
	case !validRepoName(r.RepoName):
		return apperrors.InvalidField("repoName", "must be a repository name such as owner/name")
	}

	supported := false
	for _, s := range constants.SupportedStacks {
		if r.DetectedStack == s {
			supported = true
		}
	}
	if !supported {
		return apperrors.InvalidField("detectedStack", "must be one of: "+strings.Join(constants.SupportedStacks, ", "))
	}

	switch {
	case r.OriginalEstSizeMB <= 0:
		return apperrors.InvalidField("originalEstSizeMB", "must be a positive number of megabytes")
	case r.OriginalEstSizeMB > maxEstimateMB:
		return apperrors.InvalidField("originalEstSizeMB", "is implausibly large")
	case r.OptimizedEstSizeMB <= 0:
		return apperrors.InvalidField("optimizedEstSizeMB", "must be a positive number of megabytes")
	case r.OptimizedEstSizeMB > maxEstimateMB:
		return apperrors.InvalidField("optimizedEstSizeMB", "is implausibly large")
	}

	if len(r.RulesApplied) > maxRules {
		return apperrors.InvalidField("rulesApplied", "must list at most 32 rules")
	}
	seen := map[string]bool{}
	rules := make([]string, 0, len(r.RulesApplied))
	for _, rule := range r.RulesApplied {
		if len(rule) > maxRuleLen || !ruleRe.MatchString(rule) {
			return apperrors.InvalidField("rulesApplied", "rule ids are lowercase words joined by hyphens")
		}
		if !seen[rule] {
			seen[rule] = true
			rules = append(rules, rule)
		}
	}
	r.RulesApplied = rules
	return nil
}

// validRepoName accepts owner/name style names. Segments of only dots are
// refused: ".." is not a repository, and a name that looks like a path
// traversal would be a trap for any future code that builds a URL or path
// from it.
func validRepoName(name string) bool {
	if len(name) > maxRepoNameLen || !repoNameRe.MatchString(name) {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if strings.Trim(seg, ".") == "" {
			return false
		}
	}
	return true
}

// wrapStore classifies a storage error: classified errors (NotFound and
// friends) pass through; anything else is internal and hidden from clients.
func wrapStore(op string, err error) error {
	if apperrors.KindOf(err) != apperrors.KindInternal {
		return err
	}
	return apperrors.Internal(op, err)
}
