package backend

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/service"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
)

// authInterceptor verifies the session token on every RPC and puts the user id
// in the context.
//
// Authentication only. Authorization — "a user can only read/write their own
// optimization history" — is enforced in the service layer on every query
// (Phase 9.2), so a handler that forgot to check would still be safe.
//
// Every RPC in the Phase 4.5 surface requires a session; the OAuth routes are
// plain HTTP and never pass through here.
func authInterceptor(auth *service.Auth, users service.UserStore) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		token := bearer(ctx)
		if token == "" {
			return nil, apperrors.GRPCStatus(apperrors.Unauthenticated("missing session token; log in first"))
		}
		s, err := auth.VerifySession(token)
		if err != nil {
			return nil, apperrors.GRPCStatus(err)
		}
		// A valid signature is not enough: the account must still exist.
		// Without this a session outliving its account (deleted, or a
		// restored database) could still write records and create orgs
		// for an owner nobody can sign in as. One indexed read by _id.
		if _, err := users.GetUser(ctx, s.UserID); err != nil {
			if apperrors.KindOf(err) == apperrors.KindNotFound {
				return nil, apperrors.GRPCStatus(apperrors.Unauthenticated("session refers to an account that no longer exists; log in again"))
			}
			return nil, apperrors.GRPCStatus(err)
		}
		return handler(service.WithUserID(ctx, s.UserID), req)
	}
}

// bearer extracts "Bearer <token>" from the authorization metadata the gateway
// forwards from the HTTP Authorization header.
func bearer(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	for _, key := range []string{"authorization", "grpcgateway-authorization"} {
		for _, v := range md.Get(key) {
			if scheme, tok, ok := strings.Cut(v, " "); ok && strings.EqualFold(scheme, "bearer") {
				return strings.TrimSpace(tok)
			}
		}
	}
	return ""
}
