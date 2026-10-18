// Package oidc is the OpenID Connect client behind service.OIDCProvider: one
// implementation for Okta, Microsoft Entra ID, Google Workspace, Keycloak,
// Auth0 and any other standards-compliant provider.
//
// Authorization code flow with PKCE (RFC 7636). The ID token is received
// directly from the token endpoint over TLS, authenticated with the client
// secret; OpenID Connect Core 1.0 §3.1.3.7 allows TLS server validation in
// place of checking the token's signature in exactly that case. Every claim
// check the specification requires — issuer, audience, expiry, nonce — is
// still made.
package oidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/service"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
)

// Client implements service.OIDCProvider.
type Client struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	Scopes       []string

	// GroupsClaim names the claim carrying group membership ("groups" for
	// Okta and Keycloak; Entra ID emits "groups" with object ids).
	GroupsClaim string

	HTTP *http.Client
	Now  func() time.Time

	mu        sync.Mutex
	discovery *metadata
}

var _ service.OIDCProvider = (*Client)(nil)

type metadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

// New returns a client. Discovery happens on first use, not at startup: an
// identity provider outage must not stop the API from starting (Phase 12.2).
func New(issuer, clientID, clientSecret string, scopes []string, groupsClaim string) *Client {
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	return &Client{
		Issuer: strings.TrimRight(issuer, "/"), ClientID: clientID, ClientSecret: clientSecret,
		Scopes: scopes, GroupsClaim: groupsClaim,
		HTTP: &http.Client{Timeout: 10 * time.Second}, Now: time.Now,
	}
}

func (c *Client) meta(ctx context.Context) (*metadata, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.discovery != nil {
		return c.discovery, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, err
	}
	var m metadata
	if err := c.doJSON(req, &m); err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	if strings.TrimRight(m.Issuer, "/") != c.Issuer {
		return nil, fmt.Errorf("oidc discovery: issuer %q does not match configured %q", m.Issuer, c.Issuer)
	}
	if m.AuthorizationEndpoint == "" || m.TokenEndpoint == "" {
		return nil, fmt.Errorf("oidc discovery: endpoints missing")
	}
	c.discovery = &m
	return &m, nil
}

// AuthorizeURL returns the sign-in page, or "" when the provider cannot be
// reached (the login route then answers 503 instead of redirecting nowhere).
func (c *Client) AuthorizeURL(state, nonce, redirectURI, codeChallenge string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m, err := c.meta(ctx)
	if err != nil {
		return ""
	}
	endpoint := m.AuthorizationEndpoint
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(c.Scopes, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	return endpoint + sep + q.Encode()
}

// Exchange redeems the code and validates the ID token's claims.
func (c *Client) Exchange(ctx context.Context, code, redirectURI, verifier, nonce string) (service.OIDCIdentity, error) {
	m, err := c.meta(ctx)
	if err != nil {
		return service.OIDCIdentity{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return service.OIDCIdentity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(c.ClientID), url.QueryEscape(c.ClientSecret))

	var tok struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := c.doJSON(req, &tok); err != nil {
		if strings.Contains(err.Error(), "status 400") || strings.Contains(err.Error(), "status 401") {
			return service.OIDCIdentity{}, apperrors.Unauthenticated("the identity provider rejected the login; start it again")
		}
		return service.OIDCIdentity{}, err
	}
	if tok.Error != "" || tok.IDToken == "" {
		return service.OIDCIdentity{}, apperrors.Unauthenticated("the identity provider rejected the login; start it again")
	}

	claims, err := c.validate(tok.IDToken, m.Issuer, nonce)
	if err != nil {
		return service.OIDCIdentity{}, err
	}
	id := service.OIDCIdentity{
		Subject:       str(claims["sub"]),
		Email:         str(claims["email"]),
		EmailVerified: claims["email_verified"] == true || claims["email_verified"] == "true",
		Name:          str(claims["name"]),
		Groups:        strs(claims[c.GroupsClaim]),
	}
	// Some providers (Entra ID, Okta with thin tokens) keep the email out of
	// the ID token; ask the userinfo endpoint, which only answers for this
	// user's access token.
	if id.Email == "" && m.UserinfoEndpoint != "" && tok.AccessToken != "" {
		if info, err := c.userinfo(ctx, m.UserinfoEndpoint, tok.AccessToken); err == nil && str(info["sub"]) == id.Subject {
			id.Email = str(info["email"])
			id.EmailVerified = info["email_verified"] == true
			if id.Name == "" {
				id.Name = str(info["name"])
			}
			if len(id.Groups) == 0 {
				id.Groups = strs(info[c.GroupsClaim])
			}
		}
	}
	return id, nil
}

// validate checks the ID token claims OIDC Core §3.1.3.7 requires.
func (c *Client) validate(idToken, issuer, nonce string) (map[string]any, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, apperrors.Unauthenticated("the identity provider returned an invalid ID token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, apperrors.Unauthenticated("the identity provider returned an invalid ID token")
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, apperrors.Unauthenticated("the identity provider returned an invalid ID token")
	}
	now := c.Now()
	switch {
	case strings.TrimRight(str(claims["iss"]), "/") != strings.TrimRight(issuer, "/"):
		return nil, apperrors.Unauthenticated("ID token issuer mismatch")
	case !audienceContains(claims["aud"], c.ClientID):
		return nil, apperrors.Unauthenticated("ID token audience mismatch")
	case num(claims["exp"]) == 0 || now.Unix() > int64(num(claims["exp"]))+60:
		return nil, apperrors.Unauthenticated("ID token expired; start the login again")
	case nonce == "" || str(claims["nonce"]) != nonce:
		return nil, apperrors.Unauthenticated("ID token nonce mismatch; start the login again")
	}
	if azp := str(claims["azp"]); azp != "" && azp != c.ClientID {
		return nil, apperrors.Unauthenticated("ID token authorized party mismatch")
	}
	return claims, nil
}

func (c *Client) userinfo(ctx context.Context, endpoint, accessToken string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	var info map[string]any
	return info, c.doJSON(req, &info)
}

func (c *Client) doJSON(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: status %d", req.Method, req.URL.Path, resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

func audienceContains(aud any, id string) bool {
	switch v := aud.(type) {
	case string:
		return v == id
	case []any:
		for _, a := range v {
			if a == id {
				return true
			}
		}
	}
	return false
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func strs(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return strings.Fields(strings.ReplaceAll(x, ",", " "))
	}
	return nil
}
