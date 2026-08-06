package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sensitiveFieldRe matches key:"value" and key=value forms, which is what
// fmt's %+v produces for a struct. Ported from the reference storage-plugin's
// logging interceptor, whose redaction rule this project keeps.
var sensitiveFieldRe = regexp.MustCompile(
	`(?i)(password|secret_?key|access_?key|client_?secret|signing_?key|token|secret)\s*[:=]\s*("[^"]*"|[^\s}]+)`,
)

// Redact replaces the value half of any sensitive-looking field with ***.
//
// This is defence in depth, not the primary control. The primary control is not
// putting secrets in a request message in the first place. But an OAuth code, a
// session token and a client secret all pass through this service, and a debug
// log is exactly where one would otherwise end up in a hosting platform's log
// viewer forever (Phase 8.4, Phase 9.2).
func Redact(s string) string {
	return sensitiveFieldRe.ReplaceAllStringFunc(s, func(match string) string {
		loc := sensitiveFieldRe.FindStringSubmatchIndex(match)
		return match[:loc[4]] + "***"
	})
}

// isReadOnlyMethod reports whether the method only reads. FullMethod is
// /package.Service/Method.
//
// Read-only calls are logged at debug rather than info: the dashboard polls
// them, and one info record per poll is noise that buries the writes that
// actually matter.
func isReadOnlyMethod(fullMethod string) bool {
	name := fullMethod
	if i := strings.LastIndexByte(fullMethod, '/'); i >= 0 {
		name = fullMethod[i+1:]
	}
	return strings.HasPrefix(name, "Get") || strings.HasPrefix(name, "List")
}

// UnaryLogging logs one record per unary call: the method, its duration and its
// resulting gRPC code. The request payload is included only at debug level, and
// only after redaction.
func UnaryLogging(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	start := time.Now()

	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.DebugContext(ctx, "grpc request",
			slog.String("method", info.FullMethod),
			slog.String("request", Redact(fmt.Sprintf("%+v", req))),
		)
	}

	resp, err := handler(ctx, req)
	logResult(ctx, info.FullMethod, time.Since(start), err)
	return resp, err
}

// StreamLogging is the streaming counterpart of UnaryLogging. No stream RPCs
// exist yet — the Phase 4.5 surface is four unary calls — but the server chains
// it so adding one later needs no change here.
func StreamLogging(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()
	err := handler(srv, ss)
	logResult(ss.Context(), info.FullMethod, time.Since(start), err)
	return err
}

// logResult emits the single completion record for a call.
//
// The error is logged in full — that is the point of a log. Phase 5.4's rule
// that internal detail must not leak applies to the *response*, and is enforced
// by pkg/errors.Public at the handler boundary, not here.
func logResult(ctx context.Context, method string, took time.Duration, err error) {
	attrs := []any{
		slog.String("method", method),
		slog.Duration("duration", took),
		slog.String("code", status.Code(err).String()),
	}

	switch {
	case err == nil && isReadOnlyMethod(method):
		slog.DebugContext(ctx, "grpc call", attrs...)
	case err == nil:
		slog.InfoContext(ctx, "grpc call", attrs...)
	case status.Code(err) == codes.Internal || status.Code(err) == codes.Unknown:
		// Our fault. Loud, with the cause attached.
		slog.ErrorContext(ctx, "grpc call failed", append(attrs, slog.String("error", Redact(err.Error())))...)
	default:
		// Client's fault — a validation or auth rejection. Expected
		// traffic, not an incident, so it must not pollute the error rate
		// Phase 12.1 alerts on.
		slog.WarnContext(ctx, "grpc call rejected", append(attrs, slog.String("error", Redact(err.Error())))...)
	}
}
