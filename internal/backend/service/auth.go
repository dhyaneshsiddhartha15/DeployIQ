package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// GitHubUser is the profile the OAuth exchange yields.
type GitHubUser struct {
	ID    int64
	Login string
	Email string
}

// GitHubOAuth is the GitHub side of Phase 5.3, implemented by
// internal/backend/github and faked in tests.
type GitHubOAuth interface {
	// AuthorizeURL is the consent page to send the browser to.
	AuthorizeURL(state, redirectURI string) string

	// Exchange trades the callback code for the user's profile.
	Exchange(ctx context.Context, code, redirectURI string) (GitHubUser, error)
}

// Auth issues and verifies session tokens and runs the OAuth login.
//
// Tokens are stateless: an HMAC-SHA256 over the user id and expiry, keyed by
// the env-only signing key (Phase 8.4). Short-lived by configuration (Phase
// 5.3). No token table to keep in sync, nothing to leak from the database; the
// cost is that a token cannot be revoked before it expires, which the short TTL
// bounds. Rotating the signing key revokes every token at once.
type Auth struct {
	key    []byte
	ttl    time.Duration
	users  UserStore
	github GitHubOAuth
	now    func() time.Time

	// orgs, when set, applies invitations and SSO membership at login.
	orgs *Orgs

	// oidc, when set, enables company single sign-on.
	oidc      OIDCProvider
	ssoPolicy SSOPolicy
}

// GitHubEnabled reports whether GitHub login is configured.
func (a *Auth) GitHubEnabled() bool { return a.github != nil }

// NewAuth returns the auth service.
func NewAuth(signingKey string, ttl time.Duration, users UserStore, gh GitHubOAuth) *Auth {
	return &Auth{key: []byte(signingKey), ttl: ttl, users: users, github: gh, now: time.Now}
}

// SetClock replaces the time source, for tests.
func (a *Auth) SetClock(now func() time.Time) { a.now = now }

const tokenVersion = "v1"

