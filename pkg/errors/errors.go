// Package errors is the project's error framework.
//
// It exists to satisfy two hard rules from the design document:
//
//   - Phase 5.4: malformed input returns 400 with a specific field-level
//     message, never a generic 500.
//   - Phase 5.4 / 9.2: errors are logged with full context, but API responses
//     never leak internal error strings, and never reflect user input back.
//
// The shape is deliberately plain: a Kind enum, an *Error carrying an optional
// field name and a caller-safe message, and wrapping via the standard library's
// errors.Is/errors.As/%w. No third-party error package — fmt.Errorf plus a
// concrete type covers everything needed here.
//
// Usage:
//
//	if repo == "" {
//	    return errors.InvalidField("repoName", "must not be empty")
//	}
//	if err := store.Insert(ctx, doc); err != nil {
//	    return errors.Internal("store optimization", err) // wraps, hides detail
//	}
//
// At the transport edge: log err with full context, then send Public(err) with
// HTTPStatus(err) (or GRPCCode(err) for a gRPC handler).
package errors

import (
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Re-exported so callers need only this package for error handling.
var (
	Is     = errors.Is
	As     = errors.As
	Join   = errors.Join
	New    = errors.New
	Unwrap = errors.Unwrap
)

// Kind classifies an error into the small set of outcomes the transport layer
// needs to distinguish. It maps to both an HTTP status and a gRPC code.
type Kind int

// Error kinds. KindInternal is the zero value so an unclassified error is
// treated as a server fault rather than silently reported as success.
const (
	KindInternal Kind = iota
	KindInvalidArgument
	KindNotFound
	KindAlreadyExists
	KindUnauthenticated
	KindPermissionDenied
	KindRateLimited
	KindUnavailable
)

// String returns a stable, machine-friendly name for the kind. Safe to expose:
// it contains no request data.
func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid_argument"
	case KindNotFound:
		return "not_found"
	case KindAlreadyExists:
		return "already_exists"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindPermissionDenied:
		return "permission_denied"
	case KindRateLimited:
		return "rate_limited"
	case KindUnavailable:
		return "unavailable"
	default:
		return "internal"
	}
}

// Error is a classified error. Message is written for the client; op and the
// wrapped err are for logs only and are never exposed by Public.
type Error struct {
	// Kind determines the transport status.
	Kind Kind

	// Field names the offending request field, for the field-level
	// validation messages Phase 5.4 requires. Empty when not applicable.
	Field string

	// Message is safe to return to a client: written by us, never
	// interpolated from user input.
	Message string

	// op describes what the code was doing, for log context.
	op string

	// err is the underlying cause. Never rendered to a client.
	err error
}

// Error implements the error interface. This is the *internal* rendering — it
// deliberately includes the wrapped cause, so it must only reach logs.
func (e *Error) Error() string {
	var s string
	switch {
	case e.op != "" && e.Field != "":
		s = fmt.Sprintf("%s: %s: %s", e.op, e.Field, e.Message)
	case e.op != "":
		s = fmt.Sprintf("%s: %s", e.op, e.Message)
	case e.Field != "":
		s = fmt.Sprintf("%s: %s", e.Field, e.Message)
	default:
		s = e.Message
	}
	if e.err != nil {
		return s + ": " + e.err.Error()
	}
	return s
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.err }

// KindOf reports the Kind of err, walking the wrap chain. Any error not
// produced by this package is KindInternal — an unclassified failure is a bug
// on our side until proven otherwise.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// Public returns a message safe to send to a client. For internal errors it
// returns a fixed string, so no internal detail and no reflected user input can
// escape (Phase 9.2, OWASP).
func Public(err error) string {
	var e *Error
	if errors.As(err, &e) && e.Kind != KindInternal {
		if e.Field != "" {
			return e.Field + ": " + e.Message
		}
		return e.Message
	}
	return "internal server error"
}

// Field returns the offending field name, or "" when the error does not name one.
func FieldOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Field
	}
	return ""
}

// HTTPStatus maps err to an HTTP status code.
func HTTPStatus(err error) int {
	switch KindOf(err) {
	case KindInvalidArgument:
		return http.StatusBadRequest
	case KindNotFound:
		return http.StatusNotFound
	case KindAlreadyExists:
		return http.StatusConflict
	case KindUnauthenticated:
		return http.StatusUnauthorized
	case KindPermissionDenied:
		return http.StatusForbidden
	case KindRateLimited:
		return http.StatusTooManyRequests
	case KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// GRPCCode maps err to a gRPC status code. The gateway derives the HTTP status
// from this, so the two mappings stay consistent by construction.
func GRPCCode(err error) codes.Code {
	switch KindOf(err) {
	case KindInvalidArgument:
		return codes.InvalidArgument
	case KindNotFound:
		return codes.NotFound
	case KindAlreadyExists:
		return codes.AlreadyExists
	case KindUnauthenticated:
		return codes.Unauthenticated
	case KindPermissionDenied:
		return codes.PermissionDenied
	case KindRateLimited:
		return codes.ResourceExhausted
	case KindUnavailable:
		return codes.Unavailable
	default:
		return codes.Internal
	}
}

// GRPCStatus converts err into a gRPC status error carrying only the public
// message. Return this from a gRPC handler; log err separately.
func GRPCStatus(err error) error {
	if err == nil {
		return nil
	}
	return status.Error(GRPCCode(err), Public(err))
}

// --- constructors ---------------------------------------------------------

// Internal wraps an unexpected failure. op describes the operation for logs;
// the cause is preserved for logs and hidden from clients.
func Internal(op string, cause error) *Error {
	return &Error{Kind: KindInternal, Message: "internal server error", op: op, err: cause}
}

// Invalid reports a bad request that is not tied to a single field.
func Invalid(format string, args ...any) *Error {
	return &Error{Kind: KindInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

// InvalidField reports a bad request field. reason must be a fixed string
// written by us — never the submitted value (Phase 9.2: no reflected input).
func InvalidField(field, reason string) *Error {
	return &Error{Kind: KindInvalidArgument, Field: field, Message: reason}
}

// NotFound reports a missing resource.
func NotFound(what string) *Error {
	return &Error{Kind: KindNotFound, Message: what + " not found"}
}

// AlreadyExists reports a uniqueness conflict.
func AlreadyExists(what string) *Error {
	return &Error{Kind: KindAlreadyExists, Message: what + " already exists"}
}

// Unauthenticated reports a missing or invalid session token.
func Unauthenticated(reason string) *Error {
	return &Error{Kind: KindUnauthenticated, Message: reason}
}

// PermissionDenied reports an authenticated caller reaching another user's
// data. Phase 9.2 requires this check at the service layer on every query, so
// the message stays deliberately uninformative about what exists.
func PermissionDenied() *Error {
	return &Error{Kind: KindPermissionDenied, Message: "permission denied"}
}

// RateLimited reports a caller exceeding the per-IP budget (Phase 9.2).
func RateLimited() *Error {
	return &Error{Kind: KindRateLimited, Message: "too many requests"}
}

// Unavailable reports a dependency being down — MongoDB Atlas or GitHub OAuth
// (Phase 12.2 step 3 treats these as wait-it-out, not failover).
func Unavailable(dependency string) *Error {
	return &Error{Kind: KindUnavailable, Message: dependency + " is unavailable"}
}
