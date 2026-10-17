# DeployIQ Optimizer

> Point it at a repo. Get back a smaller, safer Dockerfile — no code changes
> required.

A free, open-source CLI that detects a repository's stack, generates an
optimized multi-stage Dockerfile, and shows the estimated size reduction before
you change anything. It runs entirely on your machine: no account, no network
calls, no telemetry by default.

```
$ doiq .
Detected stack:  node (package.json), npm, package-lock.json
Dockerfile:      Dockerfile (1 stage, runtime base node:20)

Optimizations applied:
  + multi-stage-build  build happens in a separate stage; only what the app needs at runtime ships
  + alpine-base        runtime uses the Alpine variant of the base image
  + prod-only-deps     only production dependencies are present in the final image
  + layer-caching      dependency manifests are copied before source, so code edits reuse the dependency layer
  + no-package-cache   package-manager caches are disabled or removed in the same layer
  + non-root-user      the container runs as an unprivileged user

Estimated image size (estimates, not measurements):
                               before        after
  base image                  1100 MB       160 MB
  ...
  Estimated reduction: ~959 MB (84%)

Dry run: nothing was written. Re-run with -write to save Dockerfile.optimized.
```

**Supported stacks:** Node.js (npm, Yarn, pnpm), Go, Python (pip,
`pyproject.toml`, Poetry). Every one is covered by a fixture whose generated
Dockerfile is built, started and smoke-tested in CI on every commit.

---

## Install

```bash
go install github.com/dhyaneshsiddhartha15/DeployIQ/cmd/doiq@latest
```

