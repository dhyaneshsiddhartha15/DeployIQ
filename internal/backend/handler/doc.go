// Package handler is the transport layer of the Phase 4+ API server: the top
// third of the three-layer split Phase 5.1 defines.
//
// Its only job is transport. Parse the request, call one service method, turn
// the result into a response. A handler that reaches into MongoDB, or decides
// whether a user may see a record, has taken work that belongs a layer down.
//
// Two rules the code here must enforce, both from Phase 5.4:
//
//   - Validate the inbound body against a strict schema and return 400 with a
//     field-level message. Never a generic 500 for bad input. Use
//     errors.InvalidField, which carries the field name through to the client.
//   - Log the error with request context, respond with errors.Public(err). A
//     handler must never pass err.Error() to the client — that is how an
//     internal detail or a reflected input value escapes (Phase 9.2, OWASP).
//
// The shape each handler takes, once Phase 4 lands:
//
//	type Optimizations struct{ svc *service.Optimizations }
//
//	func (h *Optimizations) Create(ctx context.Context, req *apiv1.CreateOptimizationRequest) (*apiv1.Optimization, error) {
//	    if req.GetRepoName() == "" {
//	        return nil, errors.GRPCStatus(errors.InvalidField("repoName", "must not be empty"))
//	    }
//	    ...
//	}
//
// Files: grpc.go (OptimizationsService, UsersService — exposed as REST by the
// gateway), auth.go (GET /api/v1/auth/github/login and /callback: plain HTTP,
// because their job is to issue redirects), errors.go (the one JSON error
// envelope every route returns, and the gateway error handler that keeps
// decoder messages from reflecting input).
package handler
