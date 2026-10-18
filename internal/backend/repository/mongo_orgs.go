package repository

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

type orgDoc struct {
	ID        bson.ObjectID     `bson:"_id,omitempty"`
	Slug      string            `bson:"slug"`
	Name      string            `bson:"name"`
	Settings  model.OrgSettings `bson:"settings"`
	CreatedAt time.Time         `bson:"createdAt"`
}

type membershipDoc struct {
	OrgID     bson.ObjectID `bson:"orgId"`
	UserID    bson.ObjectID `bson:"userId"`
	Role      string        `bson:"role"`
	CreatedAt time.Time     `bson:"createdAt"`
}

type invitationDoc struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	OrgID     bson.ObjectID `bson:"orgId"`
	Email     string        `bson:"email,omitempty"`
	Login     string        `bson:"login,omitempty"`
	LoginLC   string        `bson:"loginLower,omitempty"`
	Role      string        `bson:"role"`
	InvitedBy string        `bson:"invitedBy"`
	CreatedAt time.Time     `bson:"createdAt"`
}

func (d orgDoc) model() model.Organization {
	return model.Organization{ID: d.ID.Hex(), Slug: d.Slug, Name: d.Name, Settings: d.Settings, CreatedAt: d.CreatedAt}
}

func (d invitationDoc) model() model.Invitation {
	return model.Invitation{ID: d.ID.Hex(), OrgID: d.OrgID.Hex(), Email: d.Email, Login: d.Login, Role: model.Role(d.Role), InvitedBy: d.InvitedBy, CreatedAt: d.CreatedAt}
}

func hexOrEmpty(id bson.ObjectID) string {
	if id.IsZero() {
		return ""
	}
	return id.Hex()
}

func oid(s, what string) (bson.ObjectID, error) {
	id, err := bson.ObjectIDFromHex(s)
	if err != nil {
		return bson.ObjectID{}, apperrors.NotFound(what)
	}
	return id, nil
}

// UpsertUserBySubject creates or refreshes an SSO account atomically.
func (m *Mongo) UpsertUserBySubject(ctx context.Context, u model.User) (model.User, error) {
	now := m.now().UTC().Truncate(time.Millisecond)
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "email", Value: u.Email}, {Key: "name", Value: u.Name}, {Key: "updatedAt", Value: now}}},
		{Key: "$setOnInsert", Value: bson.D{{Key: "provider", Value: u.Provider}, {Key: "subject", Value: u.Subject},
			{Key: "plan", Value: string(model.PlanFree)}, {Key: "createdAt", Value: now}}},
	}
	var d userDoc
	err := m.db.Collection(CollUsers).FindOneAndUpdate(ctx,
		bson.D{{Key: "provider", Value: u.Provider}, {Key: "subject", Value: u.Subject}}, update,
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&d)
	if err != nil {
		return model.User{}, classify("upsert sso user", err)
	}
	return d.model(), nil
}

// FindUser looks an account up by email or GitHub login.
func (m *Mongo) FindUser(ctx context.Context, email, login string) (model.User, error) {
	var or bson.A
	if email != "" {
		or = append(or, bson.D{{Key: "email", Value: strings.ToLower(email)}})
	}
	if login != "" {
		// Logins are case-insensitive on GitHub; a collation keeps the
		// query typed rather than building a regex from user input.
		or = append(or, bson.D{{Key: "login", Value: login}})
	}
	if len(or) == 0 {
		return model.User{}, apperrors.NotFound("user")
	}
	var d userDoc
	err := m.db.Collection(CollUsers).FindOne(ctx, bson.D{{Key: "$or", Value: or}},
		options.FindOne().SetCollation(&options.Collation{Locale: "en", Strength: 2})).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return model.User{}, apperrors.NotFound("user")
	}
	if err != nil {
		return model.User{}, classify("find user", err)
	}
	return d.model(), nil
}

