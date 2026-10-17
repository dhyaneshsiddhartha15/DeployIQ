import { listOptimizations } from "../api/client.js";
import { formatMB, summarizeRepos, totals } from "../api/stats.js";
import { useApi } from "../api/useApi.js";
import RepoList from "../components/RepoList.jsx";
import { EmptyState, ErrorState, Loading } from "../components/States.jsx";

export default function Dashboard() {
  const { status, data, error, retry } = useApi((signal) => listOptimizations({ limit: 500 }, signal), []);

  return (
    <>
      <div className="page-head">
        <h1>Your repositories</h1>
        <p className="muted">Most recent estimated savings for every repository you have reported.</p>
      </div>
      {status === "loading" && <Loading label="Loading your history" />}
      {status === "error" && <ErrorState error={error} onRetry={retry} />}
      {status === "loaded" && <Loaded records={data} />}
    </>
  );
}

function Loaded({ records }) {
  if (records.length === 0) {
    return (
      <EmptyState title="No reports yet">
        <p>Run the CLI with reporting turned on to see results here:</p>
        <pre className="terminal">
          <code>
            <span className="prompt">$</span> doiq login{"\n"}
            <span className="prompt">$</span> doiq -report .
          </code>
        </pre>
      </EmptyState>
    );
  }
  const repos = summarizeRepos(records);
  const t = totals(repos);
  return (
    <>
      <dl className="tiles">
        <div className="tile">
          <dt>Repositories</dt>
          <dd>{t.repos}</dd>
        </div>
        <div className="tile">
          <dt>Runs reported</dt>
          <dd>{t.runs}</dd>
        </div>
        <div className="tile">
          <dt>Average saving</dt>
          <dd>{Math.round(t.avgSavings)}%</dd>
        </div>
        <div className="tile">
          <dt>Estimated MB saved</dt>
          <dd>{formatMB(t.savedMB)}</dd>
        </div>
      </dl>
      <RepoList repos={repos} />
    </>
  );
}
