# DeployIQ Optimizer

> Point it at a repo. Get back a smaller, safer Dockerfile — no code changes
> required.

A free, open-source CLI that detects a repository's stack, generates an
optimized multi-stage Dockerfile, and shows the estimated size reduction before
you change anything. It runs entirely on your machine: no account, no network
calls, no telemetry by default.

**Status: project foundation.** The structure, build, configuration and gRPC
scaffolding are in place. No feature logic is implemented yet — see
[Phase mapping](#phase-mapping) for what lands where.

---

## Quick start

```bash
make build          # both binaries into ./bin
./bin/doiq .        # analyse the current repo (dry run — nothing is written)
./bin/doiq -write . # write the optimized Dockerfile, after confirmation
```

The CLI needs no configuration. Phase 0.7 requires a useful result with zero
flags and zero setup, so the default path reads no config file at all.

## Repository layout

```
deployiq-optimizer/
├── cmd/
│   ├── doiq/                  CLI entrypoint — the v1 product
│   └── doiq-api/              API server entrypoint (Phase 4+, optional)
├── internal/                  private; nothing outside this module imports it
│   ├── detector/              stack detection from manifest files
│   ├── dockerfile/            Dockerfile parsing and generation
│   ├── rules/                 per-stack optimization rules
│   ├── analyzer/              image size estimation
│   ├── report/                terminal output and diffing
│   └── backend/               API server: gRPC + REST gateway + admin
│       ├── handler/           transport layer  — parse, call, respond
│       ├── service/           business logic   — no HTTP, no MongoDB
│       └── repository/        storage layer    — MongoDB only
├── api/
│   ├── v1/                    protobuf contract + generated code
│   └── swagger/OpenAPI/       generated OpenAPI bundle, embedded in the binary
├── pkg/                       importable by external consumers
│   ├── config/                configuration loading and validation
│   ├── constants/             values shared across packages
│   ├── errors/                error framework: kinds, HTTP/gRPC mapping
│   ├── logger/                slog setup, runtime-adjustable level
│   ├── middleware/            gRPC interceptors
│   ├── model/                 domain types crossing layer boundaries
│   └── version/               build metadata injected at link time
├── build/                     Dockerfile, docker-compose
├── configs/                   default.yml (no secrets, ever)
├── docs/                      architecture, phase mapping, incidents
├── testdata/fixtures/         real repos the CI build gate runs against
├── third_party/               vendored protos (google.api annotations)
└── web/                       React dashboard (Phase 5+)
```

Package placement follows the usual Go split: `internal/` for anything private
to this module, `pkg/` for what an external consumer could reasonably import,
`cmd/` for thin entrypoints that wire the two together.

## Development

```bash
make help           # list every target
make               # fmt, vet, test, build
make test           # unit tests
make test-race      # unit tests under the race detector (needs cgo)
make test-fixtures  # the Phase 10.2 gate: generated Dockerfiles must build
make lint           # golangci-lint
make proto          # regenerate protobuf / gRPC / gateway / OpenAPI
make compose-up     # local API + MongoDB
```

Requires Go (version pinned in `go.mod`). `make proto` additionally needs
`protoc` with `protoc-gen-go`, `protoc-gen-go-grpc`, `protoc-gen-grpc-gateway`
and `protoc-gen-openapiv2`; generated code is committed, so a plain clone
builds without them.

## The backend (Phase 4+)

Optional, and it stays that way. The CLI must remain fully useful with zero
backend, forever, even after the dashboard ships (Phase 2.1) — nothing under
`cmd/doiq` imports anything the server needs.

Three listeners:

| Listener | Address           | Exposure  | Purpose                              |
| -------- | ----------------- | --------- | ------------------------------------ |
| HTTP     | `:8080`           | public    | REST/JSON API (`/api/v1/…`), OpenAPI |
| gRPC     | `127.0.0.1:8090`  | loopback  | internal transport for the gateway   |
| admin    | `127.0.0.1:8081`  | loopback  | health, version, runtime log level   |

Configuration is defaults → `configs/default.yml` → `DEPLOYIQ_*` environment
variables. Secrets have **no** YAML key and are read from the environment only,
so committing one is impossible rather than merely discouraged (Phase 8.4):

```
DEPLOYIQ_MONGO_URI
DEPLOYIQ_GITHUB_CLIENT_SECRET
DEPLOYIQ_AUTH_SESSION_SIGNING_KEY
```

## Phase mapping

Every phase of the design document has a home in this tree, whether or not it
is implemented yet. The full table — including which phases are structural
rather than code — is in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

| Phase                     | Where it lives                                           | Status      |
| ------------------------- | -------------------------------------------------------- | ----------- |
| 0 Vision, 1 Requirements  | `docs/`, `pkg/constants`, exit codes                      | recorded    |
| 2 High-level design       | this layout; `internal/backend` as one service            | structural  |
| 3 Technology decisions    | `go.mod`, `docs/ARCHITECTURE.md`                          | applied     |
| 4 Detailed design         | `cmd/`, `internal/{detector,dockerfile,rules,analyzer,report}` | placeholders |
| 5 Backend design          | `internal/backend/{handler,service,repository}`, `pkg/config` | scaffolded |
| 6 Frontend design         | `web/`                                                    | placeholder |
| 7 Database design         | `internal/backend/repository`, `pkg/model`                | documented  |
| 8 Infra & DevOps          | `build/`, `.github/workflows/`, `Makefile`                | ready       |
| 9 Security                | `pkg/errors`, `pkg/middleware`, secret handling           | enforced    |
| 10 Testing                | `testdata/fixtures/`, `make test-fixtures`                | wired       |
| 11 Deployment             | `.github/workflows/release.yml`, `build/Dockerfile`       | ready       |
| 12 Operations             | admin endpoints, JSON logs, `docs/incidents/`             | ready       |
| 13 Future roadmap         | `docs/ARCHITECTURE.md`, `ponytail:`-style debt notes      | recorded    |

## Documentation

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — structure, phase-by-phase
  mapping, and the architectural decisions taken here
- [`docs/incidents/`](docs/incidents/) — post-incident notes (Phase 12.2)

## License

Copyright 2026, Coredge.io Inc. All rights reserved.