// ListOrgOptimizations returns an organisation's reports, newest first.
func (m *Mongo) ListOrgOptimizations(ctx context.Context, orgID, repo string, limit int) ([]model.Optimization, error) {
	id, err := bson.ObjectIDFromHex(orgID)
	if err != nil {
		return []model.Optimization{}, nil
	}
	filter := bson.D{{Key: "orgId", Value: id}}
	if repo != "" {
		filter = append(filter, bson.E{Key: "repoName", Value: repo})
	}
	cur, err := m.db.Collection(CollOptimizations).Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, classify("list org optimizations", err)
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
	return out, classifyNil(cur.Err(), "list org optimizations")
}

// CreateOrg inserts the organisation, then its first admin.
func (m *Mongo) CreateOrg(ctx context.Context, o model.Organization, creatorID string) (model.Organization, error) {
	uid, err := oid(creatorID, "user")
	if err != nil {
		return model.Organization{}, err
	}
	d := orgDoc{Slug: o.Slug, Name: o.Name, Settings: o.Settings, CreatedAt: m.now().UTC().Truncate(time.Millisecond)}
	res, err := m.db.Collection(CollOrganizations).InsertOne(ctx, d)
	if err != nil {
		return model.Organization{}, classify("create org", err)
	}
	d.ID = res.InsertedID.(bson.ObjectID)
	if _, err := m.db.Collection(CollMemberships).InsertOne(ctx, membershipDoc{OrgID: d.ID, UserID: uid, Role: string(model.RoleAdmin), CreatedAt: d.CreatedAt}); err != nil {
		// Without its admin the organisation is unmanageable; undo it.
		_, _ = m.db.Collection(CollOrganizations).DeleteOne(ctx, bson.D{{Key: "_id", Value: d.ID}})
		return model.Organization{}, classify("create org admin", err)
	}
	return d.model(), nil
}

// GetOrgBySlug loads an organisation.
func (m *Mongo) GetOrgBySlug(ctx context.Context, slug string) (model.Organization, error) {
	return m.findOrg(ctx, bson.D{{Key: "slug", Value: slug}})
}

// GetOrg loads an organisation by id.
func (m *Mongo) GetOrg(ctx context.Context, id string) (model.Organization, error) {
	o, err := oid(id, "organization")
	if err != nil {
		return model.Organization{}, err
	}
	return m.findOrg(ctx, bson.D{{Key: "_id", Value: o}})
}

func (m *Mongo) findOrg(ctx context.Context, filter bson.D) (model.Organization, error) {
	var d orgDoc
	err := m.db.Collection(CollOrganizations).FindOne(ctx, filter).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return model.Organization{}, apperrors.NotFound("organization")
	}
	if err != nil {
		return model.Organization{}, classify("get org", err)
	}
	return d.model(), nil
}

// UpdateOrgSettings replaces the settings.
func (m *Mongo) UpdateOrgSettings(ctx context.Context, orgID string, s model.OrgSettings) (model.Organization, error) {
	id, err := oid(orgID, "organization")
	if err != nil {
		return model.Organization{}, err
	}
	var d orgDoc
	err = m.db.Collection(CollOrganizations).FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: id}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "settings", Value: s}}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return model.Organization{}, apperrors.NotFound("organization")
	}
	if err != nil {
		return model.Organization{}, classify("update settings", err)
	}
	return d.model(), nil
}

// ListMemberships returns a user's memberships.
func (m *Mongo) ListMemberships(ctx context.Context, userID string) ([]model.Membership, error) {
	uid, err := oid(userID, "user")
	if err != nil {
		return nil, nil
	}
	cur, err := m.db.Collection(CollMemberships).Find(ctx, bson.D{{Key: "userId", Value: uid}})
	if err != nil {
		return nil, classify("list memberships", err)
	}
	defer cur.Close(ctx)
	var out []model.Membership
	for cur.Next(ctx) {
		var d membershipDoc
		if err := cur.Decode(&d); err != nil {
			return nil, classify("decode membership", err)
		}
		out = append(out, model.Membership{OrgID: d.OrgID.Hex(), UserID: d.UserID.Hex(), Role: model.Role(d.Role), CreatedAt: d.CreatedAt})
	}
	return out, classifyNil(cur.Err(), "list memberships")
}

