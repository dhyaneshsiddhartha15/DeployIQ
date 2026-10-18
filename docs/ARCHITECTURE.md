# Architecture

How this repository is organised, why, and where each phase of the design
document lands. Read this before adding a package.

The design document (`DeployIQ-Optimizer-Full-Documentation-Phases-0-13`) is the
requirement source. This file records how those requirements became a tree of
directories, and the four places the tree deliberately departs from either the
document or the reference project it was modelled on.

---

## 1. The one-paragraph version

DeployIQ Optimizer is a CLI. Everything else is optional and later.

`doiq` reads a repository, detects its stack, generates an optimized Dockerfile
and prints an estimated size reduction. It writes nothing without an explicit
opt-in. That is the whole v1 product, it runs on the user's own machine, and it
has no backend (Phase 0.8).

From Phase 4 of the build plan there is also `doiq-api`: a single small Go
service that accepts opt-in optimization reports and serves a dashboard. It is
additive. The CLI must stay fully useful with zero backend, forever, even after
the dashboard ships (Phase 2.1) — which in this tree is enforced structurally,
not by convention: nothing under `cmd/doiq` transitively imports
`internal/backend`, `api/` or any database driver.

---

## 2. Layout and the reasoning behind it

```
cmd/            thin entrypoints — flag parsing and wiring, no logic
internal/       private to this module; the compiler enforces it
pkg/            importable by an external consumer
api/            the versioned contract (protos) and code generated from it
build/          how the artifact is packaged
configs/        non-secret defaults
docs/           this file and operational notes
testdata/       fixture repositories for the CI build gate
third_party/    vendored protos, so protoc needs no network access
web/            the React dashboard (Phase 5+)
```

**Why `internal/` for the feature packages.** The design document's Phase 4.3
folder structure already places `detector`, `dockerfile`, `rules`, `analyzer`
and `report` under `internal/`, and it is the right call for a reason worth
stating: these are implementation details of one product, not a library.
Putting them under `internal/` means an external repository importing
`deployiq-optimizer` cannot depend on the rule engine's shape, so it can be
restructured freely — which Phase 13.2 says it will be, when one-rule-per-stack
becomes ranked-and-composed rules.

**Why `pkg/` at all.** Only for what genuinely crosses the module boundary:
`model` types appear in the API contract, `errors` kinds map to status codes a
client sees, `version` is reported over the wire. The rest of `pkg/` (`config`,
`logger`, `middleware`, `constants`) sits there because both binaries use it and
neither owns it. The split matches the reference project, which
uses `pkg/` for shared infrastructure and `internal/` for private machinery.

**Why `cmd/` is thin.** `cmd/doiq-api/main.go` is 60 lines: parse a flag, load
config, init the logger, call `backend.New` and `backend.Run`. The reference
project keeps its whole bootstrap in a 500-line root `main.go`; that works but
is not testable, and this project splits the server into `internal/backend`
instead. This is a deliberate departure — see §6.

---

## 3. Phase-by-phase mapping

Every phase has a place, including the ones that are documentation rather than
code. "Structural" below means the phase shaped the layout without producing a
file of its own.

### Phase 0 — Project Vision

Nothing to build. Its rules are load-bearing on the code and are enforced where
they are easy to violate:

| Rule (Phase 0.6/0.7) | Enforced by |
| --- | --- |
| Never write a file without confirmation | `cmd/doiq`: `-write` defaults false, then a y/N prompt (or `-yes`); non-interactive runs without `-yes` refuse. `internal/dockerfile.Generate` returns bytes and has no filesystem access. Tested byte-for-byte in `cmd/doiq/main_test.go` (US-2) |
| Runs fully offline, no telemetry by default | `internal/analyzer` estimates statically, no registry pull; the only HTTP client (`internal/client`) is reached solely by `doiq login`/`whoami` and `-report` |
| Zero configuration for the happy path | `cmd/doiq` reads no config file on the default path |
| Single static binary | `CGO_ENABLED=0`, stdlib `flag`, and `make check-cli-deps` fails CI if the CLI links gRPC, protobuf, the backend or the MongoDB driver (8.7 MB binary) |

