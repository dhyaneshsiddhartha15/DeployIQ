package handler

import (
	"context"
	"log/slog"

	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/dhyaneshsiddhartha15/DeployIQ/api/v1"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/service"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// Optimizations serves OptimizationsService: parse, call one service method,
// respond. No business rule lives here.
type Optimizations struct {
	apiv1.UnimplementedOptimizationsServiceServer
	svc *service.Optimizations
}

// NewOptimizations returns the handler.
func NewOptimizations(svc *service.Optimizations) *Optimizations { return &Optimizations{svc: svc} }

// CreateOptimization handles POST /api/v1/optimizations.
func (h *Optimizations) CreateOptimization(ctx context.Context, req *apiv1.CreateOptimizationRequest) (*apiv1.Optimization, error) {
	o, err := h.svc.Create(ctx, model.OptimizationReport{
		RepoName:           req.GetRepoName(),
		DetectedStack:      req.GetDetectedStack(),
		OriginalEstSizeMB:  int64(req.GetOriginalEstSizeMb()),
		OptimizedEstSizeMB: int64(req.GetOptimizedEstSizeMb()),
		RulesApplied:       req.GetRulesApplied(),
		Org:                req.GetOrg(),
	})
	if err != nil {
		return nil, respondErr(ctx, "create optimization", err)
	}
	return toProto(o), nil
}

// ListOptimizations handles GET /api/v1/optimizations?repo=&limit=.
func (h *Optimizations) ListOptimizations(ctx context.Context, req *apiv1.ListOptimizationsRequest) (*apiv1.ListOptimizationsResponse, error) {
	list, err := h.svc.List(ctx, req.GetRepo(), int(req.GetLimit()))
	if err != nil {
		return nil, respondErr(ctx, "list optimizations", err)
	}
	out := &apiv1.ListOptimizationsResponse{Optimizations: make([]*apiv1.Optimization, 0, len(list))}
	for _, o := range list {
		out.Optimizations = append(out.Optimizations, toProto(o))
	}
	return out, nil
}

// Users serves UsersService.
type Users struct {
	apiv1.UnimplementedUsersServiceServer
	svc *service.Users
}

// NewUsers returns the handler.
func NewUsers(svc *service.Users) *Users { return &Users{svc: svc} }

// GetMe handles GET /api/v1/me.
func (h *Users) GetMe(ctx context.Context, _ *apiv1.Empty) (*apiv1.User, error) {
	u, err := h.svc.Me(ctx)
	if err != nil {
		return nil, respondErr(ctx, "get me", err)
	}
	return userProto(u), nil
}

func toProto(o model.Optimization) *apiv1.Optimization {
	return &apiv1.Optimization{
		Id:                 o.ID,
		RepoName:           o.RepoName,
		DetectedStack:      o.DetectedStack,
		OriginalEstSizeMb:  int32(o.OriginalEstSizeMB),
		OptimizedEstSizeMb: int32(o.OptimizedEstSizeMB),
		RulesApplied:       nonNil(o.RulesApplied),
		CreatedAt:          timestamppb.New(o.CreatedAt),
		SavingsPercent:     o.SavingsPercent(),
		ReportedBy:         o.ReportedBy,
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// respondErr logs internal failures with context and converts err into the
// public gRPC status. Client errors are logged by the logging interceptor at
// warn; only our own faults are logged here, at error, with the cause.
func respondErr(ctx context.Context, op string, err error) error {
	if apperrors.KindOf(err) == apperrors.KindInternal {
		slog.ErrorContext(ctx, op+" failed", slog.String("error", err.Error()))
	}
	return apperrors.GRPCStatus(err)
}
