package scan

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Repo is one repository from the GitHub API.
type Repo struct {
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	CloneURL string `json:"clone_url"`
	Archived bool   `json:"archived"`
	Fork     bool   `json:"fork"`
	Disabled bool   `json:"disabled"`
	SizeKB   int    `json:"size"`
}

// GitHub lists and clones an organisation's (or user's) repositories.
type GitHub struct {
	// API is the REST base, https://api.github.com or a GitHub Enterprise
	// Server's https://<host>/api/v3.
	API string

	// Token authenticates listing and cloning. Never logged and never
	// placed on a command line.
	Token string

	HTTP *http.Client
}

// NewGitHub returns a client.
func NewGitHub(api, token string) *GitHub {
	if api == "" {
		api = "https://api.github.com"
	}
	return &GitHub{API: strings.TrimRight(api, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// ListOptions filters the listing.
type ListOptions struct {
	IncludeForks    bool
	IncludeArchived bool
	Limit           int // 0 = no limit

	// MaxSizeMB skips repositories larger than this (0 = no limit): huge
	// monorepos dominate clone time in a fleet scan.
	MaxSizeMB int
}

// List returns owner's repositories. It tries the organisation endpoint and
// falls back to the user endpoint, so both "acme" and "octocat" work.
func (g *GitHub) List(ctx context.Context, owner string, opts ListOptions) ([]Repo, error) {
	repos, err := g.list(ctx, "/orgs/"+url.PathEscape(owner)+"/repos?type=all")
	if err != nil && strings.Contains(err.Error(), "404") {
		repos, err = g.list(ctx, "/users/"+url.PathEscape(owner)+"/repos?type=owner")
	}
	if err != nil {
		return nil, err
	}
	var out []Repo
	for _, r := range repos {
		switch {
		case r.Disabled:
		case r.Fork && !opts.IncludeForks:
		case r.Archived && !opts.IncludeArchived:
		case opts.MaxSizeMB > 0 && r.SizeKB > opts.MaxSizeMB*1024:
		default:
			out = append(out, r)
		}
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out, nil
}

func (g *GitHub) list(ctx context.Context, path string) ([]Repo, error) {
	var all []Repo
	for page := 1; page <= 100; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s%s&per_page=100&page=%d", g.API, path, page), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if g.Token != "" {
			req.Header.Set("Authorization", "Bearer "+g.Token)
		}
		resp, err := g.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github: %w", err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github: listing repositories: status %d", resp.StatusCode)
		}
		var batch []Repo
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("github: decoding repository list: %w", err)
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// Clone makes a shallow clone of r into dir/<name>. The token is handed to git
// through GIT_CONFIG_* environment variables as an HTTP header, so it appears
// in neither the process arguments nor the clone's .git/config.
func (g *GitHub) Clone(ctx context.Context, r Repo, dir string) (string, error) {
	dest := filepath.Join(dir, strings.ReplaceAll(r.FullName, "/", "__"))
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "clone", "--depth", "1", "--single-branch", "--quiet", "--no-tags", r.CloneURL, dest)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1")
	if g.Token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + g.Token))
		cmd.Env = append(cmd.Env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cloning %s: %s", r.FullName, strings.TrimSpace(redact(string(out), g.Token)))
	}
	return dest, nil
}

func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}
