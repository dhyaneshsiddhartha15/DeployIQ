// Package service holds the business logic of the Phase 4+ API server: the
// middle third of the three-layer split Phase 5.1 defines.
//
// It knows nothing about HTTP, gRPC or MongoDB. Inputs and outputs are pkg/model
// types; storage is reached through the repository interfaces this package
// declares (see internal/backend/repository). That is what makes Phase 5.1's
// "the db layer could be swapped later without touching business logic" true
// rather than aspirational — the dependency points inward, from repository to
// service, not the other way.
//
// The interfaces belong here, with the consumer, not with the implementation.
// Go convention, and it keeps the fake used in tests a three-line struct.
//
//	type OptimizationStore interface {
//	    Insert(ctx context.Context, o model.Optimization) (string, error)
//	    ListByUser(ctx context.Context, userID string, limit int) ([]model.Optimization, error)
//	}
//
//	type Optimizations struct{ store OptimizationStore }
//
// One rule this layer owns outright, from Phase 9.2: a user may only reach
// their own data, "enforced at the service layer on every query, not just at
// the API gateway". Every method that touches a record takes the caller's user
// id and filters on it. Not a middleware, not a handler check — here, on every
// query, because this is the layer no transport can bypass.
//
// Empty until Phase 4 of the build plan.
package service
