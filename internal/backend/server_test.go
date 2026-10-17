package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/repository"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/client"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/config"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// fakeGitHub implements the three GitHub endpoints the OAuth flow touches.
// code "good" -> user 1 (private email), code "other" -> user 2.
func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		// Consent is instant: bounce straight back to the callback.
		q := r.URL.Query()
		code := q.Get("login_hint") // tests pick the user this way
		if code == "" {
			code = "good"
		}
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "csecret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Form.Get("code") {
		case "good":
			fmt.Fprint(w, `{"access_token":"gho_1"}`)
		case "other":
			fmt.Fprint(w, `{"access_token":"gho_2"}`)
		default:
			fmt.Fprint(w, `{"error":"bad_verification_code"}`)
		}
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer gho_1":
			fmt.Fprint(w, `{"id":1,"login":"octocat","email":null}`)
		case "Bearer gho_2":
			fmt.Fprint(w, `{"id":2,"login":"hubot","email":"hubot@example.com"}`)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"email":"secondary@example.com","primary":false,"verified":true},{"email":"octo@example.com","primary":true,"verified":true}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type harness struct {
	t    *testing.T
	srv  *Server
	base string
	gh   *httptest.Server
}

func testConfig(gh string) *config.Config {
	cfg := config.Default()
	cfg.Server.GRPCAddr = "127.0.0.1:0"
	cfg.Server.HTTPAddr = "127.0.0.1:0"
	cfg.Server.AdminAddr = "127.0.0.1:0"
	cfg.Server.ShutdownTimeout = 5 * time.Second
	cfg.Mongo.URI = repository.MemoryURI
	cfg.GitHub.ClientID = "cid"
	cfg.GitHub.ClientSecret = "csecret"
	cfg.GitHub.AuthURL = gh + "/login/oauth/authorize"
	cfg.GitHub.TokenURL = gh + "/login/oauth/access_token"
	cfg.GitHub.APIURL = gh
	cfg.Auth.SessionSigningKey = "test-signing-key-0123456789"
	cfg.Log.Format = "text"
	cfg.RateLimit.RequestsPerMinute = 100000
	cfg.RateLimit.Burst = 100000
	return &cfg
}

func newHarness(t *testing.T, mutate ...func(*config.Config)) *harness {
	t.Helper()
	gh := fakeGitHub(t)
	cfg := testConfig(gh.URL)
	for _, m := range mutate {
		m(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Shutdown)
	return &harness{t: t, srv: srv, base: "http://" + srv.HTTPAddr(), gh: gh}
}

// noRedirect is a client that returns 3xx responses instead of following them.
func noRedirect() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("not JSON (%d): %s", r.status, r.body)
	}
}

func (r response) errBody(t *testing.T) (code, message, field string) {
	t.Helper()
	var e struct {
		Error struct{ Code, Message, Field string } `json:"error"`
	}
	r.json(t, &e)
	return e.Error.Code, e.Error.Message, e.Error.Field
}

func (h *harness) do(method, path, token, body string) response {
	h.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, h.base+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: b}
}

// webLogin runs the browser OAuth flow and returns the session token.
func (h *harness) webLogin(hint string) string {
	h.t.Helper()
	c := noRedirect()
	resp, err := c.Get(h.base + "/api/v1/auth/github/login?client=web")
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		h.t.Fatalf("login: status %d", resp.StatusCode)
	}
	authorize, _ := url.Parse(resp.Header.Get("Location"))
	if authorize.Query().Get("client_id") != "cid" || !strings.Contains(authorize.Query().Get("scope"), "user:email") {
		h.t.Fatalf("authorize URL = %s", authorize)
	}
	if hint != "" {
		q := authorize.Query()
		q.Set("login_hint", hint)
		authorize.RawQuery = q.Encode()
	}
	// GitHub "consents" and redirects to the callback.
	resp, err = c.Get(authorize.String())
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	callback := resp.Header.Get("Location")
	resp, err = c.Get(callback)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		h.t.Fatalf("callback: status %d", resp.StatusCode)
	}
	final := resp.Header.Get("Location")
	_, frag, ok := strings.Cut(final, "/login/callback#token=")
	if !ok || frag == "" {
		h.t.Fatalf("final redirect = %q", final)
	}
	tok, _ := url.QueryUnescape(frag)
	return tok
}

const validReport = `{"repoName":"company/backend","detectedStack":"node","originalEstSizeMB":640,"optimizedEstSizeMB":118,"rulesApplied":["multi-stage-build","alpine-base","prod-only-deps"]}`