type sessionClaims struct {
	UserID    string `json:"uid"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// IssueSession mints a session token for userID.
func (a *Auth) IssueSession(userID string) (string, model.Session) {
	now := a.now()
	s := model.Session{UserID: userID, IssuedAt: now, ExpiresAt: now.Add(a.ttl)}
	payload, _ := json.Marshal(sessionClaims{UserID: userID, IssuedAt: now.Unix(), ExpiresAt: s.ExpiresAt.Unix()})
	return a.sign("s", payload), s
}

// VerifySession checks a token and returns its session.
func (a *Auth) VerifySession(token string) (model.Session, error) {
	payload, ok := a.verify("s", token)
	if !ok {
		return model.Session{}, apperrors.Unauthenticated("invalid session token")
	}
	var c sessionClaims
	if err := json.Unmarshal(payload, &c); err != nil || c.UserID == "" {
		return model.Session{}, apperrors.Unauthenticated("invalid session token")
	}
	s := model.Session{UserID: c.UserID, IssuedAt: time.Unix(c.IssuedAt, 0), ExpiresAt: time.Unix(c.ExpiresAt, 0)}
	if s.Expired(a.now()) {
		return model.Session{}, apperrors.Unauthenticated("session expired; log in again")
	}
	return s, nil
}

// LoginState is carried through the OAuth round trip in the signed state
// parameter. It records where to send the browser afterwards.
type LoginState struct {
	// Client is "cli" or "web".
	Client string `json:"c"`

	// Provider is "github" (the default when empty) or "oidc"; a state
	// minted for one provider's callback is refused by the other's.
	Provider string `json:"v,omitempty"`

	// Port is the CLI's loopback port (cli only).
	Port int `json:"p,omitempty"`

	// CLIState is the CLI's own nonce, echoed back so it can verify the
	// redirect came from the flow it started (cli only).
	CLIState string `json:"s,omitempty"`

	// Nonce binds the state to the browser via a cookie (CSRF protection
	// for the callback).
	Nonce string `json:"n"`

	ExpiresAt int64 `json:"e"`
}

// stateTTL bounds how long a user may sit on GitHub's consent page.
const stateTTL = 10 * time.Minute

// NewLoginState validates the login request and returns the signed state plus
// the nonce the caller must set as a cookie.
func (a *Auth) NewLoginState(client string, port int, cliState string) (state, nonce string, err error) {
	return a.NewLoginStateFor(ProviderGitHub, client, port, cliState)
}

// NewLoginStateFor is NewLoginState for a given provider.
func (a *Auth) NewLoginStateFor(provider, client string, port int, cliState string) (state, nonce string, err error) {
	switch client {
	case "web":
		port, cliState = 0, ""
	case "cli":
		// The redirect goes to 127.0.0.1:<port> only; the host is never
		// taken from the request, so this cannot become an open redirect.
		if port < 1024 || port > 65535 {
			return "", "", apperrors.InvalidField("port", "must be a loopback port between 1024 and 65535")
		}
		if len(cliState) < 16 || len(cliState) > 128 || strings.Trim(cliState, "0123456789abcdefABCDEF") != "" {
			return "", "", apperrors.InvalidField("state", "must be 16-128 hex characters")
		}
	default:
		return "", "", apperrors.InvalidField("client", "must be cli or web")
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", "", apperrors.Internal("login nonce", err)
	}
	nonce = hex.EncodeToString(raw)
	payload, _ := json.Marshal(LoginState{
		Client: client, Port: port, CLIState: cliState, Nonce: nonce, Provider: provider,
		ExpiresAt: a.now().Add(stateTTL).Unix(),
	})
	return a.sign("o", payload), nonce, nil
}

// VerifyLoginState checks the state from the callback against the cookie nonce.
func (a *Auth) VerifyLoginState(state, cookieNonce string) (LoginState, error) {
	return a.VerifyLoginStateFor(ProviderGitHub, state, cookieNonce)
}

// VerifyLoginStateFor checks a state minted for provider.
func (a *Auth) VerifyLoginStateFor(provider, state, cookieNonce string) (LoginState, error) {
	ls, err := a.verifyLoginState(state, cookieNonce)
	if err != nil {
		return ls, err
	}
	got := ls.Provider
	if got == "" {
		got = ProviderGitHub
	}
	if got != provider {
		return LoginState{}, apperrors.Invalid("login state is invalid; start the login again")
	}
	return ls, nil
}

func (a *Auth) verifyLoginState(state, cookieNonce string) (LoginState, error) {
	payload, ok := a.verify("o", state)
	if !ok {
		return LoginState{}, apperrors.Invalid("login state is invalid; start the login again")
	}
	var ls LoginState
	if err := json.Unmarshal(payload, &ls); err != nil {
		return LoginState{}, apperrors.Invalid("login state is invalid; start the login again")
	}
	if a.now().Unix() > ls.ExpiresAt {
		return LoginState{}, apperrors.Invalid("login took too long; start it again")
	}
	if cookieNonce == "" || !hmac.Equal([]byte(cookieNonce), []byte(ls.Nonce)) {
		return LoginState{}, apperrors.Invalid("login was started in a different browser session; start it again")
	}
	return ls, nil
}

// AuthorizeURL delegates to the GitHub client.
func (a *Auth) AuthorizeURL(state, redirectURI string) string {
	return a.github.AuthorizeURL(state, redirectURI)
}

// CompleteLogin exchanges the OAuth code, creates or refreshes the account and
// issues a session.
func (a *Auth) CompleteLogin(ctx context.Context, code, redirectURI string) (model.User, string, error) {
	if strings.TrimSpace(code) == "" {
		return model.User{}, "", apperrors.InvalidField("code", "is required")
	}
	if a.github == nil {
		return model.User{}, "", apperrors.Invalid("GitHub login is not configured")
	}
	gh, err := a.github.Exchange(ctx, code, redirectURI)
	if err != nil {
		if apperrors.KindOf(err) != apperrors.KindInternal {
			return model.User{}, "", err
		}
		return model.User{}, "", apperrors.Unavailable("GitHub")
	}
	if gh.ID <= 0 {
		return model.User{}, "", apperrors.Unavailable("GitHub")
	}
	u, err := a.users.UpsertUserByGitHubID(ctx, model.User{
		GitHubID: strconv.FormatInt(gh.ID, 10),
		Login:    gh.Login,
		Email:    gh.Email,
		Plan:     model.PlanFree,
	})
	if err != nil {
		return model.User{}, "", wrapStore("upsert user", err)
	}
	a.joinOrgs(ctx, u, "", false)
	token, _ := a.IssueSession(u.ID)
	return u, token, nil
}

// sign returns <version>.<kind>.<payload>.<mac>, base64url throughout. The
// kind prefix stops a login state being replayed as a session token, or vice
// versa — both are signed with the same key.
func (a *Auth) sign(kind string, payload []byte) string {
	body := tokenVersion + "." + kind + "." + base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(a.mac(body))
}

func (a *Auth) verify(kind, token string) ([]byte, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != tokenVersion || parts[1] != kind {
		return nil, false
	}
	body := strings.Join(parts[:3], ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || !hmac.Equal(sig, a.mac(body)) {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, false
	}
	return payload, true
}

func (a *Auth) mac(body string) []byte {
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte(body))
	return m.Sum(nil)
}
