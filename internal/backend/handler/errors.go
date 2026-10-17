package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
)

// ErrorBody is the JSON shape of every error response on the public API:
//
//	{"error": {"code": "invalid_argument", "message": "must not be empty", "field": "repoName"}}
//
// field is present only for field-level validation errors (Phase 5.4).
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the inner object of ErrorBody.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// WriteError writes err as an ErrorBody from a plain HTTP handler.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	kind := apperrors.KindOf(err)
	if kind == apperrors.KindInternal {
		slog.ErrorContext(r.Context(), "request failed", slog.String("path", r.URL.Path), slog.String("error", err.Error()))
	}
	msg := apperrors.Public(err)
	field := apperrors.FieldOf(err)
	if field != "" {
		// Public() prefixes the field; the body carries it separately.
		var e *apperrors.Error
		if apperrors.As(err, &e) {
			msg = e.Message
		}
	}
	writeJSON(w, apperrors.HTTPStatus(err), ErrorBody{Error: ErrorDetail{Code: kind.String(), Message: msg, Field: field}})
}

// GatewayErrorHandler renders gRPC errors from the REST gateway in the same
// ErrorBody shape.
//
// Two cases need care. Our own validation errors carry a BadRequest detail
// naming the field; that becomes `field`. The gateway's own InvalidArgument
// errors — a body that is not JSON, an unknown field, a wrong type — carry
// decoder text that can quote the request back, so they are replaced with a
// fixed message (Phase 9.2: no reflected input).
func GatewayErrorHandler(ctx context.Context, _ *gwruntime.ServeMux, _ gwruntime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
	st, ok := status.FromError(err)
	if !ok {
		st = status.New(codes.Unknown, "")
	}
	detail := ErrorDetail{Code: codeName(st.Code()), Message: st.Message()}

	switch st.Code() {
	case codes.InvalidArgument:
		detail.Field = apperrors.FieldFromStatus(st)
		if detail.Field == "" && !isOwnMessage(st) {
			detail.Message = "request body or parameters are malformed for this endpoint"
		} else if detail.Field != "" {
			detail.Message = trimFieldPrefix(detail.Field, st.Message())
		}
	case codes.Internal, codes.Unknown, codes.DataLoss:
		detail.Message = "internal server error"
	case codes.NotFound:
		if detail.Message == "" || detail.Message == "Not Found" {
			detail.Message = "not found"
		}
	case codes.Unimplemented:
		detail.Message = "method not allowed"
	}

	httpStatus := gwruntime.HTTPStatusFromCode(st.Code())
	if st.Code() == codes.Unimplemented {
		httpStatus = http.StatusMethodNotAllowed
	}
	if httpStatus >= 500 {
		slog.ErrorContext(ctx, "api error", slog.String("path", r.URL.Path), slog.String("code", st.Code().String()))
	}
	writeJSON(w, httpStatus, ErrorBody{Error: detail})
}

// isOwnMessage reports whether an InvalidArgument status came from our
// service layer (which always attaches details or uses a fixed message
// constructed by apperrors.Invalid) rather than the gateway's decoder. Our
// non-field invalid errors are marked with a BadRequest detail too, so in
// practice: has details → ours.
func isOwnMessage(st *status.Status) bool { return len(st.Details()) > 0 }

func trimFieldPrefix(field, msg string) string {
	p := field + ": "
	if len(msg) > len(p) && msg[:len(p)] == p {
		return msg[len(p):]
	}
	return msg
}

func codeName(c codes.Code) string {
	switch c {
	case codes.InvalidArgument:
		return apperrors.KindInvalidArgument.String()
	case codes.NotFound:
		return apperrors.KindNotFound.String()
	case codes.AlreadyExists:
		return apperrors.KindAlreadyExists.String()
	case codes.Unauthenticated:
		return apperrors.KindUnauthenticated.String()
	case codes.PermissionDenied:
		return apperrors.KindPermissionDenied.String()
	case codes.ResourceExhausted:
		return apperrors.KindRateLimited.String()
	case codes.Unavailable:
		return apperrors.KindUnavailable.String()
	case codes.Unimplemented:
		return "method_not_allowed"
	}
	return apperrors.KindInternal.String()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