func TestHealthAndDocs(t *testing.T) {
	h := newHarness(t)
	if r := h.do("GET", "/healthz", "", ""); r.status != 200 || !strings.Contains(string(r.body), "ok") {
		t.Fatalf("healthz: %d %s", r.status, r.body)
	}
	r := h.do("GET", "/swagger/apidocs.swagger.json", "", "")
	if r.status != 200 || !strings.Contains(string(r.body), "/api/v1/optimizations") {
		t.Fatalf("swagger: %d", r.status)
	}
	if r := h.do("GET", "/api/v1/nope", "", ""); r.status != 404 {
		t.Fatalf("unknown route: %d", r.status)
	} else if code, _, _ := r.errBody(t); code != "not_found" {
		t.Fatalf("code %s", code)
	}
	if r := h.do("GET", "/healthz", "", ""); r.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers missing")
	}
}

func TestUnauthenticated(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/me", ""},
		{"GET", "/api/v1/optimizations", ""},
		{"POST", "/api/v1/optimizations", validReport},
	} {
		for _, tok := range []string{"", "garbage", "v1.s.e30.AAAA"} {
			r := h.do(c.method, c.path, tok, c.body)
			if r.status != http.StatusUnauthorized {
				t.Errorf("%s %s token=%q: %d %s", c.method, c.path, tok, r.status, r.body)
				continue
			}
			if code, _, _ := r.errBody(t); code != "unauthenticated" {
				t.Errorf("code = %s", code)
			}
		}
	}
}

func TestLoginMeAndOptimizations(t *testing.T) {
	h := newHarness(t)
	tok := h.webLogin("")

	// GET /api/v1/me — the private email was resolved via /user/emails.
	r := h.do("GET", "/api/v1/me", tok, "")
	if r.status != 200 {
		t.Fatalf("me: %d %s", r.status, r.body)
	}
	var me struct{ ID, GithubID, Login, Email, Plan string }
	r.json(t, &me)
	if me.GithubID != "1" || me.Login != "octocat" || me.Email != "octo@example.com" || me.Plan != "free" || me.ID == "" {
		t.Fatalf("me = %+v", me)
	}

	// POST /api/v1/optimizations with the exact Phase 4.5 example body.
	r = h.do("POST", "/api/v1/optimizations", tok, validReport)
	if r.status != 200 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	var created struct {
		ID                 string   `json:"id"`
		RepoName           string   `json:"repoName"`
		DetectedStack      string   `json:"detectedStack"`
		OriginalEstSizeMB  int      `json:"originalEstSizeMB"`
		OptimizedEstSizeMB int      `json:"optimizedEstSizeMB"`
		RulesApplied       []string `json:"rulesApplied"`
		CreatedAt          string   `json:"createdAt"`
		SavingsPercent     float64  `json:"savingsPercent"`
	}
	r.json(t, &created)
	if created.ID == "" || created.RepoName != "company/backend" || created.OriginalEstSizeMB != 640 ||
		created.OptimizedEstSizeMB != 118 || len(created.RulesApplied) != 3 || created.CreatedAt == "" {
		t.Fatalf("created = %+v", created)
	}
	if created.SavingsPercent < 81 || created.SavingsPercent > 82 {
		t.Fatalf("savings = %v", created.SavingsPercent)
	}

	h.do("POST", "/api/v1/optimizations", tok, strings.Replace(validReport, "company/backend", "company/web", 1))
	h.do("POST", "/api/v1/optimizations", tok, strings.Replace(validReport, "company/backend", "company/web", 1))

	// GET list, newest first; ?repo= filter; limit.
	var list struct {
		Optimizations []struct {
			RepoName string `json:"repoName"`
		} `json:"optimizations"`
	}
	h.do("GET", "/api/v1/optimizations", tok, "").json(t, &list)
	if len(list.Optimizations) != 3 || list.Optimizations[2].RepoName != "company/backend" {
		t.Fatalf("list = %+v", list)
	}
	h.do("GET", "/api/v1/optimizations?repo=company/web", tok, "").json(t, &list)
	if len(list.Optimizations) != 2 {
		t.Fatalf("repo filter = %+v", list)
	}
	h.do("GET", "/api/v1/optimizations?limit=1", tok, "").json(t, &list)
	if len(list.Optimizations) != 1 {
		t.Fatalf("limit = %+v", list)
	}

	// Validation on the list parameters.
	r = h.do("GET", "/api/v1/optimizations?repo=bad%20name", tok, "")
	if code, _, field := r.errBody(t); r.status != 400 || code != "invalid_argument" || field != "repo" {
		t.Fatalf("bad repo: %d %s", r.status, r.body)
	}
	if r := h.do("GET", "/api/v1/optimizations?limit=-1", tok, ""); r.status != 400 {
		t.Fatalf("negative limit: %d", r.status)
	}
	if r := h.do("GET", "/api/v1/optimizations?limit=abc", tok, ""); r.status != 400 {
		t.Fatalf("non-numeric limit: %d", r.status)
	}
}

