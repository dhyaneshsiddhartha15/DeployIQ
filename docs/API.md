# API reference

The optional backend (`doiq-api`). REST over HTTPS, JSON bodies, versioned
under `/api/v1` (Phase 5.2). The RPC routes are generated from `api/v1/*.proto`;
the machine-readable spec is served at `/swagger/apidocs.swagger.json`.

Every API route except the two OAuth redirects needs
`Authorization: Bearer <session token>`. A user only ever reads and writes their
own records — enforced in the service layer on every query (Phase 9.2).

## Errors

Every error, on every route, has one shape:

```json
{ "error": { "code": "invalid_argument", "message": "must not be empty", "field": "repoName" } }
```

| HTTP | `code` | When |
| --- | --- | --- |
| 400 | `invalid_argument` | validation failed (`field` names the field) or the body is malformed |
| 401 | `unauthenticated` | missing, invalid or expired session |
| 404 | `not_found` | unknown route |
| 405 | `method_not_allowed` | known route, wrong method |
| 413 / 400 | `invalid_argument` | body larger than `server.maxBodyBytes` (64 KiB) |
| 429 | `rate_limited` | per-IP limit exceeded; see `Retry-After` |
| 500 | `internal` | our fault; the message is always `internal server error` |
| 503 | `unavailable` | MongoDB or GitHub is unreachable |

Messages are fixed strings written by the server. Submitted values are never
echoed back, and decoder errors are replaced with a fixed message (Phase 9.2).
Unknown JSON fields are rejected, not ignored (Phase 5.4: strict schema).

## `POST /api/v1/optimizations`

Store one opt-in result (sent by `doiq -report`).

```json
{
  "repoName": "company/backend",
  "detectedStack": "node",
  "originalEstSizeMB": 640,
  "optimizedEstSizeMB": 118,
  "rulesApplied": ["multi-stage-build", "alpine-base", "prod-only-deps"]
}
```

| Field | Rule |
| --- | --- |
| `repoName` | required; `owner/name` style, letters, digits, `._-`, `/`-separated, ≤200 chars, no `.`/`..` segments |
| `detectedStack` | one of `node`, `go`, `python` |
| `originalEstSizeMB`, `optimizedEstSizeMB` | positive integers, ≤ 1,048,576 |
| `rulesApplied` | ≤32 ids, each lowercase words joined by hyphens; duplicates are dropped |

Response `200`:

```json
{
  "id": "6ad3b50fd7cb20780db6c1df",
  "repoName": "company/backend",
  "detectedStack": "node",
  "originalEstSizeMB": 640,
  "optimizedEstSizeMB": 118,
  "rulesApplied": ["multi-stage-build", "alpine-base", "prod-only-deps"],
  "createdAt": "2026-10-17T17:48:47.123Z",
  "savingsPercent": 81.5625
}
```

## `GET /api/v1/optimizations?repo=&limit=`

The caller's history, newest first. `repo` filters to one repository; `limit`
defaults to 50 and is capped at 500.

```json
{ "optimizations": [ { "id": "...", "repoName": "...", "...": "..." } ] }
```

## `GET /api/v1/me`

```json
{ "id": "6ad3b505ff6b7e22a24375fe", "githubId": "424242", "login": "octocat",
  "email": "octocat@example.com", "plan": "free", "createdAt": "2026-10-17T17:48:54Z" }
```

## OAuth (Phase 5.3)

Two plain HTTP routes; both answer with redirects.

### `GET /api/v1/auth/github/login`

| Query | |
| --- | --- |
| `client=web` | browser login for the dashboard (default) |
| `client=cli&port=<1024-65535>&state=<16-128 hex>` | `doiq login`: the CLI listens on `127.0.0.1:<port>` |

Sets a short-lived, `HttpOnly`, `SameSite=Lax` nonce cookie and redirects to
GitHub's consent page with a signed `state` (scopes `read:user user:email`).

### `GET /api/v1/auth/github/callback`

GitHub's redirect target. Must match the OAuth app's callback URL exactly. It
checks the signed state (and that it is under 10 minutes old) against the nonce
cookie, exchanges the code, creates or refreshes the user, issues a session
token and redirects:

- `client=cli` → `http://127.0.0.1:<port>/callback?state=<cli state>&token=<token>`
  (or `&error=access_denied|login_failed`). The host is always `127.0.0.1` and
  the port comes from the signed state, so this is not an open redirect.
- `client=web` → `<dashboardUrl>/login/callback#token=<token>`. The token is in
  the fragment, which browsers never send to a server, so it stays out of
  access logs. On refusal: `<dashboardUrl>/login?error=access_denied`.

Session tokens are HMAC-SHA256-signed, stateless, and expire after
`auth.sessionTtl` (24h by default). Rotating
`DEPLOYIQ_AUTH_SESSION_SIGNING_KEY` invalidates every session at once.

## Other routes

| Route | Listener | Purpose |
| --- | --- | --- |
| `GET /healthz` | public | liveness for the hosting platform; never checks MongoDB |
| `GET /swagger/apidocs.swagger.json` | public | OpenAPI spec |
| `GET /` and any non-API path | public | the dashboard (when `server.webDir` is set), with SPA fallback |
| `GET /internal/healthz`, `/internal/version` | admin (loopback) | operator checks |
| `GET`/`PUT /internal/log-level` | admin (loopback) | `{"level":"debug"}` takes effect immediately |
