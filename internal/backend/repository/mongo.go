package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/service"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// Collection names (Phase 7.1).
const (
	CollOptimizations = "optimizations"
	CollUsers         = "users"
	CollOrganizations = "organizations"
	CollMemberships   = "memberships"
	CollInvitations   = "invitations"
)

// Mongo implements service.Store on MongoDB.
type Mongo struct {
	client *mongo.Client
	db     *mongo.Database
	now    func() time.Time
}

var _ service.Store = (*Mongo)(nil)

// optimizationDoc is the stored shape of model.Optimization. The domain type
// carries no bson tags (Phase 5.1); this is the only place the two meet.
type optimizationDoc struct {
	ID                 bson.ObjectID `bson:"_id,omitempty"`
	UserID             bson.ObjectID `bson:"userId"`
	RepoName           string        `bson:"repoName"`
	DetectedStack      string        `bson:"detectedStack"`
	OriginalEstSizeMB  int64         `bson:"originalEstSizeMB"`
	OptimizedEstSizeMB int64         `bson:"optimizedEstSizeMB"`
	RulesApplied       []string      `bson:"rulesApplied"`
	CreatedAt          time.Time     `bson:"createdAt"`
	OrgID              bson.ObjectID `bson:"orgId,omitempty"`
	ReportedBy         string        `bson:"reportedBy,omitempty"`
}

type userDoc struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	GitHubID  string        `bson:"githubId,omitempty"`
	Provider  string        `bson:"provider,omitempty"`
	Subject   string        `bson:"subject,omitempty"`
	Login     string        `bson:"login,omitempty"`
	Email     string        `bson:"email,omitempty"`
	Name      string        `bson:"name,omitempty"`
	Plan      string        `bson:"plan"`
	CreatedAt time.Time     `bson:"createdAt"`
	UpdatedAt time.Time     `bson:"updatedAt"`
}

// Connect dials MongoDB, verifies it answers within timeout, and ensures the
// Phase 7.2 indexes exist. A wrong URI fails here, at startup, not on the first
// request (Phase 5.6: fail fast).
func Connect(ctx context.Context, uri, database string, timeout time.Duration) (*Mongo, error) {
	opts := options.Client().ApplyURI(uri).
		SetConnectTimeout(timeout).
		SetServerSelectionTimeout(timeout).
		SetAppName("deployiq-api")
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("repository: connecting to mongodb: %w", err)
	}
	m := &Mongo{client: client, db: client.Database(database), now: time.Now}

	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := m.Ping(pctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("repository: mongodb did not answer within %s: %w", timeout, err)
	}
	if err := m.EnsureIndexes(pctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return m, nil
}

