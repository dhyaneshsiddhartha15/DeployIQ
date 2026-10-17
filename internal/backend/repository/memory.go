package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/service"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
)

// MemoryURI selects the in-memory store: DEPLOYIQ_MONGO_URI=memory://.
// For local development and tests only — nothing survives a restart, and the
// server logs a warning when it is used.
const MemoryURI = "memory://"

// Memory implements service.Store in process. It mirrors the Mongo
// implementation's semantics (newest-first ordering, upsert by GitHub id,
// 24-hex ids) so API tests exercise the same contract.
type Memory struct {
	mu    sync.RWMutex
	opts  []model.Optimization
	users map[string]model.User // by id
	now   func() time.Time
}

var _ service.Store = (*Memory)(nil)

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{users: map[string]model.User{}, now: time.Now}
}

// SetClock replaces the time source, for tests.
func (m *Memory) SetClock(now func() time.Time) { m.now = now }

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Ping always succeeds.
func (m *Memory) Ping(context.Context) error { return nil }

// Close is a no-op.
func (m *Memory) Close(context.Context) error { return nil }

// InsertOptimization stores a copy.
func (m *Memory) InsertOptimization(_ context.Context, o model.Optimization) (model.Optimization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[o.UserID]; !ok {
		return model.Optimization{}, apperrors.Unauthenticated("session refers to an unknown account")
	}
	o.ID = newID()
	o.CreatedAt = m.now().UTC().Truncate(time.Millisecond)
	o.RulesApplied = append([]string{}, o.RulesApplied...)
	m.opts = append(m.opts, o)
	return o, nil
}

// ListOptimizations filters by owner (always) and repo (optionally).
func (m *Memory) ListOptimizations(_ context.Context, userID, repo string, limit int) ([]model.Optimization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []model.Optimization{}
	for i := len(m.opts) - 1; i >= 0; i-- {
		o := m.opts[i]
		if o.UserID != userID || (repo != "" && o.RepoName != repo) {
			continue
		}
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// UpsertUserByGitHubID creates or refreshes.
func (m *Memory) UpsertUserByGitHubID(_ context.Context, u model.User) (model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, existing := range m.users {
		if existing.GitHubID == u.GitHubID {
			existing.Login, existing.Email = u.Login, u.Email
			m.users[id] = existing
			return existing, nil
		}
	}
	u.ID = newID()
	if u.Plan == "" {
		u.Plan = model.PlanFree
	}
	u.CreatedAt = m.now().UTC().Truncate(time.Millisecond)
	m.users[u.ID] = u
	return u, nil
}

// GetUser loads one account.
func (m *Memory) GetUser(_ context.Context, id string) (model.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	if !ok {
		return model.User{}, apperrors.NotFound("user")
	}
	return u, nil
}
