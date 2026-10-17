// Package middleware holds the gRPC interceptors the API server chains.
//
// Present now, because they are pure infrastructure and need no dependency
// beyond gRPC itself:
//
//   - UnaryRecovery / StreamRecovery — turn a panic into an Internal error
//     instead of killing the process.
//   - UnaryLogging / StreamLogging — one structured record per call, with
//     sensitive values redacted.
//
// Order matters. Recovery must be outermost so it also catches a panic raised
// inside another interceptor:
//
//	grpc.ChainUnaryInterceptor(
//	    middleware.UnaryRecovery, // outermost
//	    middleware.UnaryLogging,  // logs rejected calls too
//	    authInterceptor(auth),    // internal/backend: needs the auth service
//	)
//
// Three concerns are deliberately handled elsewhere, or not at all:
//
//   - Authentication is internal/backend/auth.go, because it depends on the
//     session service. Authorization is not an interceptor's job at all:
//     Phase 9.2 requires the "only your own data" rule be enforced in the
//     service layer on every query.
//
//   - Per-IP rate limiting (Phase 9.2, shipped at launch) is HTTP middleware
//     in internal/backend/httpmw.go, so it also covers the OAuth redirect
//     routes that never reach gRPC. A hand-rolled token bucket with idle
//     eviction; no limiter dependency.
//
//   - Tracing — the reference storage-plugin threads a trace/span id through
//     every call because it sits in a multi-service platform. This is one
//     small service (Phase 5.1); Phase 8.5 asks only for structured logs in the
//     hosting platform's viewer. Revisit if a second service ever appears.
package middleware
