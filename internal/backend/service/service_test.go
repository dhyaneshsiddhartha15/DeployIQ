package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// fakeStore is the three-line fake the package doc promises, plus recording.
type fakeStore struct {
	inserted []model.Optimization
	listed   struct {
		user, repo string
		limit      int
	}
	users map[string]model.User
	err   error
}

func (f *fakeStore) InsertOptimization(_ context.Context, o model.Optimization) (model.Optimization, error) {
	if f.err != nil {
		return model.Optimization{}, f.err
	}
	o.ID = "id1"
	f.inserted = append(f.inserted, o)
	return o, nil
}

func (f *fakeStore) ListOptimizations(_ context.Context, user, repo string, limit int) ([]model.Optimization, error) {
	f.listed.user, f.listed.repo, f.listed.limit = user, repo, limit
	return nil, f.err
}

func (f *fakeStore) UpsertUserByGitHubID(_ context.Context, u model.User) (model.User, error) {
	if f.err != nil {
		return model.User{}, f.err
	}
	u.ID = "u-" + u.GitHubID
	if f.users == nil {
		f.users = map[string]model.User{}
	}
	f.users[u.ID] = u
	return u, nil
}

func (f *fakeStore) GetUser(_ context.Context, id string) (model.User, error) {
	if f.err != nil {
		return model.User{}, f.err
	}
	u, ok := f.users[id]
	if !ok {
		return model.User{}, apperrors.NotFound("user")
	}
	return u, nil
}

func TestOptimizationsRequireAUser(t *testing.T) {
	s := NewOptimizations(&fakeStore{})
	if _, err := s.Create(context.Background(), model.OptimizationReport{}); apperrors.KindOf(err) != apperrors.KindUnauthenticated {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), "", 0); apperrors.KindOf(err) != apperrors.KindUnauthenticated {
		t.Fatal(err)
	}
}