func TestCreateValidation(t *testing.T) {
	h := newHarness(t)
	tok := h.webLogin("")
	cases := []struct {
		name, body, field string
	}{
		{"empty repo", `{"repoName":"","detectedStack":"node","originalEstSizeMB":1,"optimizedEstSizeMB":1}`, "repoName"},
		{"repo with spaces", `{"repoName":"a b","detectedStack":"node","originalEstSizeMB":1,"optimizedEstSizeMB":1}`, "repoName"},
		{"repo too long", `{"repoName":"` + strings.Repeat("a", 201) + `","detectedStack":"node","originalEstSizeMB":1,"optimizedEstSizeMB":1}`, "repoName"},
		{"unknown stack", `{"repoName":"a/b","detectedStack":"cobol","originalEstSizeMB":1,"optimizedEstSizeMB":1}`, "detectedStack"},
		{"zero original", `{"repoName":"a/b","detectedStack":"go","originalEstSizeMB":0,"optimizedEstSizeMB":1}`, "originalEstSizeMB"},
		{"negative optimized", `{"repoName":"a/b","detectedStack":"go","originalEstSizeMB":10,"optimizedEstSizeMB":-1}`, "optimizedEstSizeMB"},
		{"huge", `{"repoName":"a/b","detectedStack":"go","originalEstSizeMB":2000000000,"optimizedEstSizeMB":1}`, "originalEstSizeMB"},
		{"bad rule", `{"repoName":"a/b","detectedStack":"go","originalEstSizeMB":10,"optimizedEstSizeMB":1,"rulesApplied":["Not A Rule"]}`, "rulesApplied"},
	}
	for _, c := range cases {
		r := h.do("POST", "/api/v1/optimizations", tok, c.body)
		code, msg, field := r.errBody(t)
		if r.status != 400 || code != "invalid_argument" || field != c.field || msg == "" {
			t.Errorf("%s: %d %s", c.name, r.status, r.body)
		}
	}

	// Malformed bodies: 400 with a fixed message that does not echo input.
	for name, body := range map[string]string{
		"not json":      `{"repoName": <script>`,
		"unknown field": `{"repoName":"a/b","sneaky<script>":1}`,
		"wrong type":    `{"repoName":["<script>"]}`,
	} {
		r := h.do("POST", "/api/v1/optimizations", tok, body)
		code, msg, _ := r.errBody(t)
		if r.status != 400 || code != "invalid_argument" || strings.Contains(string(r.body), "script") || msg == "" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}

	// Oversized body is rejected, not buffered.
	big := `{"repoName":"a/b","detectedStack":"go","originalEstSizeMB":10,"optimizedEstSizeMB":1,"rulesApplied":["` + strings.Repeat("a", 70<<10) + `"]}`
	if r := h.do("POST", "/api/v1/optimizations", tok, big); r.status < 400 || r.status >= 500 && r.status != 413 {
		t.Errorf("oversized body: %d", r.status)
	}
}

func TestUsersOnlySeeTheirOwnData(t *testing.T) {
	h := newHarness(t)
	alice := h.webLogin("good")
	bob := h.webLogin("other")
	if alice == bob {
		t.Fatal("distinct users got the same token")
	}
	h.do("POST", "/api/v1/optimizations", alice, validReport)

	var list struct {
		Optimizations []json.RawMessage `json:"optimizations"`
	}
	h.do("GET", "/api/v1/optimizations", bob, "").json(t, &list)
	if len(list.Optimizations) != 0 {
		t.Fatalf("bob sees alice's data: %d records", len(list.Optimizations))
	}
	h.do("GET", "/api/v1/optimizations?repo=company/backend", bob, "").json(t, &list)
	if len(list.Optimizations) != 0 {
		t.Fatal("repo filter leaks across users")
	}
	var me struct{ Login string }
	h.do("GET", "/api/v1/me", bob, "").json(t, &me)
	if me.Login != "hubot" {
		t.Fatalf("bob = %+v", me)
	}
}