// EnsureIndexes creates the three Phase 7.2 indexes. Idempotent: creating an
// index that already exists with the same spec is a no-op.
func (m *Mongo) EnsureIndexes(ctx context.Context) error {
	_, err := m.db.Collection(CollOptimizations).Indexes().CreateMany(ctx, []mongo.IndexModel{
		// The dashboard's main query: recent history for this user.
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "createdAt", Value: -1}}, Options: options.Index().SetName("userId_createdAt")},
		// The repo-detail page.
		{Keys: bson.D{{Key: "repoName", Value: 1}}, Options: options.Index().SetName("repoName")},
	})
	if err != nil {
		return fmt.Errorf("repository: creating optimizations indexes: %w", err)
	}
	// Organisation history (Phase 13.1 team accounts). Partial: personal
	// reports carry no orgId and need no entry.
	_, err = m.db.Collection(CollOptimizations).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "orgId", Value: 1}, {Key: "createdAt", Value: -1}},
		Options: options.Index().SetName("orgId_createdAt").
			SetPartialFilterExpression(bson.D{{Key: "orgId", Value: bson.D{{Key: "$exists", Value: true}}}}),
	})
	if err != nil {
		return fmt.Errorf("repository: creating optimizations org index: %w", err)
	}

	// The original Phase 7.2 index was unique on githubId for every
	// document; SSO accounts have no githubId, so it becomes partial. The old
	// index is dropped once — it would reject the second SSO user.
	users := m.db.Collection(CollUsers).Indexes()
	_ = users.DropOne(ctx, "githubId_unique")
	_, err = users.CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "githubId", Value: 1}}, Options: options.Index().SetName("githubId_unique_partial").SetUnique(true).
			SetPartialFilterExpression(bson.D{{Key: "githubId", Value: bson.D{{Key: "$exists", Value: true}}}})},
		{Keys: bson.D{{Key: "provider", Value: 1}, {Key: "subject", Value: 1}}, Options: options.Index().SetName("provider_subject_unique").SetUnique(true).
			SetPartialFilterExpression(bson.D{{Key: "subject", Value: bson.D{{Key: "$exists", Value: true}}}})},
		{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetName("email")},
		{Keys: bson.D{{Key: "login", Value: 1}}, Options: options.Index().SetName("login")},
	})
	if err != nil {
		return fmt.Errorf("repository: creating users indexes: %w", err)
	}

	if _, err := m.db.Collection(CollOrganizations).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "slug", Value: 1}}, Options: options.Index().SetName("slug_unique").SetUnique(true),
	}); err != nil {
		return fmt.Errorf("repository: creating organizations index: %w", err)
	}
	if _, err := m.db.Collection(CollMemberships).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "orgId", Value: 1}, {Key: "userId", Value: 1}}, Options: options.Index().SetName("org_user_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "userId", Value: 1}}, Options: options.Index().SetName("userId")},
	}); err != nil {
		return fmt.Errorf("repository: creating memberships indexes: %w", err)
	}
	if _, err := m.db.Collection(CollInvitations).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "orgId", Value: 1}}, Options: options.Index().SetName("orgId")},
		{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetName("email")},
		{Keys: bson.D{{Key: "login", Value: 1}}, Options: options.Index().SetName("login")},
	}); err != nil {
		return fmt.Errorf("repository: creating invitations indexes: %w", err)
	}
	return nil
}

// Ping checks the primary is reachable.
func (m *Mongo) Ping(ctx context.Context) error { return m.client.Ping(ctx, readpref.Primary()) }

// Close disconnects.
func (m *Mongo) Close(ctx context.Context) error { return m.client.Disconnect(ctx) }

// InsertOptimization stores one record.
func (m *Mongo) InsertOptimization(ctx context.Context, o model.Optimization) (model.Optimization, error) {
	uid, err := bson.ObjectIDFromHex(o.UserID)
	if err != nil {
		return model.Optimization{}, apperrors.Unauthenticated("session refers to an unknown account")
	}
	doc := optimizationDoc{
		UserID:             uid,
		RepoName:           o.RepoName,
		DetectedStack:      o.DetectedStack,
		OriginalEstSizeMB:  o.OriginalEstSizeMB,
		OptimizedEstSizeMB: o.OptimizedEstSizeMB,
		RulesApplied:       o.RulesApplied,
		CreatedAt:          m.now().UTC().Truncate(time.Millisecond),
		ReportedBy:         o.ReportedBy,
	}
	if o.OrgID != "" {
		oid, err := bson.ObjectIDFromHex(o.OrgID)
		if err != nil {
			return model.Optimization{}, apperrors.NotFound("organization")
		}
		doc.OrgID = oid
	}
	if doc.RulesApplied == nil {
		doc.RulesApplied = []string{}
	}
	res, err := m.db.Collection(CollOptimizations).InsertOne(ctx, doc)
	if err != nil {
		return model.Optimization{}, classify("insert optimization", err)
	}
	doc.ID = res.InsertedID.(bson.ObjectID)
	return doc.model(), nil
}

