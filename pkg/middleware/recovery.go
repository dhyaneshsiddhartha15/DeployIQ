package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"google.golang.org/grpc"

	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
)

// UnaryRecovery converts a panic in a handler into an Internal error.
//
// Without this a single nil-map write takes the whole process down. For a
// single-instance deployment (Phase 2.5: vertical scaling only, one instance at
// Phase 4 launch) that is a full outage for every user, not one failed request.
//
// The panic value and stack go to the log; the client gets the same generic
// message as any other internal error (Phase 5.4).
func UnaryRecovery(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = recovered(ctx, info.FullMethod, p)
			resp = nil
		}
	}()
	return handler(ctx, req)
}

// StreamRecovery is the streaming counterpart of UnaryRecovery.
func StreamRecovery(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = recovered(ss.Context(), info.FullMethod, p)
		}
	}()
	return handler(srv, ss)
}

// recovered logs a panic with its stack and returns the error to send back.
func recovered(ctx context.Context, method string, p any) error {
	slog.ErrorContext(ctx, "recovered from panic in grpc handler",
		slog.String("method", method),
		slog.String("panic", Redact(fmt.Sprint(p))),
		slog.String("stack", string(debug.Stack())),
	)
	return apperrors.GRPCStatus(apperrors.Internal("panic in "+method, fmt.Errorf("%v", p)))
}