func TestOAuthCallbackRejections(t *testing.T) {
	h := newHarness(t)
	c := noRedirect()

	// No state at all.
	resp, _ := c.Get(h.base + "/api/v1/auth/github/callback?code=good")
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("missing state: %d", resp.StatusCode)
	}

	// Valid state but no nonce cookie: a login started in another browser.
	state, _, err := h.srv.Auth().NewLoginState("web", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	resp, _ = http.Get(h.base + "/api/v1/auth/github/callback?code=good&state=" + url.QueryEscape(state))
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("missing cookie: %d", resp.StatusCode)
	}

	// Bad login parameters.
	for _, q := range []string{"client=desktop", "client=cli&port=80&state=" + strings.Repeat("a", 32), "client=cli&port=5000&state=short"} {
		resp, _ := c.Get(h.base + "/api/v1/auth/github/login?" + q)
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("%s: %d", q, resp.StatusCode)
		}
	}

	// The user declines consent on GitHub: back to the dashboard login page.
	resp, _ = c.Get(h.base + "/api/v1/auth/github/login?client=web")
	resp.Body.Close()
	authz, _ := url.Parse(resp.Header.Get("Location"))
	resp, _ = c.Get(h.base + "/api/v1/auth/github/callback?error=access_denied&state=" + url.QueryEscape(authz.Query().Get("state")))
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode != 302 || loc != "/login?error=access_denied" {
		t.Errorf("declined: %d %s", resp.StatusCode, loc)
	}

	// A bad code from GitHub: 401, not 500.
	resp, _ = c.Get(h.base + "/api/v1/auth/github/login?client=web")
	resp.Body.Close()
	authz, _ = url.Parse(resp.Header.Get("Location"))
	resp, _ = c.Get(h.base + "/api/v1/auth/github/callback?code=wrong&state=" + url.QueryEscape(authz.Query().Get("state")))
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("bad code: %d %s", resp.StatusCode, b)
	}
}

func TestExpiredSession(t *testing.T) {
	h := newHarness(t)
	auth := h.srv.Auth()
	auth.SetClock(func() time.Time { return time.Now().Add(-48 * time.Hour) })
	old, _ := auth.IssueSession("000000000000000000000000")
	auth.SetClock(time.Now)
	r := h.do("GET", "/api/v1/me", old, "")
	if _, msg, _ := r.errBody(t); r.status != 401 || !strings.Contains(msg, "expired") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	// A valid token for an account that does not exist.
	ghost, _ := auth.IssueSession("ffffffffffffffffffffffff")
	if r := h.do("GET", "/api/v1/me", ghost, ""); r.status != 401 {
		t.Fatalf("ghost: %d", r.status)
	}
	if r := h.do("POST", "/api/v1/optimizations", ghost, validReport); r.status != 401 {
		t.Fatalf("ghost create: %d %s", r.status, r.body)
	}
}

func TestRateLimit(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.RateLimit.RequestsPerMinute = 60
		c.RateLimit.Burst = 3
	})
	var last response
	for i := 0; i < 4; i++ {
		last = h.do("GET", "/api/v1/me", "", "")
	}
	if last.status != http.StatusTooManyRequests || last.header.Get("Retry-After") == "" {
		t.Fatalf("4th request: %d", last.status)
	}
	if code, _, _ := last.errBody(t); code != "rate_limited" {
		t.Fatal(code)
	}
	// Non-API routes are not limited.
	if r := h.do("GET", "/healthz", "", ""); r.status != 200 {
		t.Fatalf("healthz limited: %d", r.status)
	}
}