// ListOptimizations runs the per-user query, newest first. Values go through
// the driver as typed bson, never string-built filters (Phase 9.2).
func (m *Mongo) ListOptimizations(ctx context.Context, userID, repo string, limit int) ([]model.Optimization, error) {
	uid, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return []model.Optimization{}, nil
	}
	filter := bson.D{{Key: "userId", Value: uid}}
	if repo != "" {
		filter = append(filter, bson.E{Key: "repoName", Value: repo})
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(limit))
	cur, err := m.db.Collection(CollOptimizations).Find(ctx, filter, opts)
	if err != nil {
		return nil, classify("list optimizations", err)
	}
	defer cur.Close(ctx)
	out := []model.Optimization{}
	for cur.Next(ctx) {
		var d optimizationDoc
		if err := cur.Decode(&d); err != nil {
			return nil, classify("decode optimization", err)
		}
		out = append(out, d.model())
	}
	if err := cur.Err(); err != nil {
		return nil, classify("list optimizations", err)
	}
	return out, nil
}

// UpsertUserByGitHubID creates or refreshes an account atomically.
func (m *Mongo) UpsertUserByGitHubID(ctx context.Context, u model.User) (model.User, error) {
	now := m.now().UTC().Truncate(time.Millisecond)
	plan := string(u.Plan)
	if plan == "" {
		plan = string(model.PlanFree)
	}
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "login", Value: u.Login}, {Key: "email", Value: u.Email}, {Key: "updatedAt", Value: now}}},
		{Key: "$setOnInsert", Value: bson.D{{Key: "githubId", Value: u.GitHubID}, {Key: "provider", Value: "github"}, {Key: "plan", Value: plan}, {Key: "createdAt", Value: now}}},
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	var d userDoc
	err := m.db.Collection(CollUsers).FindOneAndUpdate(ctx, bson.D{{Key: "githubId", Value: u.GitHubID}}, update, opts).Decode(&d)
	if err != nil {
		return model.User{}, classify("upsert user", err)
	}
	return d.model(), nil
}

// GetUser loads one account.
func (m *Mongo) GetUser(ctx context.Context, id string) (model.User, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return model.User{}, apperrors.NotFound("user")
	}
	var d userDoc
	err = m.db.Collection(CollUsers).FindOne(ctx, bson.D{{Key: "_id", Value: oid}}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return model.User{}, apperrors.NotFound("user")
	}
	if err != nil {
		return model.User{}, classify("get user", err)
	}
	return d.model(), nil
}

func (d optimizationDoc) model() model.Optimization {
	return model.Optimization{
		ID:                 d.ID.Hex(),
		UserID:             d.UserID.Hex(),
		RepoName:           d.RepoName,
		DetectedStack:      d.DetectedStack,
		OriginalEstSizeMB:  d.OriginalEstSizeMB,
		OptimizedEstSizeMB: d.OptimizedEstSizeMB,
		RulesApplied:       d.RulesApplied,
		CreatedAt:          d.CreatedAt,
		ReportedBy:         d.ReportedBy,
		OrgID:              hexOrEmpty(d.OrgID),
	}
}

func (d userDoc) model() model.User {
	return model.User{
		ID: d.ID.Hex(), GitHubID: d.GitHubID, Login: d.Login, Email: d.Email,
		Plan: model.Plan(d.Plan), CreatedAt: d.CreatedAt,
		Provider: d.Provider, Subject: d.Subject, Name: d.Name,
	}
}

// classify maps driver errors onto the error kinds. A timeout or lost
// connection is Unavailable (Phase 12.2: an Atlas outage is waited out); a
// duplicate key is AlreadyExists; the rest are internal.
func classify(op string, err error) error {
	switch {
	case mongo.IsDuplicateKeyError(err):
		return apperrors.AlreadyExists("record")
	case mongo.IsTimeout(err), mongo.IsNetworkError(err), errors.Is(err, context.DeadlineExceeded):
		return apperrors.Join(apperrors.Unavailable("database"), err)
	}
	return apperrors.Internal(op, err)
}
