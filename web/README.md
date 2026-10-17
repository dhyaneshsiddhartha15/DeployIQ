# Dashboard (Phase 5+)

The React dashboard that visualizes optimization history and trends: results
users chose to send with `doiq -report`. Optional, like the whole backend — the
CLI never needs it.

```bash
npm ci            # install
npm run dev       # http://localhost:5173, proxies /api to the Go API on :8080
npm test          # React Testing Library (vitest + jsdom)
npm run build     # static bundle in dist/
```

For a local API with no database or OAuth app: `make dev-api` from the
repository root (in-memory store). For the full stack: `make compose-up`.

## Routes (Phase 6.1)

| Route              | Purpose                                      | Data source |
| ------------------ | -------------------------------------------- | ----------- |
| `/`                | Landing — why use this                       | static |
| `/login`           | GitHub OAuth entry                           | `/api/v1/auth/github/login?client=web` |
| `/login/callback`  | receives the session token (URL fragment)    | — |
| `/dashboard`       | Repos analyzed, most recent savings          | `GET /api/v1/optimizations` |
| `/dashboard/*`     | History and trend for one repo (`owner/name`) | `GET /api/v1/optimizations?repo=` |

The repo route is a splat, not `:repo`, because repository names contain `/`.

## Structure (Phase 6.3)

```
web/src/
├── pages/       Landing.jsx  Login.jsx  Dashboard.jsx  RepoDetail.jsx
├── components/  SavingsChart.jsx  RepoList.jsx  AuthButton.jsx  Layout.jsx  States.jsx
├── api/         client.js — thin fetch wrapper over the Go API
│                useApi.js — the loading / error / loaded state machine
│                stats.js  — pure helpers (per-repo summary, totals, formatting)
├── auth/        AuthContext.jsx — token and current user
└── App.jsx      routes
```

## Decisions

- **State: plain `useState` / `useContext`** (Phase 6.2). A handful of screens
  and one data source.
- **Three explicit states per data-fetching view** — loading, error (with a
  retry button), loaded; plus an explicit empty state. No silent blank screens
  (Phase 6.4).
- **The API client centralizes auth** — it attaches the session token and, on
  401, clears it and redirects to `/login`; no component does either itself.
  The token is kept in `localStorage` and arrives in a URL fragment, which
  never reaches a server log.
- **Color-blind-safe chart** (Phase 6.5): Okabe-Ito vermillion and blue, a hatch
  pattern on "before" so the series differ in more than hue, an accessible text
  summary, and the same numbers in a table.
- **Responsive**: verified at 375 px with no horizontal page scroll; wide tables
  scroll inside their own container.

## Serving

Built as a static bundle and served by `doiq-api` itself from the same origin
(Phase 2.3, 8.6): the API image builds the dashboard and sets
`DEPLOYIQ_SERVER_WEB_DIR=/opt/web`. Fingerprinted assets are cached as
immutable; `index.html` is not. No CDN, no object storage.