### Phase 1 — Requirement Gathering

The functional requirements map to modules one-to-one:

| FR | Module |
| --- | --- |
| FR-1 detect stack from manifests | `internal/detector` — one probe per stack, priority from `constants.SupportedStacks` |
| FR-2 parse an existing Dockerfile | `internal/dockerfile` (`parser.go`, `analysis.go`) |
| FR-3 generate a multi-stage Dockerfile | `internal/rules` + `internal/dockerfile/generator.go` |
| FR-4 estimate and display before/after | `internal/analyzer` + `internal/report` |
| FR-5 never overwrite without confirmation | `cmd/doiq.writeOutput` — prompt names "Overwrite existing" when the file exists; atomic temp-file + rename |
| FR-6 `--dry-run` default, `--write` opt-in | `cmd/doiq` flags; `-dry-run` accepted as the explicit default |
| FR-7 clear message on undetected stack | `cmd/doiq` + `constants.ExitStackUnknown` (3) |
| FR-8 fully offline | no network code on the default path |

Edge cases (Phase 1.4): a polyglot repo picks the first stack by priority and
the report says so; a missing Dockerfile is inferred from conventions; unusual
syntax or an unrecognised base image fails safely with `ExitUnsupported` (4),
distinct from a crash (1) so CI can tell "not applicable" from "broken"; a
missing or unreadable path is a filesystem error.

### Phase 2 — High-Level System Design

The component table is: CLI binary (`cmd/doiq` + `internal/{detector,
dockerfile,rules,analyzer,report}`), Go API server (`cmd/doiq-api` +
`internal/backend`), MongoDB (`internal/backend/repository`), GitHub OAuth
(`internal/backend/github` + `service/auth.go`), React dashboard (`web/`).

Phase 2.3's "intentionally not a Kubernetes deployment" shows up as an absence:
no Helm chart, no manifests. `deploy/` holds one Fly.io file per environment.

### Phase 3 — Technology Decisions

Applied in `go.mod`. The dependency list is short on purpose:

| Dependency | Why it is here | Linked into |
| --- | --- | --- |
| `github.com/BurntSushi/toml` | reading `pyproject.toml` (FR-1) | CLI |
| `gopkg.in/yaml.v3` | config file parsing | both |
| `google.golang.org/grpc`, `grpc-gateway/v2`, `protobuf` | internal transport and the generated REST/JSON surface | API only |
| `go.mongodb.org/mongo-driver/v2` | the Phase 4 store | API only |

Not present, deliberately: a CLI framework (stdlib `flag`), a logging library
(`log/slog`), a validation library, a rate-limiter package (a 40-line token
bucket), a router (stdlib `ServeMux` patterns), a DI container. Phase 3.2 also
rules out Kubernetes, a message queue, a service mesh, Redis, and AI in the
core engine — none appear. The dashboard uses React, React Router and Vite
only; the chart is hand-written SVG.

### Phase 4 — Detailed System Design

The module breakdown is the `internal/` tree:

| Package | Contents |
| --- | --- |
| `detector` | `Detect`; `NodeInfo` (package manager, lockfile counts, native modules, workspaces), `GoInfo` (version, main packages, cgo, embed), `PythonInfo` (pip/Poetry, requirement names, entry points, Django WSGI) |
| `dockerfile` | `Parse`/`ParseBytes` (continuations, escape directive, exec form, ARG substitution, stages); `Image` families and variants; `Analyze` classifies every RUN command; `Generate` renders a `File` |
| `rules` | one generator per stack (`node.go`, `go.go`, `python.go`), the shared carry-over logic (`carry.go`), and the persisted rule ids |
| `analyzer` | base-image and package size tables, dependency and source estimates, conservative `Compare` |
| `report` | text and JSON output, a unified diff |
| `client` | the CLI's opt-in API client and the loopback OAuth listener |

