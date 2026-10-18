package service

import (
	"context"
	"math"
	"net/mail"
	"regexp"
	"sort"
	"strings"

	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

var (
	slugRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)
	loginRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
)

// Orgs is the business logic behind /api/v1/orgs. Every method resolves the
// caller from the verified session and checks their membership itself: a
// handler cannot forget to (Phase 9.2).
type Orgs struct {
	orgs  OrgStore
	users UserStore
	opts  OptimizationStore
}

// NewOrgs returns the service.
func NewOrgs(orgs OrgStore, users UserStore, opts OptimizationStore) *Orgs {
	return &Orgs{orgs: orgs, users: users, opts: opts}
}

// OrgWithRole is an organisation and the caller's role in it.
type OrgWithRole struct {
	Org  model.Organization
	Role model.Role
}

// Create makes an organisation with the caller as its admin.
func (s *Orgs) Create(ctx context.Context, slug, name string) (model.Organization, error) {
	uid, err := caller(ctx)
	if err != nil {
		return model.Organization{}, err
	}
	slug = strings.TrimSpace(slug)
	name = strings.TrimSpace(name)
	if !slugRe.MatchString(slug) {
		return model.Organization{}, apperrors.InvalidField("slug", "must be 3-40 lowercase letters, digits or hyphens")
	}
	if name == "" || len(name) > 100 {
		return model.Organization{}, apperrors.InvalidField("name", "must be 1-100 characters")
	}
	o, err := s.orgs.CreateOrg(ctx, model.Organization{Slug: slug, Name: name, Settings: model.DefaultOrgSettings()}, uid)
	if err != nil {
		return model.Organization{}, wrapStore("create org", err)
	}
	return o, nil
}

// ListMine returns the caller's organisations.
func (s *Orgs) ListMine(ctx context.Context) ([]OrgWithRole, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	ms, err := s.orgs.ListMemberships(ctx, uid)
	if err != nil {
		return nil, wrapStore("list memberships", err)
	}
	out := make([]OrgWithRole, 0, len(ms))
	for _, m := range ms {
		o, err := s.orgs.GetOrg(ctx, m.OrgID)
		if err != nil {
			continue // a membership of a deleted org is not worth failing the list
		}
		out = append(out, OrgWithRole{Org: o, Role: m.Role})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Org.Slug < out[j].Org.Slug })
	return out, nil
}

// access resolves slug and checks the caller's role. A non-member gets the
// same NotFound as for an organisation that does not exist, so membership
// probing reveals nothing.
func (s *Orgs) access(ctx context.Context, slug string, need model.Role) (model.Organization, model.Membership, error) {
	uid, err := caller(ctx)
	if err != nil {
		return model.Organization{}, model.Membership{}, err
	}
	o, err := s.orgs.GetOrgBySlug(ctx, strings.TrimSpace(slug))
	if err != nil {
		return model.Organization{}, model.Membership{}, apperrors.NotFound("organization")
	}
	m, err := s.orgs.GetMembership(ctx, o.ID, uid)
	if err != nil {
		return model.Organization{}, model.Membership{}, apperrors.NotFound("organization")
	}
	if need == model.RoleAdmin && m.Role != model.RoleAdmin {
		return model.Organization{}, model.Membership{}, apperrors.PermissionDenied()
	}
	return o, m, nil
}

// Optimizations lists reports sent into the organisation. Members only.
func (s *Orgs) Optimizations(ctx context.Context, slug, repo string, limit int) ([]model.Optimization, error) {
	o, _, err := s.access(ctx, slug, model.RoleMember)
	if err != nil {
		return nil, err
	}
	if repo = strings.TrimSpace(repo); repo != "" && !validRepoName(repo) {
		return nil, apperrors.InvalidField("repo", "must be a repository name such as owner/name")
	}
	limit = clampLimit(limit)
	list, err := s.opts.ListOrgOptimizations(ctx, o.ID, repo, limit)
	if err != nil {
		return nil, wrapStore("list org optimizations", err)
	}
	if list == nil {
		list = []model.Optimization{}
	}
	return list, nil
}

// Summary builds the organisation dashboard. Members only.
func (s *Orgs) Summary(ctx context.Context, slug string) (model.OrgSummary, error) {
	o, _, err := s.access(ctx, slug, model.RoleMember)
	if err != nil {
		return model.OrgSummary{}, err
	}
	list, err := s.opts.ListOrgOptimizations(ctx, o.ID, "", MaxListLimit*10)
	if err != nil {
		return model.OrgSummary{}, wrapStore("summary", err)
	}
	members, err := s.orgs.ListMembers(ctx, o.ID)
	if err != nil {
		return model.OrgSummary{}, wrapStore("summary members", err)
	}
	return Summarize(o, list, len(members)), nil
}

