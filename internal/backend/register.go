package backend

import (
	"context"

	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
)

// registerServices attaches every gRPC service implementation to the server.
//
// This is the single seam for wiring: New calls it once, and nothing else in
// the package knows which services exist. Adding one is a two-line change here
// plus its twin in registerGateways below — the same pattern the reference
// project uses, kept out of main so cmd/ stays a thin entrypoint.
//
// Empty by design. Phase 4.5 defines the first four routes; the task that adds
// them owns the handler, service and repository behind each one. A stub service
// registered now would answer Unimplemented on a route the CLI might treat as
// live, which is worse than a 404 from an unrouted path.
//
//	api.RegisterOptimizationsServiceServer(srv, handler.NewOptimizations(svc))
//	api.RegisterAuthServiceServer(srv, handler.NewAuth(svc))
func registerServices(srv *grpc.Server) {
	_ = srv
}

// registerGateways attaches the REST/JSON translation for each service to mux.
//
// Every service registered above needs exactly one entry here, or its routes
// exist over gRPC but not over the public HTTP listener the CLI and dashboard
// actually use.
//
//	if err := api.RegisterOptimizationsServiceHandler(ctx, mux, conn); err != nil {
//	    return fmt.Errorf("backend: registering optimizations gateway: %w", err)
//	}
func registerGateways(ctx context.Context, mux *gwruntime.ServeMux, conn *grpc.ClientConn) error {
	_, _, _ = ctx, mux, conn
	return nil
}
