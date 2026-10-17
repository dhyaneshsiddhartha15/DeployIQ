package service

import "context"

type ctxKey struct{}

// WithUserID returns ctx carrying the authenticated user's id. Set once, by the
// auth interceptor, after the session token verifies.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, userID)
}

// UserID returns the authenticated user's id, or "" when the request carries
// none. Service methods treat "" as unauthenticated rather than trusting any
// caller-supplied identity.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}
