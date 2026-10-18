package service

import (
	"context"
	"log/slog"
	"strings"

	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// Login providers.
const (
	ProviderGitHub = "github"
	ProviderOIDC   = "oidc"
)

// OIDCIdentity is what a successful OpenID Connect login proves.
type OIDCIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Groups        []string
}

// OIDCProvider is the company identity provider (Okta, Entra ID, Google
// Workspace, Keycloak, Auth0...), implemented by internal/backend/oidc.
type OIDCProvider interface {
	// AuthorizeURL is the provider's sign-in page for this attempt.
	AuthorizeURL(state, nonce, redirectURI, codeChallenge string) string

	// Exchange redeems the code and returns the validated identity. nonce
	// must match the ID token's nonce claim.
	Exchange(ctx context.Context, code, redirectURI, codeVerifier, nonce string) (OIDCIdentity, error)
}

// SSOPolicy decides who may sign in with SSO and where they land.
type SSOPolicy struct {
	// AllowedDomains restricts sign-in to these email domains (empty: any
	// identity the provider vouches for).
	AllowedDomains []string

	// AutoOrg is an organisation every SSO user joins as a member.
	AutoOrg string

	// AdminGroups are provider groups whose members join AutoOrg as admins.
	AdminGroups []string
}

// WithOrgs makes logins claim invitations and apply SSO membership.
func (a *Auth) WithOrgs(orgs *Orgs) *Auth { a.orgs = orgs; return a }

// WithOIDC enables SSO.
func (a *Auth) WithOIDC(p OIDCProvider, policy SSOPolicy) *Auth {
	a.oidc, a.ssoPolicy = p, policy
	return a
}

// OIDCEnabled reports whether SSO is configured.
func (a *Auth) OIDCEnabled() bool { return a.oidc != nil }

// OIDCAuthorizeURL delegates to the provider.
func (a *Auth) OIDCAuthorizeURL(state, nonce, redirectURI, challenge string) string {
	return a.oidc.AuthorizeURL(state, nonce, redirectURI, challenge)
}

// CompleteOIDCLogin redeems an SSO login, applies the policy, creates or
// refreshes the account and issues a session.
func (a *Auth) CompleteOIDCLogin(ctx context.Context, code, redirectURI, verifier, nonce string) (model.User, string, error) {
	if a.oidc == nil {
		return model.User{}, "", apperrors.Invalid("single sign-on is not configured")
	}
	if strings.TrimSpace(code) == "" {
		return model.User{}, "", apperrors.InvalidField("code", "is required")
	}
	id, err := a.oidc.Exchange(ctx, code, redirectURI, verifier, nonce)
	if err != nil {
		if apperrors.KindOf(err) != apperrors.KindInternal {
			return model.User{}, "", err
		}
		slog.ErrorContext(ctx, "oidc exchange failed", slog.String("error", err.Error()))
		return model.User{}, "", apperrors.Unavailable("the identity provider")
	}
	if id.Subject == "" {
		return model.User{}, "", apperrors.Unavailable("the identity provider")
	}
	if len(a.ssoPolicy.AllowedDomains) > 0 && !domainAllowed(id, a.ssoPolicy.AllowedDomains) {
		return model.User{}, "", apperrors.PermissionDenied()
	}

	u, err := a.users.UpsertUserBySubject(ctx, model.User{
		Provider: ProviderOIDC, Subject: id.Subject, Email: strings.ToLower(id.Email), Name: id.Name, Plan: model.PlanFree,
	})
	if err != nil {
		return model.User{}, "", wrapStore("upsert sso user", err)
	}
	a.joinOrgs(ctx, u, a.ssoPolicy.AutoOrg, intersects(id.Groups, a.ssoPolicy.AdminGroups))
	token, _ := a.IssueSession(u.ID)
	return u, token, nil
}

// joinOrgs claims invitations and applies SSO membership. A failure here must
// not lock the user out, so it is logged rather than returned.
func (a *Auth) joinOrgs(ctx context.Context, u model.User, autoOrg string, admin bool) {
	if a.orgs == nil {
		return
	}
	if err := a.orgs.JoinOnLogin(ctx, u, autoOrg, admin); err != nil {
		slog.ErrorContext(ctx, "applying organization membership at login", slog.String("error", err.Error()))
	}
}

// domainAllowed requires a verified email in an allowed domain: an
// unverified address proves nothing about who the user works for.
func domainAllowed(id OIDCIdentity, domains []string) bool {
	if !id.EmailVerified {
		return false
	}
	_, domain, ok := strings.Cut(strings.ToLower(id.Email), "@")
	if !ok {
		return false
	}
	for _, d := range domains {
		if strings.EqualFold(strings.TrimSpace(d), domain) {
			return true
		}
	}
	return false
}

func intersects(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range b {
		set[strings.ToLower(x)] = true
	}
	for _, x := range a {
		if set[strings.ToLower(x)] {
			return true
		}
	}
	return false
}
