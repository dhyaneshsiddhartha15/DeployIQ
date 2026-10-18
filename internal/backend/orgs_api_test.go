package backend

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/config"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// fakeIDP is a minimal OpenID Connect provider: discovery, an authorize
// endpoint that consents at once, and a token endpoint that enforces PKCE and
// returns an ID token carrying the nonce it was given.
type fakeIDP struct {
	*httptest.Server
	mu        sync.Mutex
	codes     map[string]idpGrant
	user      map[string]any // claims for the next login
	breakPKCE bool
}

type idpGrant struct{ challenge, nonce string }

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	f := &fakeIDP{codes: map[string]idpGrant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer": f.URL, "authorization_endpoint": f.URL + "/authorize", "token_endpoint": f.URL + "/token",
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "oidc-client" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		code := fmt.Sprintf("code-%d", time.Now().UnixNano())
		f.mu.Lock()
		f.codes[code] = idpGrant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce")}
		f.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		id, secret, _ := r.BasicAuth()
		f.mu.Lock()
		g, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		claims := map[string]any{}
		for k, v := range f.user {
			claims[k] = v
		}
		broken := f.breakPKCE
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || id != "oidc-client" || secret != "oidc-secret" || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge || broken {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		claims["iss"], claims["aud"], claims["nonce"] = f.URL, "oidc-client", g.nonce
		claims["exp"] = float64(time.Now().Add(time.Hour).Unix())
		payload, _ := json.Marshal(claims)
		jwt := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
		json.NewEncoder(w).Encode(map[string]string{"id_token": jwt, "access_token": "at"})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeIDP) next(claims map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.user = claims
}

func withOIDC(idp *fakeIDP, mutate ...func(*config.Config)) func(*config.Config) {
	return func(c *config.Config) {
		c.OIDC.Issuer = idp.URL
		c.OIDC.ClientID = "oidc-client"
		c.OIDC.ClientSecret = "oidc-secret"
		c.OIDC.DisplayName = "Okta"
		for _, m := range mutate {
			m(c)
		}
	}
}

// ssoLogin runs the browser SSO flow and returns the final redirect target.
func (h *harness) ssoLogin() string {
	h.t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if strings.HasPrefix(req.URL.Path, "/login") {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	resp, err := c.Get(h.base + "/api/v1/auth/oidc/login?client=web")
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Header.Get("Location")
}

func tokenFrom(t *testing.T, loc string) string {
	t.Helper()
	_, frag, ok := strings.Cut(loc, "#token=")
	if !ok {
		t.Fatalf("no token in %q", loc)
	}
	tok, _ := url.QueryUnescape(frag)
	return tok
}

// stranger mints a session for a fresh account in no organisation.
func (h *harness) stranger(t *testing.T) string {
	t.Helper()
	u, err := h.srv.store.UpsertUserByGitHubID(t.Context(), model.User{GitHubID: "999", Login: "stranger"})
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := h.srv.Auth().IssueSession(u.ID)
	return tok
}

type providers struct {
	GitHub bool `json:"github"`
	OIDC   struct {
		Enabled bool   `json:"enabled"`
		Name    string `json:"name"`
	} `json:"oidc"`
}

func TestProvidersEndpoint(t *testing.T) {
	h := newHarness(t)
	var p providers
	h.do("GET", "/api/v1/auth/providers", "", "").json(t, &p)
	if !p.GitHub || p.OIDC.Enabled {
		t.Fatalf("%+v", p)
	}
	if r := h.do("GET", "/api/v1/auth/oidc/login", "", ""); r.status != http.StatusNotFound {
		t.Fatalf("oidc route must not exist when disabled: %d", r.status)
	}

	idp := newFakeIDP(t)
	h = newHarness(t, withOIDC(idp))
	h.do("GET", "/api/v1/auth/providers", "", "").json(t, &p)
	if !p.GitHub || !p.OIDC.Enabled || p.OIDC.Name != "Okta" {
		t.Fatalf("%+v", p)
	}
}

func TestSSOLoginAndAutoOrg(t *testing.T) {
	idp := newFakeIDP(t)
	h := newHarness(t, withOIDC(idp, func(c *config.Config) {
		c.OIDC.Organization = "acme"
		c.OIDC.AdminGroups = []string{"platform-admins"}
		c.OIDC.AllowedDomains = []string{"acme.com"}
	}))

	idp.next(map[string]any{"sub": "u-1", "email": "Pat@ACME.com", "email_verified": true, "name": "Pat", "groups": []any{"platform-admins"}})
	pat := tokenFrom(t, h.ssoLogin())
	var me struct{ Provider, Email, Name string }
	h.do("GET", "/api/v1/me", pat, "").json(t, &me)
	if me.Provider != "oidc" || me.Email != "pat@acme.com" || me.Name != "Pat" {
		t.Fatalf("me = %+v", me)
	}
	var orgs struct {
		Orgs []struct{ Slug, Role string } `json:"orgs"`
	}
	h.do("GET", "/api/v1/orgs", pat, "").json(t, &orgs)
	if len(orgs.Orgs) != 1 || orgs.Orgs[0].Slug != "acme" || orgs.Orgs[0].Role != "admin" {
		t.Fatalf("auto org: %+v", orgs)
	}

	// Without the admin group: a member.
	idp.next(map[string]any{"sub": "u-2", "email": "sam@acme.com", "email_verified": true})
	sam := tokenFrom(t, h.ssoLogin())
	h.do("GET", "/api/v1/orgs", sam, "").json(t, &orgs)
	if len(orgs.Orgs) != 1 || orgs.Orgs[0].Role != "member" {
		t.Fatalf("second user: %+v", orgs)
	}

	// Outside the allowed domain, or unverified: refused.
	idp.next(map[string]any{"sub": "u-3", "email": "eve@evil.com", "email_verified": true})
	if loc := h.ssoLogin(); loc != "/login?error=domain_not_allowed" {
		t.Fatalf("other domain: %q", loc)
	}
	idp.next(map[string]any{"sub": "u-4", "email": "x@acme.com", "email_verified": false})
	if loc := h.ssoLogin(); loc != "/login?error=domain_not_allowed" {
		t.Fatalf("unverified email: %q", loc)
	}

	// A PKCE failure at the provider never yields a session.
	idp.mu.Lock()
	idp.breakPKCE = true
	idp.mu.Unlock()
	idp.next(map[string]any{"sub": "u-1", "email": "pat@acme.com", "email_verified": true})
	if loc := h.ssoLogin(); loc != "/login?error=login_failed" {
		t.Fatalf("pkce failure: %q", loc)
	}
}

func TestSSOOnlyDeployment(t *testing.T) {
	idp := newFakeIDP(t)
	h := newHarness(t, withOIDC(idp, func(c *config.Config) { c.GitHub.ClientID, c.GitHub.ClientSecret = "", "" }))
	if r := h.do("GET", "/api/v1/auth/github/login", "", ""); r.status != http.StatusNotFound {
		t.Fatalf("github login must be absent: %d", r.status)
	}
	var p providers
	h.do("GET", "/api/v1/auth/providers", "", "").json(t, &p)
	if p.GitHub || !p.OIDC.Enabled {
		t.Fatalf("%+v", p)
	}
	idp.next(map[string]any{"sub": "only", "email": "a@b.c", "email_verified": true})
	if tok := tokenFrom(t, h.ssoLogin()); tok == "" {
		t.Fatal("no session")
	}
}

func TestOrganizationsAPI(t *testing.T) {
	h := newHarness(t)
	alice := h.webLogin("good") // octocat
	if r := h.do("POST", "/api/v1/orgs", alice, `{"slug":"acme","name":"Acme Platform"}`); r.status != http.StatusOK {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	if r := h.do("POST", "/api/v1/orgs", alice, `{"slug":"acme","name":"x"}`); r.status != http.StatusConflict {
		t.Fatalf("duplicate slug: %d", r.status)
	}
	if r := h.do("POST", "/api/v1/orgs", alice, `{"slug":"No Spaces","name":"x"}`); r.status != http.StatusBadRequest {
		t.Fatalf("bad slug: %d", r.status)
	}

	// Invite hubot by email before they have ever logged in.
	var invited struct {
		Invitation *struct{ ID, Email string } `json:"invitation"`
		Member     *struct{ Role string }      `json:"member"`
	}
	h.do("POST", "/api/v1/orgs/acme/members", alice, `{"email":"hubot@example.com","role":"member"}`).json(t, &invited)
	if invited.Invitation == nil || invited.Invitation.Email != "hubot@example.com" || invited.Member != nil {
		t.Fatalf("invitation: %+v", invited)
	}
	if r := h.do("POST", "/api/v1/orgs/acme/members", alice, `{"email":"not-an-email"}`); r.status != http.StatusBadRequest {
		t.Fatalf("bad email: %d", r.status)
	}
	if r := h.do("POST", "/api/v1/orgs/acme/members", alice, `{"email":"a@b.c","login":"also"}`); r.status != http.StatusBadRequest {
		t.Fatalf("both email and login: %d", r.status)
	}

	bob := h.webLogin("other") // hubot <hubot@example.com>: claims the invitation
	var orgs struct {
		Orgs []struct{ Slug, Role string } `json:"orgs"`
	}
	h.do("GET", "/api/v1/orgs", bob, "").json(t, &orgs)
	if len(orgs.Orgs) != 1 || orgs.Orgs[0].Role != "member" {
		t.Fatalf("invitation not claimed: %+v", orgs)
	}
	// Inviting an existing account adds it at once.
	strangerTok := h.stranger(t)
	invited.Invitation, invited.Member = nil, nil
	h.do("POST", "/api/v1/orgs/acme/members", alice, `{"login":"STRANGER"}`).json(t, &invited)
	if invited.Member == nil || invited.Member.Role != "member" {
		t.Fatalf("existing user not added: %+v", invited)
	}

	// Members report into the org; personal reports stay personal.
	if r := h.do("POST", "/api/v1/optimizations", bob, `{"repoName":"acme/api","detectedStack":"go","originalEstSizeMB":900,"optimizedEstSizeMB":10,"org":"acme"}`); r.status != http.StatusOK {
		t.Fatalf("org report: %d %s", r.status, r.body)
	}
	h.do("POST", "/api/v1/optimizations", alice, `{"repoName":"acme/web","detectedStack":"node","originalEstSizeMB":1100,"optimizedEstSizeMB":180,"org":"acme"}`)
	h.do("POST", "/api/v1/optimizations", alice, `{"repoName":"alice/side","detectedStack":"java","originalEstSizeMB":500,"optimizedEstSizeMB":100}`)
	r := h.do("POST", "/api/v1/optimizations", bob, `{"repoName":"x/y","detectedStack":"go","originalEstSizeMB":1,"optimizedEstSizeMB":1,"org":"other-org"}`)
	if _, _, field := r.errBody(t); r.status != http.StatusBadRequest || field != "org" {
		t.Fatalf("report into a foreign org: %d %s", r.status, r.body)
	}

	var sum struct {
		Repos []struct {
			RepoName        string  `json:"repoName"`
			ReportedBy      string  `json:"reportedBy"`
			MonthlySavedUSD float64 `json:"monthlySavedUSD"`
		} `json:"repos"`
		Members         int     `json:"members"`
		SavedMB         float64 `json:"savedMB"`
		MonthlySavedUSD float64 `json:"monthlySavedUSD"`
	}
	h.do("GET", "/api/v1/orgs/acme/summary", bob, "").json(t, &sum)
	if len(sum.Repos) != 2 || sum.Members != 3 || sum.SavedMB != 890+920 || sum.MonthlySavedUSD <= 0 {
		t.Fatalf("summary: %+v", sum)
	}
	if sum.Repos[0].RepoName != "acme/web" || sum.Repos[0].ReportedBy != "octocat" {
		t.Fatalf("biggest saving first, with reporter: %+v", sum.Repos)
	}
	var list struct {
		Optimizations []json.RawMessage `json:"optimizations"`
	}
	h.do("GET", "/api/v1/orgs/acme/optimizations?repo=acme/api", alice, "").json(t, &list)
	if len(list.Optimizations) != 1 {
		t.Fatalf("org list: %d", len(list.Optimizations))
	}

	// Authorization matrix.
	outsiderUser, _ := h.srv.store.UpsertUserByGitHubID(t.Context(), model.User{GitHubID: "777", Login: "outsider"})
	nobody, _ := h.srv.Auth().IssueSession(outsiderUser.ID)
	ghost, _ := h.srv.Auth().IssueSession("ffffffffffffffffffffffff")
	if r := h.do("POST", "/api/v1/orgs", ghost, `{"slug":"ghost-org","name":"x"}`); r.status != http.StatusUnauthorized {
		t.Fatalf("a session for a deleted account must not create orgs: %d", r.status)
	}
	if r := h.do("GET", "/api/v1/orgs/acme/summary", nobody, ""); r.status != http.StatusNotFound {
		t.Fatalf("non-member must not learn the org exists: %d", r.status)
	}
	if r := h.do("GET", "/api/v1/orgs/nope/summary", alice, ""); r.status != http.StatusNotFound {
		t.Fatalf("unknown org: %d", r.status)
	}
	if r := h.do("POST", "/api/v1/orgs/acme/members", bob, `{"login":"someone"}`); r.status != http.StatusForbidden {
		t.Fatalf("member inviting: %d", r.status)
	}
	if r := h.do("PUT", "/api/v1/orgs/acme/settings", bob, `{"storagePerGBMonthUSD":1}`); r.status != http.StatusForbidden {
		t.Fatalf("member changing settings: %d", r.status)
	}
	var org struct {
		Settings struct {
			PullsPerDay float64 `json:"pullsPerDay"`
		} `json:"settings"`
	}
	h.do("PUT", "/api/v1/orgs/acme/settings", alice, `{"storagePerGBMonthUSD":0.2,"transferPerGBUSD":0,"pullsPerDay":100,"versionsKept":10}`).json(t, &org)
	if org.Settings.PullsPerDay != 100 {
		t.Fatalf("settings: %+v", org)
	}
	if r := h.do("PUT", "/api/v1/orgs/acme/settings", alice, `{"pullsPerDay":-1}`); r.status != http.StatusBadRequest {
		t.Fatalf("negative setting: %d", r.status)
	}

	var members struct {
		Members []struct {
			User struct{ ID, Login string } `json:"user"`
			Role string                     `json:"role"`
		} `json:"members"`
	}
	h.do("GET", "/api/v1/orgs/acme/members", bob, "").json(t, &members)
	ids := map[string]string{}
	for _, m := range members.Members {
		ids[m.User.Login] = m.User.ID
	}
	// A member can remove themself, but not others.
	if r := h.do("DELETE", "/api/v1/orgs/acme/members/"+ids["stranger"], bob, ""); r.status != http.StatusForbidden {
		t.Fatalf("member removing someone: %d", r.status)
	}
	if r := h.do("DELETE", "/api/v1/orgs/acme/members/"+ids["stranger"], strangerTok, ""); r.status != http.StatusOK {
		t.Fatalf("leave: %d %s", r.status, r.body)
	}
	// The last admin can neither be demoted nor leave.
	if r := h.do("PATCH", "/api/v1/orgs/acme/members/"+ids["octocat"], alice, `{"role":"member"}`); r.status != http.StatusBadRequest {
		t.Fatalf("demoting the last admin: %d %s", r.status, r.body)
	}
	if r := h.do("DELETE", "/api/v1/orgs/acme/members/"+ids["octocat"], alice, ""); r.status != http.StatusBadRequest {
		t.Fatalf("last admin leaving: %d", r.status)
	}
	if r := h.do("PATCH", "/api/v1/orgs/acme/members/"+ids["hubot"], alice, `{"role":"owner"}`); r.status != http.StatusBadRequest {
		t.Fatalf("unknown role: %d", r.status)
	}
	// Promote bob; then alice may step down and leave.
	if r := h.do("PATCH", "/api/v1/orgs/acme/members/"+ids["hubot"], alice, `{"role":"admin"}`); r.status != http.StatusOK {
		t.Fatalf("promote: %d %s", r.status, r.body)
	}
	if r := h.do("DELETE", "/api/v1/orgs/acme/members/"+ids["octocat"], alice, ""); r.status != http.StatusOK {
		t.Fatalf("admin leaving with another admin present: %d %s", r.status, r.body)
	}
	if r := h.do("GET", "/api/v1/orgs/acme/summary", alice, ""); r.status != http.StatusNotFound {
		t.Fatalf("former member still has access: %d", r.status)
	}
}
