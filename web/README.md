# Dashboard (Phase 5+)

The React dashboard that visualizes optimization history and trends. Not built
yet, and gated: it arrives only once Phase 4's API has real usage behind it
(Phase 2.2, Phase 13.1).

Nothing is scaffolded here on purpose. A generated project skeleton would be
stale before anyone opens it, and would put `node_modules` in a repository whose
product is a single Go binary. This file records the decisions already made in
Phase 6 so the work starts from a plan rather than a blank directory.

## Routes (Phase 6.1)

| Route              | Purpose                                      |
| ------------------ | -------------------------------------------- |
| `/`                | Landing — why use this                       |
| `/login`           | GitHub OAuth entry                           |
| `/dashboard`       | Repos analyzed, most recent savings          |
| `/dashboard/:repo` | History and trend for a single repo          |

## Structure (Phase 6.3)

```
web/src/
├── pages/       Landing.jsx  Dashboard.jsx  RepoDetail.jsx
├── components/  SavingsChart.jsx  RepoList.jsx  AuthButton.jsx
├── api/         client.js — thin fetch wrapper over the Go API
└── App.jsx
```

## Decisions already taken

- **State: plain `useState` / `useContext`** (Phase 6.2). A handful of screens
  and one data source. Redux would be unjustified complexity at this size.
- **Three explicit states per data-fetching component** — loading, error,
  loaded. No silent blank screens (Phase 6.4).
- **The API client centralizes auth** — it attaches the session token and
  handles 401 by redirecting to `/login`, so no component does it individually.
- **Color-blind-safe palette** (Phase 6.5). The core visual is a savings chart,
  so contrast and palette choice matter more here than on a typical dashboard.
- **Mobile is secondary but not broken** — layout must hold above ~768px.

## Serving

Built as a static bundle and served from the same or an adjacent host
(Phase 2.3). No CDN and no object storage: the bundle is small enough to serve
directly at launch (Phase 8.6).
