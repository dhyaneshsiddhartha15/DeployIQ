package repository

import (
	"context"
	"sort"
	"strings"
	"time"

	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// UpsertUserBySubject creates or refreshes an SSO account.
func (m *Memory) UpsertUserBySubject(_ context.Context, u model.User) (model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, existing := range m.users {
		if existing.Provider == u.Provider && existing.Subject == u.Subject && u.Subject != "" {
			existing.Email, existing.Name = u.Email, u.Name
			m.users[id] = existing
			return existing, nil
		}
	}
	u.ID = newID()
	if u.Plan == "" {
		u.Plan = model.PlanFree
	}
	u.CreatedAt = m.now().UTC().Truncate(time.Millisecond)
	m.users[u.ID] = u
	return u, nil
}

// FindUser looks an account up by email or GitHub login.
func (m *Memory) FindUser(_ context.Context, email, login string) (model.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if (email != "" && strings.EqualFold(u.Email, email)) || (login != "" && strings.EqualFold(u.Login, login)) {
			return u, nil
		}
	}
	return model.User{}, apperrors.NotFound("user")
}

// ListOrgOptimizations filters by organisation.
func (m *Memory) ListOrgOptimizations(_ context.Context, orgID, repo string, limit int) ([]model.Optimization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []model.Optimization{}
	for i := len(m.opts) - 1; i >= 0; i-- {
		o := m.opts[i]
		if o.OrgID != orgID || orgID == "" || (repo != "" && o.RepoName != repo) {
			continue
		}
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CreateOrg stores the organisation and its first admin.
func (m *Memory) CreateOrg(_ context.Context, o model.Organization, creatorID string) (model.Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.orgs {
		if existing.Slug == o.Slug {
			return model.Organization{}, apperrors.AlreadyExists("organization")
		}
	}
	o.ID = newID()
	o.CreatedAt = m.now().UTC().Truncate(time.Millisecond)
	m.orgs[o.ID] = o
	m.memberships[[2]string{o.ID, creatorID}] = model.Membership{OrgID: o.ID, UserID: creatorID, Role: model.RoleAdmin, CreatedAt: o.CreatedAt}
	return o, nil
}

// GetOrgBySlug loads an organisation.
func (m *Memory) GetOrgBySlug(_ context.Context, slug string) (model.Organization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, o := range m.orgs {
		if o.Slug == slug {
			return o, nil
		}
	}
	return model.Organization{}, apperrors.NotFound("organization")
}

// GetOrg loads an organisation by id.
func (m *Memory) GetOrg(_ context.Context, id string) (model.Organization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.orgs[id]
	if !ok {
		return model.Organization{}, apperrors.NotFound("organization")
	}
	return o, nil
}

// UpdateOrgSettings replaces the settings.
func (m *Memory) UpdateOrgSettings(_ context.Context, orgID string, s model.OrgSettings) (model.Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[orgID]
	if !ok {
		return model.Organization{}, apperrors.NotFound("organization")
	}
	o.Settings = s
	m.orgs[orgID] = o
	return o, nil
}

// ListMemberships returns a user's memberships.
func (m *Memory) ListMemberships(_ context.Context, userID string) ([]model.Membership, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []model.Membership
	for k, ms := range m.memberships {
		if k[1] == userID {
			out = append(out, ms)
		}
	}
	return out, nil
}

// GetMembership loads one membership.
func (m *Memory) GetMembership(_ context.Context, orgID, userID string) (model.Membership, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ms, ok := m.memberships[[2]string{orgID, userID}]
	if !ok {
		return model.Membership{}, apperrors.NotFound("membership")
	}
	return ms, nil
}

// UpsertMembership adds or changes a membership.
func (m *Memory) UpsertMembership(_ context.Context, ms model.Membership) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := [2]string{ms.OrgID, ms.UserID}
	if existing, ok := m.memberships[k]; ok {
		ms.CreatedAt = existing.CreatedAt
	} else {
		ms.CreatedAt = m.now().UTC().Truncate(time.Millisecond)
	}
	m.memberships[k] = ms
	return nil
}

// DeleteMembership removes a membership.
func (m *Memory) DeleteMembership(_ context.Context, orgID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.memberships, [2]string{orgID, userID})
	return nil
}

// ListMembers joins memberships with users.
func (m *Memory) ListMembers(_ context.Context, orgID string) ([]model.Member, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []model.Member{}
	for k, ms := range m.memberships {
		if k[0] != orgID {
			continue
		}
		out = append(out, model.Member{User: m.users[k[1]], Role: ms.Role})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].User.Display() < out[j].User.Display() })
	return out, nil
}

// CreateInvitation records an invitation.
func (m *Memory) CreateInvitation(_ context.Context, inv model.Invitation) (model.Invitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.invitations {
		if existing.OrgID == inv.OrgID && existing.Email == inv.Email && existing.Login == inv.Login {
			return model.Invitation{}, apperrors.AlreadyExists("invitation")
		}
	}
	inv.ID = newID()
	inv.CreatedAt = m.now().UTC().Truncate(time.Millisecond)
	m.invitations[inv.ID] = inv
	return inv, nil
}

// ListInvitations returns an organisation's pending invitations.
func (m *Memory) ListInvitations(_ context.Context, orgID string) ([]model.Invitation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []model.Invitation{}
	for _, inv := range m.invitations {
		if inv.OrgID == orgID {
			out = append(out, inv)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// DeleteInvitation withdraws one.
func (m *Memory) DeleteInvitation(_ context.Context, orgID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inv, ok := m.invitations[id]; !ok || inv.OrgID != orgID {
		return apperrors.NotFound("invitation")
	}
	delete(m.invitations, id)
	return nil
}

// ClaimInvitations deletes and returns invitations for this person.
func (m *Memory) ClaimInvitations(_ context.Context, email, login string) ([]model.Invitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Invitation
	for id, inv := range m.invitations {
		if (email != "" && strings.EqualFold(inv.Email, email)) || (login != "" && strings.EqualFold(inv.Login, login)) {
			out = append(out, inv)
			delete(m.invitations, id)
		}
	}
	return out, nil
}