// GetMembership loads one membership.
func (m *Mongo) GetMembership(ctx context.Context, orgID, userID string) (model.Membership, error) {
	o, err := oid(orgID, "membership")
	if err != nil {
		return model.Membership{}, err
	}
	u, err := oid(userID, "membership")
	if err != nil {
		return model.Membership{}, err
	}
	var d membershipDoc
	err = m.db.Collection(CollMemberships).FindOne(ctx, bson.D{{Key: "orgId", Value: o}, {Key: "userId", Value: u}}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return model.Membership{}, apperrors.NotFound("membership")
	}
	if err != nil {
		return model.Membership{}, classify("get membership", err)
	}
	return model.Membership{OrgID: orgID, UserID: userID, Role: model.Role(d.Role), CreatedAt: d.CreatedAt}, nil
}

// UpsertMembership adds or changes a membership.
func (m *Mongo) UpsertMembership(ctx context.Context, ms model.Membership) error {
	o, err := oid(ms.OrgID, "organization")
	if err != nil {
		return err
	}
	u, err := oid(ms.UserID, "user")
	if err != nil {
		return err
	}
	_, err = m.db.Collection(CollMemberships).UpdateOne(ctx, bson.D{{Key: "orgId", Value: o}, {Key: "userId", Value: u}},
		bson.D{
			{Key: "$set", Value: bson.D{{Key: "role", Value: string(ms.Role)}}},
			{Key: "$setOnInsert", Value: bson.D{{Key: "createdAt", Value: m.now().UTC().Truncate(time.Millisecond)}}},
		}, options.UpdateOne().SetUpsert(true))
	return classifyNil(err, "upsert membership")
}

// DeleteMembership removes a membership.
func (m *Mongo) DeleteMembership(ctx context.Context, orgID, userID string) error {
	o, err := oid(orgID, "membership")
	if err != nil {
		return err
	}
	u, err := oid(userID, "membership")
	if err != nil {
		return err
	}
	_, err = m.db.Collection(CollMemberships).DeleteOne(ctx, bson.D{{Key: "orgId", Value: o}, {Key: "userId", Value: u}})
	return classifyNil(err, "delete membership")
}

// ListMembers joins memberships with users.
func (m *Mongo) ListMembers(ctx context.Context, orgID string) ([]model.Member, error) {
	o, err := oid(orgID, "organization")
	if err != nil {
		return []model.Member{}, nil
	}
	cur, err := m.db.Collection(CollMemberships).Find(ctx, bson.D{{Key: "orgId", Value: o}})
	if err != nil {
		return nil, classify("list members", err)
	}
	defer cur.Close(ctx)
	roles := map[bson.ObjectID]string{}
	var ids bson.A
	for cur.Next(ctx) {
		var d membershipDoc
		if err := cur.Decode(&d); err != nil {
			return nil, classify("decode membership", err)
		}
		roles[d.UserID] = d.Role
		ids = append(ids, d.UserID)
	}
	out := []model.Member{}
	if len(ids) == 0 {
		return out, nil
	}
	ucur, err := m.db.Collection(CollUsers).Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}})
	if err != nil {
		return nil, classify("list member users", err)
	}
	defer ucur.Close(ctx)
	for ucur.Next(ctx) {
		var d userDoc
		if err := ucur.Decode(&d); err != nil {
			return nil, classify("decode user", err)
		}
		out = append(out, model.Member{User: d.model(), Role: model.Role(roles[d.ID])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].User.Display() < out[j].User.Display() })
	return out, classifyNil(ucur.Err(), "list member users")
}