// Summarize groups records (newest first) into the dashboard view: one row
// per repository from its latest run.
func Summarize(o model.Organization, list []model.Optimization, members int) model.OrgSummary {
	sum := model.OrgSummary{Org: o, Runs: len(list), Members: members, Repos: []model.RepoSummary{}}
	idx := map[string]int{}
	for _, opt := range list {
		i, ok := idx[opt.RepoName]
		if !ok {
			idx[opt.RepoName] = len(sum.Repos)
			sum.Repos = append(sum.Repos, model.RepoSummary{RepoName: opt.RepoName, Latest: opt, ReportedBy: opt.ReportedBy})
			i = len(sum.Repos) - 1
		}
		sum.Repos[i].Runs++
	}
	pct := 0.0
	for i := range sum.Repos {
		r := &sum.Repos[i]
		saved := math.Max(0, float64(r.Latest.OriginalEstSizeMB-r.Latest.OptimizedEstSizeMB))
		r.MonthlySavedUSD = round2(o.Settings.MonthlySavingUSD(saved))
		sum.CurrentMB += float64(r.Latest.OriginalEstSizeMB)
		sum.OptimizedMB += float64(r.Latest.OptimizedEstSizeMB)
		sum.SavedMB += saved
		sum.MonthlySavedUSD += r.MonthlySavedUSD
		pct += r.Latest.SavingsPercent()
	}
	if len(sum.Repos) > 0 {
		sum.AvgSavedPercent = pct / float64(len(sum.Repos))
	}
	sum.MonthlySavedUSD = round2(sum.MonthlySavedUSD)
	sort.SliceStable(sum.Repos, func(i, j int) bool { return sum.Repos[i].MonthlySavedUSD > sum.Repos[j].MonthlySavedUSD })
	return sum
}

// Members returns members and pending invitations. Members only.
func (s *Orgs) Members(ctx context.Context, slug string) ([]model.Member, []model.Invitation, error) {
	o, _, err := s.access(ctx, slug, model.RoleMember)
	if err != nil {
		return nil, nil, err
	}
	ms, err := s.orgs.ListMembers(ctx, o.ID)
	if err != nil {
		return nil, nil, wrapStore("list members", err)
	}
	invs, err := s.orgs.ListInvitations(ctx, o.ID)
	if err != nil {
		return nil, nil, wrapStore("list invitations", err)
	}
	return ms, invs, nil
}

// Invite adds a user now if they have an account, else records an
// invitation. Admins only.
func (s *Orgs) Invite(ctx context.Context, slug, email, login string, role model.Role) (*model.Member, *model.Invitation, error) {
	o, me, err := s.access(ctx, slug, model.RoleAdmin)
	if err != nil {
		return nil, nil, err
	}
	email, login = strings.ToLower(strings.TrimSpace(email)), strings.TrimSpace(login)
	switch {
	case (email == "") == (login == ""):
		return nil, nil, apperrors.InvalidField("email", "give exactly one of email or login")
	case email != "" && !validEmail(email):
		return nil, nil, apperrors.InvalidField("email", "is not a valid email address")
	case login != "" && !loginRe.MatchString(login):
		return nil, nil, apperrors.InvalidField("login", "is not a valid GitHub username")
	}
	if role == "" {
		role = model.RoleMember
	}
	if !role.Valid() {
		return nil, nil, apperrors.InvalidField("role", "must be admin or member")
	}

	if u, err := s.users.FindUser(ctx, email, login); err == nil {
		if err := s.orgs.UpsertMembership(ctx, model.Membership{OrgID: o.ID, UserID: u.ID, Role: role}); err != nil {
			return nil, nil, wrapStore("add member", err)
		}
		return &model.Member{User: u, Role: role}, nil, nil
	}
	inv, err := s.orgs.CreateInvitation(ctx, model.Invitation{OrgID: o.ID, Email: email, Login: login, Role: role, InvitedBy: me.UserID})
	if err != nil {
		return nil, nil, wrapStore("invite", err)
	}
	return nil, &inv, nil
}

// SetRole changes a member's role. Admins only; never leaves zero admins.
func (s *Orgs) SetRole(ctx context.Context, slug, userID string, role model.Role) (model.Member, error) {
	o, _, err := s.access(ctx, slug, model.RoleAdmin)
	if err != nil {
		return model.Member{}, err
	}
	if !role.Valid() {
		return model.Member{}, apperrors.InvalidField("role", "must be admin or member")
	}
	m, err := s.orgs.GetMembership(ctx, o.ID, userID)
	if err != nil {
		return model.Member{}, apperrors.NotFound("member")
	}
	if m.Role == model.RoleAdmin && role != model.RoleAdmin {
		if err := s.keepAnAdmin(ctx, o.ID); err != nil {
			return model.Member{}, err
		}
	}
	m.Role = role
	if err := s.orgs.UpsertMembership(ctx, m); err != nil {
		return model.Member{}, wrapStore("set role", err)
	}
	u, _ := s.users.GetUser(ctx, userID)
	return model.Member{User: u, Role: role}, nil
}

