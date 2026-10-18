package handler

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/dhyaneshsiddhartha15/DeployIQ/api/v1"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/service"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// Orgs serves OrgsService. Authorization is entirely in service.Orgs.
type Orgs struct {
	apiv1.UnimplementedOrgsServiceServer
	svc *service.Orgs
}

// NewOrgs returns the handler.
func NewOrgs(svc *service.Orgs) *Orgs { return &Orgs{svc: svc} }

// CreateOrg handles POST /api/v1/orgs.
func (h *Orgs) CreateOrg(ctx context.Context, req *apiv1.CreateOrgRequest) (*apiv1.Org, error) {
	o, err := h.svc.Create(ctx, req.GetSlug(), req.GetName())
	if err != nil {
		return nil, respondErr(ctx, "create org", err)
	}
	return orgProto(o, model.RoleAdmin), nil
}

// ListMyOrgs handles GET /api/v1/orgs.
func (h *Orgs) ListMyOrgs(ctx context.Context, _ *apiv1.Empty) (*apiv1.ListOrgsResponse, error) {
	list, err := h.svc.ListMine(ctx)
	if err != nil {
		return nil, respondErr(ctx, "list orgs", err)
	}
	out := &apiv1.ListOrgsResponse{Orgs: make([]*apiv1.Org, 0, len(list))}
	for _, o := range list {
		out.Orgs = append(out.Orgs, orgProto(o.Org, o.Role))
	}
	return out, nil
}

// GetOrgSummary handles GET /api/v1/orgs/{slug}/summary.
func (h *Orgs) GetOrgSummary(ctx context.Context, req *apiv1.OrgRef) (*apiv1.OrgSummary, error) {
	s, err := h.svc.Summary(ctx, req.GetSlug())
	if err != nil {
		return nil, respondErr(ctx, "org summary", err)
	}
	out := &apiv1.OrgSummary{
		Org: orgProto(s.Org, ""), Runs: int32(s.Runs), CurrentMb: s.CurrentMB, OptimizedMb: s.OptimizedMB,
		SavedMb: s.SavedMB, AvgSavedPercent: s.AvgSavedPercent, MonthlySavedUsd: s.MonthlySavedUSD, Members: int32(s.Members),
		Repos: make([]*apiv1.RepoSummary, 0, len(s.Repos)),
	}
	for _, r := range s.Repos {
		out.Repos = append(out.Repos, &apiv1.RepoSummary{
			RepoName: r.RepoName, Latest: toProto(r.Latest), Runs: int32(r.Runs), ReportedBy: r.ReportedBy, MonthlySavedUsd: r.MonthlySavedUSD,
		})
	}
	return out, nil
}

// ListOrgOptimizations handles GET /api/v1/orgs/{slug}/optimizations.
func (h *Orgs) ListOrgOptimizations(ctx context.Context, req *apiv1.ListOrgOptimizationsRequest) (*apiv1.ListOptimizationsResponse, error) {
	list, err := h.svc.Optimizations(ctx, req.GetSlug(), req.GetRepo(), int(req.GetLimit()))
	if err != nil {
		return nil, respondErr(ctx, "list org optimizations", err)
	}
	out := &apiv1.ListOptimizationsResponse{Optimizations: make([]*apiv1.Optimization, 0, len(list))}
	for _, o := range list {
		out.Optimizations = append(out.Optimizations, toProto(o))
	}
	return out, nil
}

// ListMembers handles GET /api/v1/orgs/{slug}/members.
func (h *Orgs) ListMembers(ctx context.Context, req *apiv1.OrgRef) (*apiv1.ListMembersResponse, error) {
	ms, invs, err := h.svc.Members(ctx, req.GetSlug())
	if err != nil {
		return nil, respondErr(ctx, "list members", err)
	}
	out := &apiv1.ListMembersResponse{Members: make([]*apiv1.Member, 0, len(ms)), Invitations: make([]*apiv1.Invitation, 0, len(invs))}
	for _, m := range ms {
		out.Members = append(out.Members, memberProto(m))
	}
	for _, i := range invs {
		out.Invitations = append(out.Invitations, invitationProto(i))
	}
	return out, nil
}

