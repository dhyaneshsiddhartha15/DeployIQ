package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/service"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// contract runs the same behavioural checks against any service.Store, so the
// in-memory store used by API tests cannot drift from the MongoDB one.
func contract(t *testing.T, s service.Store) {
	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	u, err := s.UpsertUserByGitHubID(ctx, model.User{GitHubID: "1001", Login: "octo", Email: "a@x"})
	if err != nil || u.ID == "" || u.Plan != model.PlanFree || u.CreatedAt.IsZero() {
		t.Fatalf("create user: %+v %v", u, err)
	}
	again, err := s.UpsertUserByGitHubID(ctx, model.User{GitHubID: "1001", Login: "octo-renamed", Email: "b@x"})
	if err != nil || again.ID != u.ID || again.Login != "octo-renamed" || !again.CreatedAt.Equal(u.CreatedAt) {
		t.Fatalf("upsert must refresh profile, keep id and createdAt: %+v vs %+v", again, u)
	}
	other, _ := s.UpsertUserByGitHubID(ctx, model.User{GitHubID: "2002", Login: "hubot"})

	got, err := s.GetUser(ctx, u.ID)
	if err != nil || got.Login != "octo-renamed" {
		t.Fatalf("get: %+v %v", got, err)
	}
	for _, missing := range []string{"ffffffffffffffffffffffff", "not-an-id"} {
		if _, err := s.GetUser(ctx, missing); apperrors.KindOf(err) != apperrors.KindNotFound {
			t.Fatalf("missing user %q: %v", missing, err)
		}
	}

	for i, repo := range []string{"acme/api", "acme/web", "acme/api"} {
		o, err := s.InsertOptimization(ctx, model.Optimization{
			UserID: u.ID, RepoName: repo, DetectedStack: "go",
			OriginalEstSizeMB: int64(100 + i), OptimizedEstSizeMB: 10, RulesApplied: []string{"multi-stage-build"},
		})
		if err != nil || o.ID == "" || o.CreatedAt.IsZero() {
			t.Fatalf("insert: %+v %v", o, err)
		}
		time.Sleep(5 * time.Millisecond) // distinct createdAt for ordering
	}
	if _, err := s.InsertOptimization(ctx, model.Optimization{UserID: other.ID, RepoName: "hubot/x", DetectedStack: "node", OriginalEstSizeMB: 1, OptimizedEstSizeMB: 1}); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListOptimizations(ctx, u.ID, "", 50)
	if err != nil || len(all) != 3 {
		t.Fatalf("list: %d %v", len(all), err)
	}
	if all[0].OriginalEstSizeMB != 102 || all[2].OriginalEstSizeMB != 100 {
		t.Fatalf("not newest first: %v %v", all[0].OriginalEstSizeMB, all[2].OriginalEstSizeMB)
	}
	for _, o := range all {
		if o.UserID != u.ID {
			t.Fatal("foreign record returned")
		}
	}
	api, _ := s.ListOptimizations(ctx, u.ID, "acme/api", 50)
	if len(api) != 2 {
		t.Fatalf("repo filter: %d", len(api))
	}
	one, _ := s.ListOptimizations(ctx, u.ID, "", 1)
	if len(one) != 1 {
		t.Fatalf("limit: %d", len(one))
	}
	none, err := s.ListOptimizations(ctx, "not-an-id", "", 10)
	if err != nil || len(none) != 0 {
		t.Fatalf("unknown user: %v %v", none, err)
	}
}

func TestMemoryContract(t *testing.T) {
	contract(t, NewMemory())
}

func TestMemoryRejectsUnknownOwner(t *testing.T) {
	_, err := NewMemory().InsertOptimization(context.Background(), model.Optimization{UserID: "nobody"})
	if apperrors.KindOf(err) != apperrors.KindUnauthenticated {
		t.Fatal(err)
	}
}

// TestMongoContract runs against a real MongoDB when DEPLOYIQ_TEST_MONGO_URI is
// set, e.g.:
//
//	docker run -d --rm -p 127.0.0.1:27017:27017 mongo:7
//	DEPLOYIQ_TEST_MONGO_URI=mongodb://127.0.0.1:27017 go test ./internal/backend/repository
func TestMongoContract(t *testing.T) {
	uri := os.Getenv("DEPLOYIQ_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEPLOYIQ_TEST_MONGO_URI not set")
	}
	ctx := context.Background()
	db := "deployiq_test_" + time.Now().Format("20060102150405")
	m, err := Connect(ctx, uri, db, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = m.db.Drop(context.Background())
		_ = m.Close(context.Background())
	})
	contract(t, m)

	// Indexes from Phase 7.2 exist, and the githubId one is unique.
	specs, err := m.db.Collection(CollUsers).Indexes().ListSpecifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	unique := false
	for _, s := range specs {
		if s.Name == "githubId_unique" && s.Unique != nil && *s.Unique {
			unique = true
		}
	}
	if !unique {
		t.Fatal("users.githubId unique index missing")
	}
	specs, _ = m.db.Collection(CollOptimizations).Indexes().ListSpecifications(ctx)
	names := map[string]bool{}
	for _, s := range specs {
		names[s.Name] = true
	}
	if !names["userId_createdAt"] || !names["repoName"] {
		t.Fatalf("optimizations indexes: %v", names)
	}
	// EnsureIndexes is idempotent.
	if err := m.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestMongoConnectFailsFast(t *testing.T) {
	start := time.Now()
	_, err := Connect(context.Background(), "mongodb://127.0.0.1:1", "x", 500*time.Millisecond)
	if err == nil {
		t.Fatal("connect to a closed port succeeded")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("connect did not respect the timeout")
	}
}