The one design principle worth knowing before editing `rules`: **the engine
changes how the image is built, never how the app runs.** Language version,
workdir, env, ports, start command, health check, user, labels, copies outside
the workdir, and every RUN step it does not recognise are carried over. Tools
the start command or health check use (`curl`, `bash`, a script's shebang) are
added back to the smaller base. Anything it cannot reason about is refused with
`ErrUnsupported` rather than guessed at.

Naming follows Phase 4.4: lowercase single-word packages, verb-first exported
functions (`detector.Detect`), fixtures named `<stack>-<scenario>`, API routes
as plural nouns under `/api/v1`.

### Phase 5 — Backend Design

`internal/backend` is the three-layer split, one directory per layer:

```
handler/     HTTP/gRPC concerns only — parse, call one service method, respond
service/     business logic; owns the repository interfaces it depends on
repository/  MongoDB access (and an in-memory twin); implements those interfaces
```

The interfaces live in `service`, with the consumer. That is what makes Phase
5.1's "the db layer could be swapped without touching business logic"
structurally true — and it was exercised: the in-memory store and the MongoDB
store pass the same contract test.

Phase 5.4's error rules are implemented once: `pkg/errors.InvalidField` carries
a field to a gRPC `BadRequest` detail, the gateway error handler turns it into
`{"error":{"code","message","field"}}`, decoder errors become a fixed message,
and internal errors always read `internal server error`. Strict decoding
rejects unknown fields. Phase 5.5 (background jobs) is absent — every
operation is a synchronous single-document read or write; the only goroutines
are the rate limiter's sweeper and the alert monitor.

Phase 5.3 (auth): GitHub OAuth for both clients. The state parameter is
HMAC-signed, expires in 10 minutes and is bound to a browser nonce cookie; the
CLI receives its token on `127.0.0.1:<port>` (never a host from the request),
the dashboard in a URL fragment. Session tokens are stateless HMAC tokens with
a short TTL; a kind prefix stops a login state being replayed as a session.

Phase 5.6 is `pkg/config`: one place that reads and validates at startup and
fails fast, reporting every problem at once.

### Phase 6 — Frontend Design

`web/` is a Vite + React app with the Phase 6.1 routes (`/`, `/login`,
`/dashboard`, `/dashboard/*` — a splat, because repository names contain `/`)
plus `/login/callback`. State is `useState`/`useContext` only (6.2). Every
data view goes through `useApi`, which has exactly three states — loading,
error (with retry), loaded (6.4). `api/client.js` is the only code that
touches the token, and it handles 401 centrally. The savings chart uses the
Okabe-Ito palette plus a hatch pattern so the two series differ in more than
hue, and has an accessible text summary (6.5). The layout holds at 375 px.

### Phase 7 — Database Design

Two collections (7.1), stored shapes in `repository/mongo.go`; `pkg/model`
carries no storage tags. The three 7.2 indexes are created idempotently at
startup: `optimizations {userId:1, createdAt:-1}`, `optimizations
{repoName:1}`, `users {githubId:1}` unique. Users are upserted atomically
(`$set` profile, `$setOnInsert` plan and createdAt). All filters are typed
`bson.D` (Phase 9.2). No sharding (7.3), Atlas backups (7.4), no
`scripts/migrate/` until the first rename (7.5).

### Phase 8 — Infrastructure & DevOps

- `.github/workflows/test.yml` — gofmt, vet, CLI import boundary, tests under
  the race detector with a MongoDB service, core coverage floor, build, proto
  drift check, golangci-lint, dashboard tests and build, the fixture gate, and
  an image build.
- `.github/workflows/release.yml` — tag `v*`: test, cross-compile, checksum,
  publish, image.
- `.github/workflows/deploy.yml` — staging, smoke test, production.
- `build/Dockerfile` — dashboard build stage, API build stage, distroless
  non-root runtime (~29 MB). The CLI is a native binary by design (8.2).
- `deploy/fly.toml`, `deploy/fly.staging.toml` — the whole of the IaC (8.3).

### Phase 9 — Security

| Requirement | Where |
| --- | --- |
| Repo path validated (9.1) | `cmd/doiq.validatePath`; manifests and Dockerfiles must be regular files (symlinks refused) under size caps |
| No execution of untrusted content (9.1) | detection and parsing are read-only text inspection; git config is read as a file; the only `exec` is opening a browser with our own URL |
| Generated output verified (9.1) | `make test-fixtures`, every commit |
| Signed, checksummed releases (9.1) | `release.yml` `SHA256SUMS`, public Actions provenance |
| GitHub OAuth only (9.2) | no password field anywhere |
| Own-data-only, at the service layer (9.2) | the user id comes from the verified session; `OptimizationStore.ListOptimizations` cannot be called without an owner |
| HTTPS only (9.2) | `force_https` in `deploy/fly*.toml` |
| Per-IP rate limiting at launch (9.2) | `internal/backend/httpmw.go`, on by default; proxy headers trusted only when configured |
| Typed queries, input validation, no reflected input (9.2) | `bson.D` filters; `service.ValidateReport`; fixed error strings |
| Secrets never in source control (8.4) | secrets have no YAML key; environment only |
| Secrets kept out of logs | `middleware.Redact`; access logs omit query strings |

RBAC, audit logs and compliance frameworks are absent per Phase 9.3.

### Phase 10 — Testing Strategy

| Layer (10.1) | Where |
| --- | --- |
| Unit | every package; core packages 90%+ (`make cover` enforces an 80% floor, 10.3) |
| Fixture / integration | `cmd/doiq/fixtures_test.go` (`-tags fixtures`): CLI → `docker build` → run → HTTP check → compare with the fixture's own image |
| API | `internal/backend/server_test.go`: every endpoint, happy and validation paths, OAuth flows, isolation between users, rate limit, CORS, dashboard serving, and a full `doiq login` round trip |
| Store contract | `repository_test.go` against the in-memory store always and real MongoDB when `DEPLOYIQ_TEST_MONGO_URI` is set (CI sets it) |
| Dashboard | `web/src/App.test.jsx` (React Testing Library): routing, auth, loading/error/loaded/empty states, chart |

The fixture target guards its own silent-success modes: no fixtures, or
fixtures with no test running them. The fixture test also fails if a stack in
`SupportedStacks` has no fixture (Phase 1.5).

### Phase 11 — Deployment Strategy

CLI: tag → `release.yml`; rollback is a new tag. Backend: merge → staging →
smoke test → production (`deploy.yml`); rollback is a redeploy of the previous
image. Both native to the platforms chosen; no custom tooling. Details in
`docs/OPERATIONS.md`.

### Phase 12 — Operations

The admin listener (`internal/backend/admin.go`) is the operational surface:
`/internal/healthz`, `/internal/version`, `/internal/log-level`, loopback only.
A public `/healthz` exists for the platform's checks and says nothing but "ok".
Neither checks MongoDB: failing health during an Atlas outage would restart a
healthy binary (12.2 step 3: wait it out).

Alerting (12.1) is in-process: the access log feeds a monitor that posts to a
webhook after a sustained 5xx rate. The runbook (12.2) is
`docs/OPERATIONS.md`; incident notes go in `docs/incidents/`. Dependabot (12.3)
is weekly, with gRPC/protobuf/gateway grouped.

### Phase 13 — Future Roadmap

Recorded, not built — see §5. Two debt items are flagged in the code where
they will bite: `internal/rules/rules.go` (the one-generator-per-stack ceiling)
and `pkg/model/optimization.go` (the deliberately loose schema).

---

## 4. What was deliberately not built

| Not here | Why | Arrives with |
| --- | --- | --- |
| `scripts/migrate/` | Phase 7.5: the first field rename, not before | when a field is renamed |
| Terraform | one Fly app and one Atlas project need no more than `deploy/*.toml` (8.3) | a second service |
| Tracing | one service (5.1); structured logs suffice (8.5) | a second service |
| Token revocation list | stateless sessions with a short TTL; rotating the signing key revokes all | team accounts |
| Homebrew tap | Phase 3.1 says "later" | demand |
| Java, Ruby, PHP; monorepos; AI pruning | Phase 1.7 / 13.1 gates | the gates in §5 |
| DI container | two binaries, a handful of dependencies, wired in `main` | never, probably |

The general rule: a placeholder is worth creating when it fixes a boundary, and
not worth creating when it only fixes a filename.

---

## 5. Future roadmap (Phase 13)

| Feature | Gated on |
| --- | --- |
| GitHub Action / CI integration with PR comments | **built** — `action.yml`, `docs/GITHUB_ACTION.md` |
| Java | **built** — Maven, Gradle, Spring Boot, with fixtures |
| Ruby, PHP, .NET | demand; each needs fixtures first (Phase 1.5) |
| Team accounts | **built** — organisations with admin/member roles (two roles, not general RBAC, per Phase 9.3) |
| Company SSO, fleet scans, measured sizes, vulnerability scans, internal base images | **built** — see README |
| Private-repo / paid tier | an unprompted willingness-to-pay signal; `model.Plan` exists for it |
| AI-assisted dependency pruning (opt-in, experimental) | a long record of zero build-breaking output |
| Kubernetes deploy integration | this product proving trustworthy on its own |

Technical debt watchlist (13.2): rules must become ranked and composed as
stacks grow; the MongoDB schema gets a tightening pass once the dashboard's
real query patterns are known. Explicitly not planned (13.3): multi-cloud,
enterprise SaaS tier, service mesh, multi-region DR.

---

## 6. Departures from the reference project

The reference project is the engineering-standards baseline: proto layout,
generation directives, config shape, interceptor patterns, three-listener split,
graceful shutdown, `pkg/`-versus-`internal/` division. Four things are
intentionally different, all driven by the design document.

**1. Bootstrap lives in `internal/backend`, not a root `main.go`.**
The reference puts its full startup sequence in a ~500-line root `main.go`. That
suits a service whose startup genuinely is a long list of manager constructions.
Here the server has a `Run` method that can be exercised in a test, and `cmd/`
stays thin — the layout the design document's Phase 4.3 already implies with
`cmd/doiq/main.go`. Two binaries also make a single root `main.go` impossible.

**2. gRPC is internal-only; the public API is REST/JSON.**
Phase 5.2 is explicit: "REST over HTTPS, JSON bodies — no GraphQL or gRPC
needed". The reference exposes both. This project keeps proto-first definition
(one contract, generated handlers, an OpenAPI document that cannot drift) while
the gateway presents only REST/JSON on the public listener; the gRPC port binds
to loopback. Clients see exactly the interface Phase 4.5 specifies.

**3. `api/` holds the contract; server code lives in `internal/backend`.**
The design document's Phase 4.3 sketch puts `server.go`, `handlers.go` and
`db/mongo.go` under `api/`. This tree follows the reference (and general Go
convention) in reserving `api/` for the interface definition and its generated
code, and moves the server into `internal/backend/{handler,service,repository}`.
Same three layers Phase 5.1 defines, same responsibilities, better enforcement:
`internal/` cannot be imported from outside the module, and the layer split is
visible in the directory names rather than in two files called `server.go` and
`handlers.go`.

**4. Configuration takes environment overrides and a stricter parse.**
The reference parses a YAML file into a package-level struct with getters. This
project keeps the YAML file but adds a `DEPLOYIQ_`-prefixed environment layer
and returns a `*Config` value instead of using package state. The reason is
Phase 8.4: secrets come from a hosting platform's secret manager as environment
variables and must have no file key at all. Unknown YAML keys are also rejected,
so a typo fails at startup rather than silently keeping a default.

---

## 7. Adding to this project

**A new gRPC service.** Write `api/v1/<resource>.proto` with
`google.api.http` annotations on every rpc, add it to the generate directive in
`api/v1/gen.go`, run `make proto`, then register it in
`internal/backend/register.go` — both in `registerServices` and
`registerGateways`. That file is the only wiring seam; nothing else needs to
know the service exists.

**A new stack.** Add its manifest to `pkg/constants`, its rules to
`internal/rules/<stack>.go`, and — before advertising it anywhere — a fixture
under `testdata/fixtures/<stack>-<scenario>/`. Phase 1.5 makes the fixture a
precondition, not a follow-up.

**A new configuration value.** Add the field to the right struct in
`pkg/config`, a default in `Default()`, an `envX` line in `applyEnv`, a check in
`Validate` if it is required, and the key to `configs/default.yml` — unless it
is a secret, which gets no key by design.
