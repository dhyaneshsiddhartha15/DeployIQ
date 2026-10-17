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
}

// UserStore persists accounts.
type UserStore interface {
	// UpsertUserByGitHubID creates the account on first login and refreshes
	// profile fields on later ones. Plan and CreatedAt are set only on
	// creation.
	UpsertUserByGitHubID(ctx context.Context, u model.User) (model.User, error)

	// GetUser returns the account, or an errors.NotFound.
	GetUser(ctx context.Context, id string) (model.User, error)
}

// Store is both, as the repository implementations provide.
type Store interface {
	OptimizationStore
	UserStore
	Ping(ctx context.Context) error
	Close(ctx context.Context) error
}
