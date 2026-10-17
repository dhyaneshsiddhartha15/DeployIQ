package backend

import (
	"context"
	"fmt"

	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"

	apiv1 "github.com/dhyaneshsiddhartha15/DeployIQ/api/v1"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/handler"
)

// registerServices attaches every gRPC service implementation to the server.
//
// This is the single seam for wiring: New calls it once, and nothing else in
// the package knows which services exist. Adding one is a line here plus its
// twin in registerGateways below — the same pattern the reference project
// uses, kept out of main so cmd/ stays a thin entrypoint.
func registerServices(srv *grpc.Server, s services) {
	apiv1.RegisterOptimizationsServiceServer(srv, handler.NewOptimizations(s.optimizations))
	apiv1.RegisterUsersServiceServer(srv, handler.NewUsers(s.users))
}

// registerGateways attaches the REST/JSON translation for each service to mux.
//
// Every service registered above needs exactly one entry here, or its routes
// exist over gRPC but not over the public HTTP listener the CLI and dashboard
// actually use.
func registerGateways(ctx context.Context, mux *gwruntime.ServeMux, conn *grpc.ClientConn) error {
	if err := apiv1.RegisterOptimizationsServiceHandler(ctx, mux, conn); err != nil {
		return fmt.Errorf("backend: registering optimizations gateway: %w", err)
	}
	if err := apiv1.RegisterUsersServiceHandler(ctx, mux, conn); err != nil {
		return fmt.Errorf("backend: registering users gateway: %w", err)
	}
	return nil
}