// Remove removes a member. Admins may remove anyone; members may remove
// themselves (leave). The last admin cannot leave.
func (s *Orgs) Remove(ctx context.Context, slug, userID string) error {
	uid, err := caller(ctx)
	if err != nil {
		return err
	}
	need := model.RoleAdmin
	if userID == uid {
		need = model.RoleMember
	}
	o, _, err := s.access(ctx, slug, need)
	if err != nil {
		return err
	}
	m, err := s.orgs.GetMembership(ctx, o.ID, userID)
	if err != nil {
		return apperrors.NotFound("member")
	}
	if m.Role == model.RoleAdmin {
		if err := s.keepAnAdmin(ctx, o.ID); err != nil {
			return err
		}
	}
	return wrapNil(s.orgs.DeleteMembership(ctx, o.ID, userID), "remove member")
}

func (s *Orgs) keepAnAdmin(ctx context.Context, orgID string) error {
	ms, err := s.orgs.ListMembers(ctx, orgID)
	if err != nil {
		return wrapStore("count admins", err)
	}
	admins := 0
	for _, m := range ms {
		if m.Role == model.RoleAdmin {
			admins++
		}
	}
	if admins <= 1 {
		return apperrors.Invalid("an organization needs at least one admin; promote someone else first")
	}
	return nil
}

// DeleteInvitation withdraws an invitation. Admins only.
func (s *Orgs) DeleteInvitation(ctx context.Context, slug, id string) error {
	o, _, err := s.access(ctx, slug, model.RoleAdmin)
	if err != nil {
		return err
	}
	return wrapNil(s.orgs.DeleteInvitation(ctx, o.ID, id), "delete invitation")
}

// UpdateSettings changes the cost parameters. Admins only.
func (s *Orgs) UpdateSettings(ctx context.Context, slug string, st model.OrgSettings) (model.Organization, error) {
	o, _, err := s.access(ctx, slug, model.RoleAdmin)
	if err != nil {
		return model.Organization{}, err
	}
	for field, v := range map[string]float64{
		"storagePerGBMonthUSD": st.StoragePerGBMonthUSD, "transferPerGBUSD": st.TransferPerGBUSD,
		"pullsPerDay": st.PullsPerDay, "versionsKept": st.VersionsKept,
	} {
		if v < 0 || v > 1e6 || math.IsNaN(v) {
			return model.Organization{}, apperrors.InvalidField(field, "must be between 0 and 1000000")
		}
	}
	out, err := s.orgs.UpdateOrgSettings(ctx, o.ID, st)
	if err != nil {
		return model.Organization{}, wrapStore("update settings", err)
	}
	return out, nil
}

// ResolveForReport returns the organisation a report may be sent into: the
// caller must be a member. Used by Optimizations.Create.
func (s *Orgs) ResolveForReport(ctx context.Context, slug string) (model.Organization, error) {
	o, _, err := s.access(ctx, slug, model.RoleMember)
	if apperrors.KindOf(err) == apperrors.KindNotFound {
		return model.Organization{}, apperrors.InvalidField("org", "is not an organization you belong to")
	}
	return o, err
}

// JoinOnLogin claims invitations addressed to the user and applies automatic
// SSO membership. Called after every successful login; failures are logged by
// the caller and never block the login itself.
func (s *Orgs) JoinOnLogin(ctx context.Context, u model.User, autoOrg string, admin bool) error {
	invs, err := s.orgs.ClaimInvitations(ctx, strings.ToLower(u.Email), u.Login)
	if err != nil {
		return err
	}
	for _, inv := range invs {
		if err := s.orgs.UpsertMembership(ctx, model.Membership{OrgID: inv.OrgID, UserID: u.ID, Role: inv.Role}); err != nil {
			return err
		}
	}
	if autoOrg == "" {
		return nil
	}
	o, err := s.orgs.GetOrgBySlug(ctx, autoOrg)
	if apperrors.KindOf(err) == apperrors.KindNotFound {
		// The configured SSO organisation is created on first use.
		o, err = s.orgs.CreateOrg(ctx, model.Organization{Slug: autoOrg, Name: autoOrg, Settings: model.DefaultOrgSettings()}, u.ID)
		return err
	}
	if err != nil {
		return err
	}
	role := model.RoleMember
	if admin {
		role = model.RoleAdmin
	}
	existing, err := s.orgs.GetMembership(ctx, o.ID, u.ID)
	if err == nil && (existing.Role == role || !admin) {
		return nil // never demote an admin the org promoted by hand
	}
	return s.orgs.UpsertMembership(ctx, model.Membership{OrgID: o.ID, UserID: u.ID, Role: role})
}

func caller(ctx context.Context) (string, error) {
	uid := UserID(ctx)
	if uid == "" {
		return "", apperrors.Unauthenticated("authentication required")
	}
	return uid, nil
}

func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultListLimit
	case limit > MaxListLimit:
		return MaxListLimit
	}
	return limit
}

func validEmail(e string) bool {
	a, err := mail.ParseAddress(e)
	return err == nil && a.Address == e && strings.Contains(e, "@")
}

func wrapNil(err error, op string) error {
	if err == nil {
		return nil
	}
	return wrapStore(op, err)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