// CreateInvitation records an invitation, refusing a duplicate.
func (m *Mongo) CreateInvitation(ctx context.Context, inv model.Invitation) (model.Invitation, error) {
	o, err := oid(inv.OrgID, "organization")
	if err != nil {
		return model.Invitation{}, err
	}
	coll := m.db.Collection(CollInvitations)
	// Empty fields are omitted from the stored document, so match on
	// whichever identifier this invitation actually carries.
	dup := bson.D{{Key: "orgId", Value: o}}
	if inv.Email != "" {
		dup = append(dup, bson.E{Key: "email", Value: inv.Email})
	} else {
		dup = append(dup, bson.E{Key: "loginLower", Value: strings.ToLower(inv.Login)})
	}
	if n, err := coll.CountDocuments(ctx, dup); err == nil && n > 0 {
		return model.Invitation{}, apperrors.AlreadyExists("invitation")
	}
	d := invitationDoc{OrgID: o, Email: inv.Email, Login: inv.Login, LoginLC: strings.ToLower(inv.Login), Role: string(inv.Role),
		InvitedBy: inv.InvitedBy, CreatedAt: m.now().UTC().Truncate(time.Millisecond)}
	res, err := coll.InsertOne(ctx, d)
	if err != nil {
		return model.Invitation{}, classify("create invitation", err)
	}
	d.ID = res.InsertedID.(bson.ObjectID)
	return d.model(), nil
}

// ListInvitations returns pending invitations, oldest first.
func (m *Mongo) ListInvitations(ctx context.Context, orgID string) ([]model.Invitation, error) {
	o, err := oid(orgID, "organization")
	if err != nil {
		return []model.Invitation{}, nil
	}
	cur, err := m.db.Collection(CollInvitations).Find(ctx, bson.D{{Key: "orgId", Value: o}}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}))
	if err != nil {
		return nil, classify("list invitations", err)
	}
	defer cur.Close(ctx)
	out := []model.Invitation{}
	for cur.Next(ctx) {
		var d invitationDoc
		if err := cur.Decode(&d); err != nil {
			return nil, classify("decode invitation", err)
		}
		out = append(out, d.model())
	}
	return out, classifyNil(cur.Err(), "list invitations")
}

// DeleteInvitation withdraws one invitation of the organisation.
func (m *Mongo) DeleteInvitation(ctx context.Context, orgID, id string) error {
	o, err := oid(orgID, "invitation")
	if err != nil {
		return err
	}
	i, err := oid(id, "invitation")
	if err != nil {
		return err
	}
	res, err := m.db.Collection(CollInvitations).DeleteOne(ctx, bson.D{{Key: "_id", Value: i}, {Key: "orgId", Value: o}})
	if err != nil {
		return classify("delete invitation", err)
	}
	if res.DeletedCount == 0 {
		return apperrors.NotFound("invitation")
	}
	return nil
}

// ClaimInvitations deletes and returns invitations for this person.
func (m *Mongo) ClaimInvitations(ctx context.Context, email, login string) ([]model.Invitation, error) {
	var or bson.A
	if email != "" {
		or = append(or, bson.D{{Key: "email", Value: strings.ToLower(email)}})
	}
	if login != "" {
		or = append(or, bson.D{{Key: "loginLower", Value: strings.ToLower(login)}})
	}
	if len(or) == 0 {
		return nil, nil
	}
	coll := m.db.Collection(CollInvitations)
	var out []model.Invitation
	for {
		var d invitationDoc
		err := coll.FindOneAndDelete(ctx, bson.D{{Key: "$or", Value: or}}).Decode(&d)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return out, nil
		}
		if err != nil {
			return out, classify("claim invitations", err)
		}
		out = append(out, d.model())
	}
}

func classifyNil(err error, op string) error {
	if err == nil {
		return nil
	}
	return classify(op, err)
}