func TestAdminEndpoints(t *testing.T) {
	h := newHarness(t)
	base := "http://" + h.srv.AdminAddr()
	for _, p := range []string{"/internal/healthz", "/internal/version", "/internal/log-level"} {
		resp, err := http.Get(base + p)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", p, err, resp)
		}
		resp.Body.Close()
	}
	req, _ := http.NewRequest("PUT", base+"/internal/log-level", strings.NewReader(`{"level":"debug"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("set level: %v %v", err, resp)
	}
	resp.Body.Close()
	req, _ = http.NewRequest("PUT", base+"/internal/log-level", strings.NewReader(`{"level":"loud"}`))
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("bad level: %d", resp.StatusCode)
	}
	// The admin surface is not on the public listener.
	if r := h.do("GET", "/internal/healthz", "", ""); r.status == 200 {
		t.Fatal("admin route reachable on the public listener")
	}
}

// TestCLILoginEndToEnd drives `doiq login`'s client code against the real
// server: loopback listener, browser redirects, state check, token delivery,
// then -report and whoami with the token it received.
func TestCLILoginEndToEnd(t *testing.T) {
	h := newHarness(t)
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	token, err := client.Login(ctx, h.base, func(u string) error {
		go func() {
			resp, err := browser.Get(u) // follows every redirect, like a browser
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}

	c := client.New(h.base, token)
	me, err := c.Me(ctx)
	if err != nil || me.Login != "octocat" {
		t.Fatalf("me = %+v, %v", me, err)
	}
	saved, err := c.ReportOptimization(ctx, model.OptimizationReport{
		RepoName: "acme/api", DetectedStack: "go", OriginalEstSizeMB: 899, OptimizedEstSizeMB: 9,
		RulesApplied: []string{"multi-stage-build", "distroless-base"},
	})
	if err != nil || saved.ID == "" || saved.RepoName != "acme/api" {
		t.Fatalf("report = %+v, %v", saved, err)
	}
	_, err = c.ReportOptimization(ctx, model.OptimizationReport{RepoName: "", DetectedStack: "go", OriginalEstSizeMB: 1, OptimizedEstSizeMB: 1})
	var apiErr *client.APIError
	if !asAPIError(err, &apiErr) || apiErr.Status != 400 || apiErr.Field != "repoName" {
		t.Fatalf("validation error = %v", err)
	}
	if _, err := client.New(h.base, "bogus").Me(ctx); err != client.ErrUnauthenticated {
		t.Fatalf("bogus token: %v", err)
	}
}

func asAPIError(err error, target **client.APIError) bool {
	e, ok := err.(*client.APIError)
	if ok {
		*target = e
	}
	return ok
}

func TestCORS(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.Server.CORSOrigins = []string{"https://dash.example.com"} })
	req, _ := http.NewRequest("OPTIONS", h.base+"/api/v1/me", nil)
	req.Header.Set("Origin", "https://dash.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "https://dash.example.com" {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
	req.Header.Set("Origin", "https://evil.example.com")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unlisted origin allowed")
	}
}

func TestDashboardServing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/index.html", "<!doctype html><title>dash</title>")
	writeFile(t, dir+"/assets/app-123.js", "console.log(1)")
	h := newHarness(t, func(c *config.Config) { c.Server.WebDir = dir })

	if r := h.do("GET", "/", "", ""); r.status != 200 || !strings.Contains(string(r.body), "dash") {
		t.Fatalf("/: %d", r.status)
	}
	// Client-side routes fall back to index.html.
	if r := h.do("GET", "/dashboard/acme/api", "", ""); r.status != 200 || !strings.Contains(string(r.body), "dash") {
		t.Fatalf("spa fallback: %d", r.status)
	}
	r := h.do("GET", "/assets/app-123.js", "", "")
	if r.status != 200 || !strings.Contains(r.header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %v", r.status, r.header)
	}
	// The API is not shadowed by the SPA.
	if r := h.do("GET", "/api/v1/me", "", ""); r.status != 401 {
		t.Fatalf("api shadowed: %d", r.status)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAlertMonitor(t *testing.T) {
	cfg := config.Default().Alert
	cfg.MinRequests, cfg.Sustain, cfg.Threshold = 10, 2, 0.1
	m := newAlertMonitor(cfg)

	window := func(total, errs int) string {
		for i := 0; i < total; i++ {
			s := 200
			if i < errs {
				s = 503
			}
			m.observe(s)
		}
		return m.tick()
	}
	if window(5, 5) != "" {
		t.Fatal("too few requests must not alert")
	}
	if window(100, 50) != "" {
		t.Fatal("one bad window is not sustained")
	}
	msg := window(100, 50)
	if !strings.Contains(msg, "50.0%") {
		t.Fatalf("second bad window should alert: %q", msg)
	}
	if window(100, 50) != "" {
		t.Fatal("cooldown must suppress a repeat")
	}
	if window(100, 0) != "" {
		t.Fatal("healthy window")
	}
}

func TestRateLimiterSweep(t *testing.T) {
	l := newRateLimiter(config.RateLimitConfig{RequestsPerMinute: 60, Burst: 1}, false)
	now := time.Now()
	l.now = func() time.Time { return now }
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("first")
	}
	if ok, wait := l.allow("a"); ok || wait <= 0 {
		t.Fatal("second should wait")
	}
	now = now.Add(time.Hour)
	l.sweep()
	if len(l.buckets) != 0 {
		t.Fatal("idle bucket not swept")
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if ip := l.clientIP(req); ip == "1.2.3.4" {
		t.Fatal("proxy headers trusted without opt-in")
	}
	l.trust = true
	if ip := l.clientIP(req); ip != "1.2.3.4" {
		t.Fatal(ip)
	}
}
