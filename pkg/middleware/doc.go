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
//	    middleware.UnaryRecovery,   // outermost
//	    middleware.UnaryRateLimit,  // Phase 4
//	    middleware.UnaryAuth,       // Phase 4
//	    middleware.UnaryLogging,    // innermost — logs the real handler
//	)
//
// Deliberately absent until the phase that needs them:
//
//   - UnaryAuth — validates the session token from Phase 5.3 and puts the user
//     id in the context. Lands with Phase 4, alongside the auth service.
//     Authorization itself is not an interceptor's job: Phase 9.2 requires the
//     "only your own data" rule be enforced at the service layer on every
//     query, not at the gateway.
//
//   - UnaryRateLimit — per-IP limiting, which Phase 9.2 ships with the Phase 4
//     launch rather than deferring. Needs a limiter dependency
//     (golang.org/x/time/rate) plus per-IP eviction, so it is added with Phase
//     4 rather than as an unused stub now.
//
//   - Tracing — the reference storage-plugin threads a trace/span id through
//     every call because it sits in a multi-service platform. This is one
//     small service (Phase 5.1); Phase 8.5 asks only for structured logs in the
//     hosting platform's viewer. Revisit if a second service ever appears.
package middleware
