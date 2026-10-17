// Pure helpers that turn the optimization history into what the dashboard
// shows. Kept out of components so they are trivially testable.

export function savingsPercent(o) {
  if (!o || o.originalEstSizeMB <= 0) return 0;
  const saved = o.originalEstSizeMB - o.optimizedEstSizeMB;
  return saved > 0 ? (saved / o.originalEstSizeMB) * 100 : 0;
}

// summarizeRepos groups records (newest first, as the API returns them) into
// one row per repository: its latest run, run count and best saving.
export function summarizeRepos(records) {
  const byRepo = new Map();
  for (const o of records) {
    let s = byRepo.get(o.repoName);
    if (!s) {
      s = { repoName: o.repoName, latest: o, runs: 0, bestSavings: 0 };
      byRepo.set(o.repoName, s);
    }
    s.runs += 1;
    if (new Date(o.createdAt) > new Date(s.latest.createdAt)) s.latest = o;
    s.bestSavings = Math.max(s.bestSavings, savingsPercent(o));
  }
  return [...byRepo.values()].sort((a, b) => new Date(b.latest.createdAt) - new Date(a.latest.createdAt));
}

// totals computes the headline tiles from the latest run of each repository.
// Phase 0.5.1 tracks average reported size reduction against a 50% target.
export function totals(repos) {
  if (repos.length === 0) return { repos: 0, runs: 0, avgSavings: 0, savedMB: 0 };
  let runs = 0;
  let savedMB = 0;
  let pct = 0;
  for (const r of repos) {
    runs += r.runs;
    savedMB += Math.max(0, r.latest.originalEstSizeMB - r.latest.optimizedEstSizeMB);
    pct += savingsPercent(r.latest);
  }
  return { repos: repos.length, runs, avgSavings: pct / repos.length, savedMB };
}

export function formatMB(mb) {
  // From 1000 rather than 1024, so an axis never reads "1000 MB" next to
  // "1.5 GB".
  if (mb >= 1000) return `${(mb / 1024).toFixed(1)} GB`;
  return `${Math.round(mb)} MB`;
}

export function formatDate(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}
