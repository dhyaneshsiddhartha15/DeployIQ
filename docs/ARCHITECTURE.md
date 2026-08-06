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
neither owns it. The split matches the reference `orbiter-storage-plugin`, which
uses `pkg/` for shared infrastructure and `internal/` for private machinery.

**Why `cmd/` is thin.** `cmd/doiq-api/main.go` is 60 lines: parse a flag, load
config, init the logger, call `backend.New` and `backend.Run`. The reference
project keeps its whole bootstrap in a 500-line root `main.go`; that works but
is not testable, and this project splits the server into `internal/backend`
instead. This is a deliberate departure — see §5.

---

## 3. Phase-by-phase mapping

Every phase has a place, including the ones that are documentation rather than
code. "Structural" below means the phase shaped the layout without producing a
file of its own.

### Phase 0 — Project Vision

Nothing to build. Two of its rules are load-bearing on the code and are enforced
where they are easy to violate:

| Rule (Phase 0.6/0.7) | Enforced by |
| --- | --- |
| Never write a file without confirmation | `cmd/doiq`: `-write` defaults false; `internal/dockerfile.Generate` returns bytes and has no filesystem access |
| Runs fully offline, no telemetry by default | `internal/analyzer` estimates statically, no registry pull; `config.CLIConfig.TelemetryEnabled` zero value is off |
| Zero configuration for the happy path | `cmd/doiq` reads no config file at all |
| Single static binary | `CGO_ENABLED=0`, stdlib `flag` over a CLI framework, no driver linked into the CLI |

### Phase 1 — Requirement Gathering

The functional requirements map to modules one-to-one, and each module's
`doc.go` names the FR it satisfies:

| FR | Module |
| --- | --- |
| FR-1 detect stack from manifests | `internal/detector` |
| FR-2 parse an existing Dockerfile | `internal/dockerfile` |
| FR-3 generate a multi-stage Dockerfile | `internal/dockerfile` + `internal/rules` |
| FR-4 estimate and display before/after | `internal/analyzer` + `internal/report` |
| FR-5 never overwrite without confirmation | `cmd/doiq` |
| FR-6 `--dry-run` default, `--write` opt-in | `cmd/doiq` |
| FR-7 clear message on undetected stack | `cmd/doiq` + `pkg/constants.ExitStackUnknown` |
| FR-8 fully offline | no HTTP client in the CLI's import graph |

US-3 requires a non-zero exit when detection fails. It gets its own exit code
(`ExitStackUnknown = 3`) rather than sharing the generic failure code, so a CI
pipeline can tell "not applicable to this repo" from "the tool broke".

### Phase 2 — High-Level System Design

Structural. The component table becomes: CLI binary (`cmd/doiq` + `internal/*`),
Go API server (`cmd/doiq-api` + `internal/backend`), MongoDB
(`internal/backend/repository`), GitHub OAuth (`internal/backend/service`),
React dashboard (`web/`).

Phase 2.3's "this is intentionally not a Kubernetes deployment" shows up as an
absence: no Helm chart, no manifests, no operator. `build/` holds one Dockerfile
and one compose file for local development.

### Phase 3 — Technology Decisions

Applied in `go.mod`. The dependency list is short on purpose:

| Dependency | Why it is here |
| --- | --- |
| `google.golang.org/grpc` | internal transport for the API server |
| `grpc-ecosystem/grpc-gateway/v2` | generates the REST/JSON surface from the protos |
| `google.golang.org/protobuf` | the contract's wire format |
| `gopkg.in/yaml.v3` | config file parsing |

Not present, deliberately: a CLI framework (stdlib `flag` covers four booleans),
a logging library (`log/slog` covers both output formats), a validation library,
a DI container, an error package. Phase 3.2 also rules out Kubernetes, a message
queue, a service mesh, Redis, and AI in the core engine — none appear.

The MongoDB driver is not in `go.mod` yet. It arrives with Phase 4, alongside
the repository code that uses it, rather than sitting in the CLI's dependency
graph unused.

### Phase 4 — Detailed System Design

The module breakdown is the `internal/` tree. Each package exists with a `doc.go`
stating its contract, its constraints and the phase that fills it in. The
packages are empty; the boundaries are not negotiable later.

Naming follows Phase 4.4: lowercase single-word packages, verb-first exported
functions (`detector.Detect`, not `detector.DetectStack`), fixtures named
`<stack>-<scenario>`, API routes as plural nouns under `/api/v1`.

### Phase 5 — Backend Design

`internal/backend` is the three-layer split, one directory per layer:

```
handler/     HTTP/gRPC concerns only — parse, call one service method, respond
service/     business logic; owns the repository interfaces it depends on
repository/  MongoDB access; implements those interfaces
```

The interfaces live in `service`, with the consumer, not with the
implementation. That is what makes Phase 5.1's "the db layer could be swapped
without touching business logic" structurally true: the dependency arrow points
from `repository` to `service`, so nothing in the business logic names MongoDB.

Phase 5.4's two error rules are implemented in `pkg/errors` rather than left to
each handler: `InvalidField` carries a field name to the client for a 400, and
`Public(err)` returns a fixed string for anything internal, so no wrapped cause
and no reflected input can escape. Phase 5.5 (background jobs) is deliberately
absent — every Phase 4 operation is a synchronous single-document read or write.

Phase 5.6 is `pkg/config`: one place that reads and validates at startup and
fails fast. `Validate` reports every problem at once rather than the first,
because an operator fixing a misconfiguration should not restart four times to
discover four missing variables.

