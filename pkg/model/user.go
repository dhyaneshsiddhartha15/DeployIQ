package model

import "time"

// Plan names the account tier. Only PlanFree exists at Phase 4; a paid tier is
// gated on an unprompted willingness-to-pay signal (Phase 13.1) and the field
// is present now only because Phase 7.1 defines it on the collection.
type Plan string

const (
	// PlanFree is the only tier available at launch.
	PlanFree Plan = "free"
)

// User is a dashboard account, created on first GitHub OAuth login.
//
// Backs the `users` collection (Phase 7.1). GitHubID carries a unique index —
// it is the OAuth login lookup key (Phase 7.2).
//
// There is deliberately no password field and never will be: Phase 3.1 chose
// GitHub OAuth specifically to avoid password storage liability.
type User struct {
	// ID is the store-assigned identifier, as an opaque string.
	ID string `json:"id"`

	// GitHubID is the numeric GitHub account id, stringified. Unique.
	GitHubID string `json:"githubId"`

	// Email comes from the GitHub profile. May be empty — a GitHub user can
	// keep their address private.
	Email string `json:"email,omitempty"`

	// Plan is the account tier.
	Plan Plan `json:"plan"`

	// CreatedAt is when the account first logged in.
	CreatedAt time.Time `json:"createdAt"`
}

// Session is a short-lived token the API issues to the CLI after a successful
// OAuth exchange (Phase 5.3). It is never persisted in the repository being
// analysed and never committed — the CLI stores it under the user config dir.
//
// Authorization stays deliberately trivial: a session identifies one user, and
// a user may only reach their own data (Phase 9.2). No roles, no permissions —
// those wait for team accounts (Phase 13.1).
type Session struct {
	// UserID is the account this session authenticates.
	UserID string `json:"userId"`

	// IssuedAt is when the token was minted.
	IssuedAt time.Time `json:"issuedAt"`

	// ExpiresAt is when it stops being accepted.
	ExpiresAt time.Time `json:"expiresAt"`
}

// Expired reports whether the session is no longer valid at time now.
func (s Session) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}
