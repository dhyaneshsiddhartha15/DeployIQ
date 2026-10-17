# Test fixtures

Real sample repositories the CLI is run against in CI. This directory backs
what Phase 10.2 calls **the single most important test**:

> Does the generated Dockerfile actually build, for every supported stack,
> every time?

Nothing ships if that fails, no exceptions. It is the check that protects the
product's core trust property (Phase 0.5.1: zero critical build-breaking bugs),
and it is why a static assertion on the generated text is not enough.

```bash
make test-fixtures
# or: go test ./cmd/doiq -run TestFixtures -tags=fixtures -v
```

For every directory here, `cmd/doiq/fixtures_test.go`:

1. copies it to a temp dir and runs `doiq -write -yes` on it, as a user would;
2. runs `docker build` on the generated `Dockerfile.optimized`;
3. starts the container and requests the URL in `fixture.json`, so "builds"
   also means "starts and serves";
4. when the fixture has its own `Dockerfile`, builds that too and requires the
   optimized image to be smaller, logging estimated against actual savings.

## Current fixtures

| Fixture | Exercises |
| --- | --- |
| `node-express-basic` | naive single-stage `node:20` Dockerfile, dev deps, npm lockfile |
| `node-build-nodockerfile` | no Dockerfile; build script needing a devDependency; prune after build |
| `go-module-nodockerfile` | no Dockerfile; no dependencies; template read from disk at runtime |
| `go-gorun-dockerfile` | `CMD ["go","run",...]` in a full golang image; one dependency; `cmd/` layout |
| `python-flask-basic` | full `python:3.12`, build-essential, curl health check, gunicorn |
| `python-fastapi-pyproject` | no Dockerfile; `[project].dependencies`; uvicorn inferred |
| `python-poetry-nodockerfile` | no Dockerfile; Poetry with `package-mode = false` and dev group |

## Naming

`<stack>-<scenario>/`, per Phase 4.4.

## What belongs in a fixture

A minimal but genuinely buildable application, plus a `fixture.json`:

```json
{ "port": 3000, "path": "/", "expect": "ok" }
```

Enough to exercise the detector and one rule path, and no more — every fixture
is built in CI on every commit, so size is wall-clock cost on every pull
request. Node fixtures commit their `package-lock.json`; Go fixtures their
`go.sum`.

A fixture without a Dockerfile is a valid and important case: Phase 1.4
requires the tool to infer one from stack conventions.

When a user reports a generated Dockerfile that does not build, the fix starts
with a new fixture here that reproduces it.

## The rule that governs this directory

Phase 1.5: **a stack may not be advertised as supported until it has fixture
coverage here.** `TestFixtures` fails if any stack in
`constants.SupportedStacks` has no `<stack>-*` fixture.
