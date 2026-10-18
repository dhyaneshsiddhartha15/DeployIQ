package service

import (
	"context"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// OptimizationStore persists optimization records. Implemented by
// internal/backend/repository; declared here, with its consumer, so the
// dependency points from storage to business logic and never the other way
// (Phase 5.1).
type OptimizationStore interface {
	// InsertOptimization stores o and returns it with ID and CreatedAt set.
	InsertOptimization(ctx context.Context, o model.Optimization) (model.Optimization, error)

	// ListOptimizations returns userID's records, newest first. A non-empty
	// repo restricts to that repository. The user filter is mandatory in
	// the signature: there is no way to call this without naming an owner.
	ListOptimizations(ctx context.Context, userID, repo string, limit int) ([]model.Optimization, error)

	// ListOrgOptimizations returns the records reported into orgID. The
	// caller's membership is checked by the service before this is reached.
	ListOrgOptimizations(ctx context.Context, orgID, repo string, limit int) ([]model.Optimization, error)
}

// UserStore persists accounts.
type UserStore interface {
	// UpsertUserByGitHubID creates the account on first login and refreshes
	// profile fields on later ones. Plan and CreatedAt are set only on
	// creation.
	UpsertUserByGitHubID(ctx context.Context, u model.User) (model.User, error)

	// UpsertUserBySubject is the same for an OIDC identity, keyed by
	// (provider, subject).
	UpsertUserBySubject(ctx context.Context, u model.User) (model.User, error)

	// GetUser returns the account, or an errors.NotFound.
	GetUser(ctx context.Context, id string) (model.User, error)

	// FindUser returns the account with this email or GitHub login (either
	// may be empty), or an errors.NotFound.
	FindUser(ctx context.Context, email, login string) (model.User, error)
}

// OrgStore persists organisations, memberships and invitations.
type OrgStore interface {
	// CreateOrg stores org with creatorID as its admin. AlreadyExists when
	// the slug is taken.
	CreateOrg(ctx context.Context, org model.Organization, creatorID string) (model.Organization, error)
	GetOrgBySlug(ctx context.Context, slug string) (model.Organization, error)
	GetOrg(ctx context.Context, id string) (model.Organization, error)
	UpdateOrgSettings(ctx context.Context, orgID string, s model.OrgSettings) (model.Organization, error)

	// ListMemberships returns userID's memberships.
	ListMemberships(ctx context.Context, userID string) ([]model.Membership, error)

	// GetMembership returns the membership, or an errors.NotFound.
	GetMembership(ctx context.Context, orgID, userID string) (model.Membership, error)
	UpsertMembership(ctx context.Context, m model.Membership) error
	DeleteMembership(ctx context.Context, orgID, userID string) error
	ListMembers(ctx context.Context, orgID string) ([]model.Member, error)

	CreateInvitation(ctx context.Context, inv model.Invitation) (model.Invitation, error)
	ListInvitations(ctx context.Context, orgID string) ([]model.Invitation, error)
	DeleteInvitation(ctx context.Context, orgID, id string) error

	// ClaimInvitations deletes and returns every invitation addressed to
	// this email or login.
	ClaimInvitations(ctx context.Context, email, login string) ([]model.Invitation, error)
}

// Store is all of them, as the repository implementations provide.
type Store interface {
	OptimizationStore
	UserStore
	OrgStore
	Ping(ctx context.Context) error
	Close(ctx context.Context) error
}