### Phase 6 — Frontend Design

`web/`, with the routing map and component layout recorded in its README. No
scaffolding: a `create-react-app` skeleton generated now would be stale by the
time Phase 5 of the build plan starts, and would put a `node_modules` in a
repository whose product is a single Go binary.

### Phase 7 — Database Design

`pkg/model` holds the two Phase 7.1 collections as plain Go types — no bson
tags, no `ObjectID`. Storage tags in the domain model would reintroduce exactly
the coupling Phase 5.1 asks to avoid; converting to whatever the store uses is
the repository's job.

`internal/backend/repository/doc.go` records the three indexes from Phase 7.2
and the typed-query requirement from Phase 9.2. Phase 7.5's `scripts/migrate/`
is not created: the schema is additive-only for now, and the document says the
migration script arrives the first time a field must be renamed — not before.

### Phase 8 — Infrastructure & DevOps

Both workflows Phase 8.1 names, with the names it gives them:

- `.github/workflows/test.yml` — vet, lint, test, build, and the fixture gate
- `.github/workflows/release.yml` — cross-compile, checksum, publish, image

`build/Dockerfile` packages only the API server. The CLI is a native binary by
design (Phase 8.2). The image is multi-stage onto `distroless/static` with a
non-root user — a container-optimization tool shipping a bloated image would be
its own counterexample.

Phase 8.3 (IaC) is intentionally not present: one small service and one managed
database do not need Terraform to describe them yet, and the document scopes it
to a single minimal file when it does.

### Phase 9 — Security

| Requirement | Where |
| --- | --- |
| Repo path validated (9.1) | `cmd/doiq.validatePath` — the CLI's only untrusted input |
| No execution of untrusted content (9.1) | parsing is read-only; no `exec` in the CLI's import graph |
| Generated output verified (9.1) | `make test-fixtures`, run on every commit |
| Secrets never in source control (8.4) | secrets have no YAML key; environment only |
| No internal detail in responses (9.2) | `errors.Public` |
| No reflected input in errors (9.2) | `errors.InvalidField` takes a fixed reason, never the submitted value |
| Own-data-only, at the service layer (9.2) | documented as `service`'s responsibility, not middleware's |
| Rate limiting at Phase 4 launch (9.2) | `config.RateLimitConfig`, defaults on |
| Secrets kept out of logs | `middleware.Redact` on every logged payload |

RBAC, audit logs and compliance frameworks are absent per Phase 9.3.

### Phase 10 — Testing Strategy

`testdata/fixtures/` and the `test-fixtures` make target exist now, before the
code they test, because Phase 10.2 makes this the release gate rather than a
nice-to-have. `pkg/config` and `pkg/errors` already carry unit tests.

Coverage follows Phase 10.3: high on `detector`, `dockerfile` and `rules`;
unmeasured on glue.

### Phase 11 — Deployment Strategy

`release.yml` is the whole release process: tag `v*`, cross-compile, checksum,
publish. Rollback is a new tag for the CLI and a redeploy of the previous image
for the backend (Phase 11.3) — both native to the platforms chosen, so there is
no custom tooling here to maintain.

### Phase 12 — Operations

The admin listener (`internal/backend/admin.go`) is the operational surface:
`/internal/healthz`, `/internal/version`, `/internal/log-level`. Bound to
loopback, because changing a running process's log level is an operator action,
not an API.

`healthz` deliberately does not check MongoDB. It answers "should this process
be restarted", and failing it during an Atlas outage would restart a healthy
binary — turning a dependency blip into an outage of our own, against Phase
12.2's explicit "wait it out, no custom failover".

`docs/incidents/` exists for the short post-incident notes Phase 12.2 asks for.

### Phase 13 — Future Roadmap

Recorded, not built. Two items are flagged in the code where they will bite:
`internal/rules/doc.go` names the one-rule-per-stack ceiling, and
`pkg/model/optimization.go` names the deliberately loose schema. Both are Phase
13.2 watchlist entries; writing them next to the code means the next person
finds them without reading this file.

---

## 4. What was deliberately not built

Foundation work has a failure mode: scaffolding for phases that may never
arrive. The following were considered and left out, each with the phase that
brings it.

| Not here | Why | Arrives with |
| --- | --- | --- |
| MongoDB driver and connection code | "do not connect databases"; also keeps it out of the CLI binary | Phase 4 |
| Stub gRPC services | a registered stub answers `Unimplemented` on a route a client may treat as live — worse than a 404 | Phase 4 |
| Auth and rate-limit interceptors | both need dependencies and real logic; empty ones would be deleted, not filled | Phase 4 |
| `web/` React scaffold | would be stale before it is touched | Phase 5 |
| `scripts/migrate/` | Phase 7.5 says the first rename, not before | when a field is renamed |
| Terraform / Fly config | Phase 8.3 scopes it to one small file, at deploy time | Phase 4 deployment |
| Tracing interceptor | the reference needs it across many services; this is one (Phase 5.1) | if a second service appears |
| DI container | two binaries, a handful of dependencies, wired in `main` | never, probably |

The general rule: a placeholder is worth creating when it fixes a boundary
(`doc.go` files do), and not worth creating when it only fixes a filename.

---

## 5. Departures from the reference project

`orbiter-storage-plugin` is the engineering-standards reference: proto layout,
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

## 6. Adding to this project

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
