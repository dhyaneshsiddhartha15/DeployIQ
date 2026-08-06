// Package repository is the storage layer of the Phase 4+ API server: the
// bottom third of the three-layer split Phase 5.1 defines.
//
// It implements the interfaces declared in internal/backend/service, translating
// pkg/model types to and from MongoDB documents. Nothing above it imports the
// driver, and nothing here contains a business rule.
//
// Phase 7.1 defines two collections:
//
//	optimizations  one opt-in report per CLI run
//	users          one account per GitHub login
//
// with three indexes (Phase 7.2), created once at startup and never assumed to
// exist:
//
//	optimizations  {userId: 1, createdAt: -1}  the dashboard's main query
//	optimizations  {repoName: 1}               the repo-detail page
//	users          {githubId: 1} unique        the OAuth login lookup
//
// Phase 9.2 requires typed queries built through the driver — bson.M and
// friends — never raw string construction. The driver parameterises values;
// string-built filters do not, and that is the Mongo equivalent of SQL
// injection.
//
// Migrations: Phase 7.5 keeps the schema additive-only for now, so new optional
// fields need no migration step. A versioned migration script under scripts/
// arrives the first time a field must be renamed or restructured, not before.
//
// Empty until Phase 4 of the build plan — the module carries no MongoDB
// dependency yet, deliberately: Phase 0.6 and 8.2 keep the CLI a single static
// binary with nothing it does not use linked in.
package repository
