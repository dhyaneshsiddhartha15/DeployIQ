package errors

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPublicNeverLeaksInternalDetail(t *testing.T) {
	// The property Phase 5.4 and 9.2 depend on: an internal error's cause
	// must not reach the client, however deeply it is wrapped.
	cause := New("dial mongodb://user:hunter2@cluster0/admin: connection refused")
	err := fmt.Errorf("handling report: %w", Internal("store optimization", cause))

	got := Public(err)
	if got != "internal server error" {
		t.Errorf("Public = %q, want a fixed generic message", got)
	}
	if strings.Contains(got, "hunter2") || strings.Contains(got, "mongodb") {
		t.Fatalf("Public leaked internal detail: %q", got)
	}
	// The full detail must still be available for logs.
	if !strings.Contains(err.Error(), "hunter2") {
		t.Error("Error() lost the cause; logs would be useless")
	}
}

func TestUnclassifiedErrorIsTreatedAsInternal(t *testing.T) {
	err := New("some third-party failure")
	if k := KindOf(err); k != KindInternal {
		t.Errorf("KindOf = %v, want KindInternal", k)
	}
	if s := HTTPStatus(err); s != http.StatusInternalServerError {
		t.Errorf("HTTPStatus = %d, want 500", s)
	}
	if Public(err) != "internal server error" {
		t.Errorf("Public = %q, want the generic message", Public(err))
	}
}

func TestInvalidFieldSurfacesFieldLevelMessage(t *testing.T) {
	// Phase 5.4: malformed input returns 400 with a specific field-level
	// message, never a generic 500.
	err := fmt.Errorf("validating report: %w", InvalidField("repoName", "must not be empty"))

	if s := HTTPStatus(err); s != http.StatusBadRequest {
		t.Errorf("HTTPStatus = %d, want 400", s)
	}
	if got, want := Public(err), "repoName: must not be empty"; got != want {
		t.Errorf("Public = %q, want %q", got, want)
	}
	if got := FieldOf(err); got != "repoName" {
		t.Errorf("FieldOf = %q, want repoName", got)
	}
}

func TestKindMapsToHTTPAndGRPCConsistently(t *testing.T) {
	cases := []struct {
		err  error
		http int
		code codes.Code
	}{
		{Invalid("bad body"), http.StatusBadRequest, codes.InvalidArgument},
		{NotFound("optimization"), http.StatusNotFound, codes.NotFound},
		{AlreadyExists("user"), http.StatusConflict, codes.AlreadyExists},
		{Unauthenticated("missing session token"), http.StatusUnauthorized, codes.Unauthenticated},
		{PermissionDenied(), http.StatusForbidden, codes.PermissionDenied},
		{RateLimited(), http.StatusTooManyRequests, codes.ResourceExhausted},
		{Unavailable("mongodb"), http.StatusServiceUnavailable, codes.Unavailable},
		{Internal("op", New("boom")), http.StatusInternalServerError, codes.Internal},
	}
	for _, c := range cases {
		if got := HTTPStatus(c.err); got != c.http {
			t.Errorf("HTTPStatus(%v) = %d, want %d", c.err, got, c.http)
		}
		if got := GRPCCode(c.err); got != c.code {
			t.Errorf("GRPCCode(%v) = %v, want %v", c.err, got, c.code)
		}
	}
}

func TestGRPCStatusCarriesOnlyPublicMessage(t *testing.T) {
	err := GRPCStatus(Internal("store", New("secret connection string")))

	st, ok := status.FromError(err)
	if !ok {
		t.Fatal("GRPCStatus did not produce a gRPC status error")
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal", st.Code())
	}
	if strings.Contains(st.Message(), "secret") {
		t.Fatalf("gRPC status leaked internal detail: %q", st.Message())
	}

	if GRPCStatus(nil) != nil {
		t.Error("GRPCStatus(nil) must stay nil")
	}
}

func TestIsMatchesThroughWrapping(t *testing.T) {
	sentinel := NotFound("user")
	wrapped := fmt.Errorf("service layer: %w", sentinel)
	if !Is(wrapped, sentinel) {
		t.Error("Is failed to match through a wrap")
	}
}