func TestCreateTakesOwnerFromContextAndNormalises(t *testing.T) {
	store := &fakeStore{}
	s := NewOptimizations(store)
	ctx := WithUserID(context.Background(), "alice")
	o, err := s.Create(ctx, model.OptimizationReport{
		RepoName: "  acme/api ", DetectedStack: "go", OriginalEstSizeMB: 10, OptimizedEstSizeMB: 2,
		RulesApplied: []string{"a", "b", "a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.UserID != "alice" || o.RepoName != "acme/api" || strings.Join(o.RulesApplied, ",") != "a,b" {
		t.Fatalf("%+v", o)
	}

	store.err = errors.New("connection reset by 10.0.0.5")
	_, err = s.Create(ctx, model.OptimizationReport{RepoName: "a/b", DetectedStack: "go", OriginalEstSizeMB: 1, OptimizedEstSizeMB: 1})
	if apperrors.KindOf(err) != apperrors.KindInternal || strings.Contains(apperrors.Public(err), "10.0.0.5") {
		t.Fatalf("store error leaked: %q", apperrors.Public(err))
	}
}

func TestListLimits(t *testing.T) {
	store := &fakeStore{}
	s := NewOptimizations(store)
	ctx := WithUserID(context.Background(), "bob")
	for in, want := range map[int]int{0: DefaultListLimit, 10: 10, 10000: MaxListLimit} {
		list, err := s.List(ctx, "a/b", in)
		if err != nil || list == nil {
			t.Fatal(err)
		}
		if store.listed.limit != want || store.listed.user != "bob" || store.listed.repo != "a/b" {
			t.Errorf("limit %d -> %+v", in, store.listed)
		}
	}
	if _, err := s.List(ctx, "../etc", 1); apperrors.FieldOf(err) != "repo" {
		t.Fatal(err)
	}
}

func TestValidateReportNeverEchoesInput(t *testing.T) {
	evil := "<script>alert(1)</script>"
	for _, r := range []model.OptimizationReport{
		{RepoName: evil, DetectedStack: "go", OriginalEstSizeMB: 1, OptimizedEstSizeMB: 1},
		{RepoName: "a/b", DetectedStack: evil, OriginalEstSizeMB: 1, OptimizedEstSizeMB: 1},
		{RepoName: "a/b", DetectedStack: "go", OriginalEstSizeMB: 1, OptimizedEstSizeMB: 1, RulesApplied: []string{evil}},
	} {
		err := ValidateReport(&r)
		if err == nil || strings.Contains(apperrors.Public(err), "script") {
			t.Errorf("err=%v", err)
		}
	}
	many := model.OptimizationReport{RepoName: "a/b", DetectedStack: "go", OriginalEstSizeMB: 1, OptimizedEstSizeMB: 1, RulesApplied: make([]string, 33)}
	if apperrors.FieldOf(ValidateReport(&many)) != "rulesApplied" {
		t.Fatal("too many rules accepted")
	}
}

type fakeGitHub struct {
	user GitHubUser
	err  error
}

func (f fakeGitHub) AuthorizeURL(state, _ string) string {
	return "https://gh/authorize?state=" + state
}
func (f fakeGitHub) Exchange(context.Context, string, string) (GitHubUser, error) {
	return f.user, f.err
}

func TestSessionTokens(t *testing.T) {
	a := NewAuth("0123456789abcdef", time.Hour, &fakeStore{}, fakeGitHub{})
	tok, s := a.IssueSession("user-1")
	got, err := a.VerifySession(tok)
	if err != nil || got.UserID != "user-1" || !got.ExpiresAt.Equal(s.ExpiresAt.Truncate(time.Second)) {
		t.Fatalf("%+v %v", got, err)
	}

	parts := strings.Split(tok, ".")
	tampered := parts[0] + "." + parts[1] + "." + "eyJ1aWQiOiJhZG1pbiJ9" + "." + parts[3]
	for name, bad := range map[string]string{
		"tampered payload": tampered,
		"truncated":        strings.Join(parts[:3], "."),
		"wrong version":    "v2." + strings.Join(parts[1:], "."),
		"garbage":          "not-a-token",
		"other key": func() string {
			t2, _ := NewAuth("another-key-0000", time.Hour, nil, nil).IssueSession("user-1")
			return t2
		}(),
	} {
		if _, err := a.VerifySession(bad); apperrors.KindOf(err) != apperrors.KindUnauthenticated {
			t.Errorf("%s accepted", name)
		}
	}

	// A login state is signed with the same key but must not pass as a session.
	state, _, err := a.NewLoginState("web", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.VerifySession(state); err == nil {
		t.Fatal("login state accepted as a session token")
	}

	a.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	if _, err := a.VerifySession(tok); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired token accepted: %v", err)
	}
}

func TestLoginState(t *testing.T) {
	a := NewAuth("0123456789abcdef", time.Hour, &fakeStore{}, fakeGitHub{})
	cliState := strings.Repeat("ab", 16)
	state, nonce, err := a.NewLoginState("cli", 51234, cliState)
	if err != nil {
		t.Fatal(err)
	}
	ls, err := a.VerifyLoginState(state, nonce)
	if err != nil || ls.Client != "cli" || ls.Port != 51234 || ls.CLIState != cliState {
		t.Fatalf("%+v %v", ls, err)
	}
	if _, err := a.VerifyLoginState(state, "wrong-nonce"); err == nil {
		t.Fatal("nonce mismatch accepted")
	}
	if _, err := a.VerifyLoginState(state, ""); err == nil {
		t.Fatal("missing cookie accepted")
	}
	session, _ := a.IssueSession("u")
	if _, err := a.VerifyLoginState(session, nonce); err == nil {
		t.Fatal("session accepted as login state")
	}
	a.SetClock(func() time.Time { return time.Now().Add(time.Hour) })
	if _, err := a.VerifyLoginState(state, nonce); err == nil {
		t.Fatal("stale state accepted")
	}

	for _, c := range []struct {
		client string
		port   int
		state  string
	}{{"cli", 80, cliState}, {"cli", 70000, cliState}, {"cli", 5000, "short"}, {"cli", 5000, strings.Repeat("z", 32)}, {"tv", 0, ""}} {
		if _, _, err := a.NewLoginState(c.client, c.port, c.state); apperrors.KindOf(err) != apperrors.KindInvalidArgument {
			t.Errorf("%+v accepted", c)
		}
	}
	// web ignores CLI-only parameters rather than signing them in.
	state, nonce, _ = a.NewLoginState("web", 5000, cliState)
	if ls, _ := a.VerifyLoginState(state, nonce); ls.Port != 0 || ls.CLIState != "" {
		t.Fatalf("%+v", ls)
	}
}

func TestCompleteLogin(t *testing.T) {
	store := &fakeStore{}
	a := NewAuth("0123456789abcdef", time.Hour, store, fakeGitHub{user: GitHubUser{ID: 42, Login: "octo", Email: "o@x"}})
	u, tok, err := a.CompleteLogin(context.Background(), "code", "https://x/cb")
	if err != nil || u.GitHubID != "42" || u.Plan != model.PlanFree {
		t.Fatalf("%+v %v", u, err)
	}
	if s, err := a.VerifySession(tok); err != nil || s.UserID != u.ID {
		t.Fatal("issued token does not verify")
	}
	if _, _, err := a.CompleteLogin(context.Background(), " ", ""); apperrors.FieldOf(err) != "code" {
		t.Fatal(err)
	}

	down := NewAuth("0123456789abcdef", time.Hour, store, fakeGitHub{err: errors.New("dial tcp: i/o timeout")})
	if _, _, err := down.CompleteLogin(context.Background(), "c", ""); apperrors.KindOf(err) != apperrors.KindUnavailable {
		t.Fatalf("github outage should be Unavailable, got %v", err)
	}
	rejected := NewAuth("0123456789abcdef", time.Hour, store, fakeGitHub{err: apperrors.Unauthenticated("bad code")})
	if _, _, err := rejected.CompleteLogin(context.Background(), "c", ""); apperrors.KindOf(err) != apperrors.KindUnauthenticated {
		t.Fatal(err)
	}
	noID := NewAuth("0123456789abcdef", time.Hour, store, fakeGitHub{user: GitHubUser{}})
	if _, _, err := noID.CompleteLogin(context.Background(), "c", ""); err == nil {
		t.Fatal("profile without id accepted")
	}
}

func TestUsersMe(t *testing.T) {
	store := &fakeStore{users: map[string]model.User{"u1": {ID: "u1", Login: "octo"}}}
	s := NewUsers(store)
	if _, err := s.Me(context.Background()); apperrors.KindOf(err) != apperrors.KindUnauthenticated {
		t.Fatal(err)
	}
	u, err := s.Me(WithUserID(context.Background(), "u1"))
	if err != nil || u.Login != "octo" {
		t.Fatal(err)
	}
	if _, err := s.Me(WithUserID(context.Background(), "gone")); apperrors.KindOf(err) != apperrors.KindUnauthenticated {
		t.Fatal(err)
	}
	store.err = errors.New("boom")
	if _, err := s.Me(WithUserID(context.Background(), "u1")); apperrors.KindOf(err) != apperrors.KindInternal {
		t.Fatal(err)
	}
}
