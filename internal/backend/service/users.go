package service

import (
	"context"

	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// Users is the business logic behind /api/v1/me.
type Users struct {
	store UserStore
}

// NewUsers returns the service.
func NewUsers(store UserStore) *Users { return &Users{store: store} }

// Me returns the caller's own account. There is no way to ask for anyone
// else's: the id comes from the verified session, not from the request.
func (s *Users) Me(ctx context.Context) (model.User, error) {
	id := UserID(ctx)
	if id == "" {
		return model.User{}, apperrors.Unauthenticated("authentication required")
	}
	u, err := s.store.GetUser(ctx, id)
	if apperrors.KindOf(err) == apperrors.KindNotFound {
		// A valid token for a deleted account: treat as logged out.
		return model.User{}, apperrors.Unauthenticated("session refers to an account that no longer exists")
	}
	if err != nil {
		return model.User{}, wrapStore("get user", err)
	}
	return u, nil
}
