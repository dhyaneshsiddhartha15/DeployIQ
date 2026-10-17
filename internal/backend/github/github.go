// Package github is the GitHub OAuth client behind service.GitHubOAuth
// (Phase 3.1: GitHub OAuth only — the user base is already on GitHub, and no
// passwords means no password liability).
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/service"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
)

// scopes requests the minimum: the public profile and the email address.
// Nothing that reads repositories — the dashboard shows data the CLI chose to
// send, not data pulled from GitHub.
const scopes = "read:user user:email"

// Client implements service.GitHubOAuth.
type Client struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	APIURL       string
	HTTP         *http.Client
}

var _ service.GitHubOAuth = (*Client)(nil)

// New returns a client with a bounded timeout: a hung GitHub must not hold a
// request goroutine forever (Phase 12.2: dependency outages are waited out,
// not retried indefinitely).
func New(clientID, clientSecret, authURL, tokenURL, apiURL string) *Client {
	return &Client{
		ClientID: clientID, ClientSecret: clientSecret,
		AuthURL: authURL, TokenURL: tokenURL, APIURL: strings.TrimRight(apiURL, "/"),
		HTTP: &http.Client{Timeout: 10 * time.Second},
	}
}

// AuthorizeURL returns GitHub's consent page.
func (c *Client) AuthorizeURL(state, redirectURI string) string {
	q := url.Values{}
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", scopes)
	q.Set("state", state)
	q.Set("allow_signup", "true")
	sep := "?"
	if strings.Contains(c.AuthURL, "?") {
		sep = "&"
	}
	return c.AuthURL + sep + q.Encode()
}

// Exchange trades the code for an access token and reads the profile. The
// GitHub access token is used for these two calls and then discarded: the API
// issues its own session (Phase 5.3) and never stores GitHub credentials.
func (c *Client) Exchange(ctx context.Context, code, redirectURI string) (service.GitHubUser, error) {
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return service.GitHubUser{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := c.doJSON(req, &tok); err != nil {
		return service.GitHubUser{}, err
	}
	if tok.Error != "" || tok.AccessToken == "" {
		// bad_verification_code and friends: the user's code, not our
		// fault, and not something to retry.
		return service.GitHubUser{}, apperrors.Unauthenticated("GitHub rejected the login code; start the login again")
	}

	var profile struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Email string `json:"email"`
	}
	if err := c.api(ctx, tok.AccessToken, "/user", &profile); err != nil {
		return service.GitHubUser{}, err
	}
	u := service.GitHubUser{ID: profile.ID, Login: profile.Login, Email: profile.Email}

	if u.Email == "" {
		// A private email is only visible through /user/emails.
		var emails []struct {
			Email    string `json:"email"`
			Primary  bool   `json:"primary"`
			Verified bool   `json:"verified"`
		}
		if err := c.api(ctx, tok.AccessToken, "/user/emails", &emails); err == nil {
			for _, e := range emails {
				if e.Primary && e.Verified {
					u.Email = e.Email
				}
			}
		}
	}
	return u, nil
}

func (c *Client) api(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return c.doJSON(req, out)
}

func (c *Client) doJSON(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("github: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github: %s %s: status %d", req.Method, req.URL.Path, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("github: decoding %s: %w", req.URL.Path, err)
	}
	return nil
}