// InviteMember handles POST /api/v1/orgs/{slug}/members.
func (h *Orgs) InviteMember(ctx context.Context, req *apiv1.InviteMemberRequest) (*apiv1.InviteMemberResponse, error) {
	m, inv, err := h.svc.Invite(ctx, req.GetSlug(), req.GetEmail(), req.GetLogin(), model.Role(req.GetRole()))
	if err != nil {
		return nil, respondErr(ctx, "invite", err)
	}
	out := &apiv1.InviteMemberResponse{}
	if m != nil {
		out.Member = memberProto(*m)
	}
	if inv != nil {
		out.Invitation = invitationProto(*inv)
	}
	return out, nil
}

// UpdateMember handles PATCH /api/v1/orgs/{slug}/members/{userId}.
func (h *Orgs) UpdateMember(ctx context.Context, req *apiv1.UpdateMemberRequest) (*apiv1.Member, error) {
	m, err := h.svc.SetRole(ctx, req.GetSlug(), req.GetUserId(), model.Role(req.GetRole()))
	if err != nil {
		return nil, respondErr(ctx, "update member", err)
	}
	return memberProto(m), nil
}

// RemoveMember handles DELETE /api/v1/orgs/{slug}/members/{userId}.
func (h *Orgs) RemoveMember(ctx context.Context, req *apiv1.MemberRef) (*apiv1.Empty, error) {
	if err := h.svc.Remove(ctx, req.GetSlug(), req.GetUserId()); err != nil {
		return nil, respondErr(ctx, "remove member", err)
	}
	return &apiv1.Empty{}, nil
}

// DeleteInvitation handles DELETE /api/v1/orgs/{slug}/invitations/{id}.
func (h *Orgs) DeleteInvitation(ctx context.Context, req *apiv1.InvitationRef) (*apiv1.Empty, error) {
	if err := h.svc.DeleteInvitation(ctx, req.GetSlug(), req.GetId()); err != nil {
		return nil, respondErr(ctx, "delete invitation", err)
	}
	return &apiv1.Empty{}, nil
}

// UpdateSettings handles PUT /api/v1/orgs/{slug}/settings.
func (h *Orgs) UpdateSettings(ctx context.Context, req *apiv1.UpdateSettingsRequest) (*apiv1.Org, error) {
	s := req.GetSettings()
	o, err := h.svc.UpdateSettings(ctx, req.GetSlug(), model.OrgSettings{
		StoragePerGBMonthUSD: s.GetStoragePerGbMonthUsd(), TransferPerGBUSD: s.GetTransferPerGbUsd(),
		PullsPerDay: s.GetPullsPerDay(), VersionsKept: s.GetVersionsKept(),
	})
	if err != nil {
		return nil, respondErr(ctx, "update settings", err)
	}
	return orgProto(o, model.RoleAdmin), nil
}

func orgProto(o model.Organization, role model.Role) *apiv1.Org {
	return &apiv1.Org{
		Id: o.ID, Slug: o.Slug, Name: o.Name, CreatedAt: timestamppb.New(o.CreatedAt), Role: string(role),
		Settings: &apiv1.OrgSettings{
			StoragePerGbMonthUsd: o.Settings.StoragePerGBMonthUSD, TransferPerGbUsd: o.Settings.TransferPerGBUSD,
			PullsPerDay: o.Settings.PullsPerDay, VersionsKept: o.Settings.VersionsKept,
		},
	}
}

func userProto(u model.User) *apiv1.User {
	return &apiv1.User{
		Id: u.ID, GithubId: u.GitHubID, Login: u.Login, Email: u.Email, Plan: string(u.Plan),
		CreatedAt: timestamppb.New(u.CreatedAt), Name: u.Name, Provider: providerOf(u),
	}
}

func providerOf(u model.User) string {
	if u.Provider != "" {
		return u.Provider
	}
	return service.ProviderGitHub
}

func memberProto(m model.Member) *apiv1.Member {
	return &apiv1.Member{User: userProto(m.User), Role: string(m.Role)}
}

func invitationProto(i model.Invitation) *apiv1.Invitation {
	return &apiv1.Invitation{Id: i.ID, Email: i.Email, Login: i.Login, Role: string(i.Role), CreatedAt: timestamppb.New(i.CreatedAt)}
}
