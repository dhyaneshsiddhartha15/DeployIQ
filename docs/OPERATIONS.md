# Operations

How the product is released, deployed, watched and maintained (Phases 8, 11,
12). Written for one maintainer: everything here is a single command or a
default that already happens.

## The CLI (v1 product)

There is nothing to operate. It runs on users' machines.

- **Release** (Phase 11.2): push a tag `vMAJOR.MINOR.PATCH`. `release.yml`
  re-runs the tests, cross-compiles the five platform binaries, publishes them
  with `SHA256SUMS` and generates release notes from PR titles.
- **Gate**: the fixture suite (`make test-fixtures`) builds, starts and
  smoke-tests the generated Dockerfile for every fixture on every commit. There
  is no staging for a CLI; this suite is the safety net (Phase 11.1).
- **Rollback** (Phase 11.3): cut a new tag. Old releases stay downloadable;
  users can pin `go install .../cmd/doiq@vX.Y.Z`.
- **Monitoring** (Phase 12.1): GitHub Issues. A generated Dockerfile that does
  not build is a bug report; reproduce it as a new fixture under
  `testdata/fixtures/` before fixing, so it can never regress.

## The backend (optional, Phase 4+)

One container on Fly.io (`deploy/fly.toml`), MongoDB on Atlas. No Kubernetes,
no message queue, no cache (Phase 3.2).

### First-time setup

1. Create a MongoDB Atlas project and a database user; note the SRV URI.
2. Create two GitHub OAuth apps (production and staging). Callback URLs:
   `https://api.deployiq.dev/api/v1/auth/github/callback` and
   `https://staging.api.deployiq.dev/api/v1/auth/github/callback`.
   Put each client id in the matching `deploy/fly*.toml`.
3. Create the Fly apps and set secrets (never in files, Phase 8.4):

   ```bash
   fly apps create deployiq-api
   fly secrets set -a deployiq-api \
     DEPLOYIQ_MONGO_URI='mongodb+srv://...' \
     DEPLOYIQ_GITHUB_CLIENT_SECRET='...' \
     DEPLOYIQ_AUTH_SESSION_SIGNING_KEY="$(openssl rand -hex 32)" \
     DEPLOYIQ_ALERT_WEBHOOK_URL='https://hooks.slack.com/services/...'
   ```

   Repeat for `deployiq-api-staging` with its own database and OAuth app.
4. DNS: `api.deployiq.dev` → `fly certs add api.deployiq.dev -a deployiq-api`.
   TLS is issued and renewed by the platform (Phase 8.6).
5. Add `FLY_API_TOKEN` as a repository secret and create `staging` and
   `production` environments in GitHub (add a required reviewer on
   `production` if you want a manual promotion step).

### Deploy (Phase 11.2)

Merge to `main`. `deploy.yml` deploys staging, smoke-tests it (health, a 401
from `/api/v1/me`, the dashboard HTML), then deploys production and verifies
its health. Manual: `fly deploy -c deploy/fly.toml`.

### Rollback (Phase 11.3)

```bash
fly releases -a deployiq-api            # find the last good image
fly deploy -c deploy/fly.toml --image registry.fly.io/deployiq-api:<deployment-id>
```

### Monitoring and alerting (Phase 12.1)

- **Logs**: structured JSON on stdout (`fly logs -a deployiq-api`). One record
  per request (method, path, status, duration — never the query string, which
  carries OAuth codes) and per RPC; secrets are redacted.
- **Uptime**: point a free uptime checker (e.g. UptimeRobot) at
  `https://api.deployiq.dev/healthz`, alerting by email.
- **Error rate**: the server watches its own 5xx rate. When more than 5% of
  requests fail for 5 consecutive one-minute windows (with at least 20 requests
  per window) it logs `alert fired` at error level and posts a Slack-compatible
  `{"text": ...}` message to `DEPLOYIQ_ALERT_WEBHOOK_URL`, at most once per 30
  minutes. Tune under `alert:` in `configs/default.yml`.
- **Log level at runtime**, without a restart. The admin listener is
  loopback-only and the image has no shell, so tunnel to it:

  ```bash
  fly proxy 8081 -a deployiq-api
  curl -X PUT -d '{"level":"debug"}' localhost:8081/internal/log-level
  ```

### Incident runbook (Phase 12.2)

1. **Alert fires** → check the Fly status page and `fly logs` first.
2. **Bad deploy** → roll back (above). Do not debug in production.
3. **Dependency outage** (MongoDB Atlas, GitHub OAuth) → API calls return 503
   `unavailable`; `/healthz` stays green on purpose so the platform does not
   restart a healthy process. Post a status note and wait it out — no custom
   failover at this scale.
4. **Afterwards** → a short note in `docs/incidents/YYYY-MM-DD-slug.md`.

### Backups and data (Phase 7.4, 7.5)

Atlas's automated daily backups. No custom tooling. The schema is additive-only;
a versioned `scripts/migrate/` is introduced the first time a field must be
renamed, not before.

### Maintenance (Phase 12.3)

- Dependabot opens weekly PRs (Go modules grouped for gRPC/protobuf, Actions,
  Docker base images). CI must pass, including the fixture gate.
- When a supported runtime moves (a new Node LTS, a new Python or Go release),
  review the defaults in `internal/rules` (`DefaultNodeMajor`,
  `DefaultPythonVersion`, `DefaultGoVersion`) and the size table in
  `internal/analyzer/sizeestimate.go`, then re-run `make test-fixtures`.

### Cost (Phase 12.4)

Atlas free tier plus one `shared-cpu-1x`/256 MB machine; staging scales to
zero when idle. The API image is ~29 MB.