or download a prebuilt binary (macOS arm64/amd64, Linux amd64/arm64, Windows
amd64) with its `SHA256SUMS` from
[GitHub Releases](https://github.com/dhyaneshsiddhartha15/DeployIQ/releases).

## Use

```bash
doiq                 # analyse the current directory (dry run: nothing is written)
doiq path/to/repo    # analyse another directory
doiq -diff .         # unified diff against your current Dockerfile
doiq -write .        # write Dockerfile.optimized, after a y/N prompt
doiq -write -yes .   # ... without prompting (CI)
doiq -format json .  # machine-readable report on stdout
```

`doiq` never modifies your `Dockerfile`. With `-write` it writes
`Dockerfile.optimized` next to it, so you can compare and switch deliberately:

```bash
docker build -f Dockerfile.optimized .
```

| Exit code | Meaning |
| --- | --- |
| 0 | success (including a declined `-write`) |
| 1 | error (unreadable path, failed write, ...) |
| 2 | usage error |
| 3 | no supported stack detected |
| 4 | stack recognised, but the repo or Dockerfile uses something v1 refuses to guess at |

### What it does — and refuses to do

The engine is deterministic and rule-based (no AI). It changes **how the image
is built, never how the app runs**:

- Kept from your Dockerfile: language version, working directory, `ENV`,
  `EXPOSE`, `CMD`/`ENTRYPOINT`, `HEALTHCHECK`, `USER`, `LABEL`, `VOLUME`, copies
  to paths outside the app directory, and every `RUN` step it does not
  recognise (carried over verbatim, and listed in the report).
- Changed: single stage → multi-stage; full base → slim/Alpine/distroless;
  dev dependencies pruned after the build; manifests copied before source;
  caches removed; non-root user (unless a port below 1024 needs root).
- Tools your start command or health check needs (`curl`, `bash`, ...) are
  added back to the smaller runtime image.

It **fails safely** instead of guessing (exit 4) on: custom/private base
images, heredocs, `ONBUILD`, base images that depend on a build argument with
no default, monorepo workspaces, multi-language builds, several Go `main`
packages with no obvious choice, and apps whose start command cannot be
inferred.

Size figures are **estimates** from static tables — nothing is pulled or built
— and are biased to under-promise. On the fixture suite the estimated saving is
within a few points of the measured one (e.g. 84% estimated vs 88% actual).

### Optional: the dashboard

The CLI is fully useful with no backend, forever. If you want history:

```bash
doiq login           # GitHub OAuth in your browser; token stored in your user config dir
doiq -report .       # send this run's result
doiq whoami
doiq logout
```

Only the repository name, stack, the two size estimates and the rule ids are
sent — never code, paths or the Dockerfile.

---

## Repository layout

```
deployiq-optimizer/
├── cmd/
│   ├── doiq/                  CLI entrypoint — the v1 product
│   └── doiq-api/              API server entrypoint (optional backend)
├── internal/
│   ├── detector/              stack detection from manifest files
│   ├── dockerfile/            Dockerfile parsing, analysis and generation
│   ├── rules/                 per-stack optimization rules
│   ├── analyzer/              image size estimation
│   ├── report/                terminal/JSON output and diffing
│   ├── client/                CLI's API client (login, -report only)
│   └── backend/               API server: gRPC + REST gateway + admin
│       ├── handler/           transport layer  — parse, call, respond
│       ├── service/           business logic   — no HTTP, no MongoDB
│       ├── repository/        storage layer    — MongoDB (+ in-memory)
│       └── github/            GitHub OAuth client
├── api/
│   ├── v1/                    protobuf contract + generated code
│   └── swagger/OpenAPI/       generated OpenAPI bundle, embedded in the binary
├── pkg/                       config, constants, errors, logger, middleware, model, version
├── web/                       React dashboard (Vite)
├── build/                     Dockerfile (API + dashboard), docker-compose
├── deploy/                    Fly.io config: production and staging
├── configs/                   default.yml (no secrets, ever)
├── docs/                      architecture, API, operations, incidents
└── testdata/fixtures/         real repos the CI build gate runs against
```

## Development

```bash
make help            # list every target
make                 # fmt, vet, test, build
make test            # unit + API tests (in-memory store)
make test-race       # under the race detector (needs cgo)
make test-fixtures   # the Phase 10.2 gate: generated Dockerfiles must build and serve (needs Docker)
make cover           # core packages must stay at 80%+ coverage
make check-cli-deps  # the CLI must never link the backend, gRPC or MongoDB
make lint            # golangci-lint
make proto           # regenerate protobuf / gRPC / gateway / OpenAPI
make web-install web-test web-build
make dev-api         # API on :8080 with the in-memory store and built dashboard
make compose-up      # API + dashboard + MongoDB in Docker
```

The MongoDB contract test runs when `DEPLOYIQ_TEST_MONGO_URI` is set:

```bash
docker run -d --rm -p 127.0.0.1:27017:27017 mongo:7
DEPLOYIQ_TEST_MONGO_URI=mongodb://127.0.0.1:27017 go test ./internal/backend/repository
```

Requires Go (version pinned in `go.mod`) and, for the dashboard, Node.js 22.
`make proto` additionally needs `protoc` with `protoc-gen-go`,
`protoc-gen-go-grpc`, `protoc-gen-grpc-gateway` and `protoc-gen-openapiv2`;
generated code is committed, so a plain clone builds without them.

## The backend

Optional, and it stays that way: nothing under `cmd/doiq` imports anything the
server needs, and CI enforces it.

| Listener | Address           | Exposure  | Purpose                                   |
| -------- | ----------------- | --------- | ----------------------------------------- |
| HTTP     | `:8080`           | public    | REST/JSON API, OAuth, dashboard, `/healthz` |
| gRPC     | `127.0.0.1:8090`  | loopback  | internal transport for the gateway        |
| admin    | `127.0.0.1:8081`  | loopback  | health, version, runtime log level        |

Endpoints are documented in [`docs/API.md`](docs/API.md). Configuration is
defaults → `configs/default.yml` → `DEPLOYIQ_*` environment variables. Secrets
have **no** YAML key and are read from the environment only:

```
DEPLOYIQ_MONGO_URI                  (memory:// for local development)
DEPLOYIQ_GITHUB_CLIENT_SECRET
DEPLOYIQ_AUTH_SESSION_SIGNING_KEY   (16+ characters)
DEPLOYIQ_ALERT_WEBHOOK_URL          (optional)
```

Deployment, monitoring and the incident runbook are in
[`docs/OPERATIONS.md`](docs/OPERATIONS.md).

## Phase mapping

| Phase                     | Where it lives                                                  | Status |
| ------------------------- | --------------------------------------------------------------- | ------ |
| 0 Vision, 1 Requirements  | `docs/`, `pkg/constants`, exit codes, `cmd/doiq` tests (US-1..3) | done |
| 2 High-level design       | this layout; `internal/backend` as one service                   | done |
| 3 Technology decisions    | `go.mod`, `docs/ARCHITECTURE.md`                                 | done |
| 4 Detailed design         | `cmd/`, `internal/{detector,dockerfile,rules,analyzer,report}`, `api/v1` | done |
| 5 Backend design          | `internal/backend/{handler,service,repository,github}`, `pkg/config` | done |
| 6 Frontend design         | `web/`                                                           | done |
| 7 Database design         | `internal/backend/repository` (collections, Phase 7.2 indexes)   | done |
| 8 Infra & DevOps          | `build/`, `deploy/`, `.github/workflows/`, `Makefile`            | done |
| 9 Security                | `pkg/errors`, auth, rate limiting, path validation, secrets      | done |
| 10 Testing                | unit, API, MongoDB contract, dashboard, fixture build gate       | done |
| 11 Deployment             | `release.yml`, `deploy.yml`, staging + production Fly config     | done |
| 12 Operations             | admin endpoints, JSON logs, 5xx alerting, `docs/OPERATIONS.md`   | done |
| 13 Future roadmap         | `docs/ARCHITECTURE.md` §7, debt notes in code                    | recorded |

Details: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## License

[Apache License 2.0](LICENSE).

Phase 0.2.5 defines this as a free, open-source tool, and Phase 0.11 assumes
distribution through developer-community channels — both require a license that
lets people actually use and redistribute it. Apache-2.0 over MIT for the
explicit patent grant, which matters for a tool that generates build files
other projects then ship.
