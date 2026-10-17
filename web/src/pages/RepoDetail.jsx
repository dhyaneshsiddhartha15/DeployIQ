import { Link, useParams } from "react-router-dom";
import { listOptimizations } from "../api/client.js";
import { formatDate, formatMB, savingsPercent } from "../api/stats.js";
import { useApi } from "../api/useApi.js";
import SavingsChart from "../components/SavingsChart.jsx";
import { EmptyState, ErrorState, Loading } from "../components/States.jsx";

export default function RepoDetail() {
  // The route is /dashboard/*, because repository names contain slashes.
  const repo = useParams()["*"];
  const { status, data, error, retry } = useApi((signal) => listOptimizations({ repo, limit: 500 }, signal), [repo]);

  return (
    <>
      <p className="crumbs">
        <Link to="/dashboard">← All repositories</Link>
      </p>
      <div className="page-head">
        <h1>{repo}</h1>
        <p className="muted">History and trend of estimated image size.</p>
      </div>
      {status === "loading" && <Loading label="Loading history" />}
      {status === "error" && <ErrorState error={error} onRetry={retry} />}
      {status === "loaded" && data.length === 0 && (
        <EmptyState title="No reports for this repository">
          <p>
            Nothing has been reported under this name. <Link to="/dashboard">Back to your repositories</Link>.
          </p>
        </EmptyState>
      )}
      {status === "loaded" && data.length > 0 && (
        <>
          <SavingsChart runs={data} />
          <div className="table-wrap">
            <table className="table">
              <caption className="sr-only">Every reported run, newest first</caption>
              <thead>
                <tr>
                  <th scope="col">Date</th>
                  <th scope="col">Stack</th>
                  <th scope="col" className="num">
                    Before (est.)
                  </th>
                  <th scope="col" className="num">
                    After (est.)
                  </th>
                  <th scope="col" className="num">
                    Saving
                  </th>
                  <th scope="col">Rules applied</th>
                </tr>
              </thead>
              <tbody>
                {data.map((o) => (
                  <tr key={o.id}>
                    <td>{formatDate(o.createdAt)}</td>
                    <td>
                      <span className={`badge badge-${o.detectedStack}`}>{o.detectedStack}</span>
                    </td>
                    <td className="num">{formatMB(o.originalEstSizeMB)}</td>
                    <td className="num">{formatMB(o.optimizedEstSizeMB)}</td>
                    <td className="num">
                      <strong>{Math.round(savingsPercent(o))}%</strong>
                    </td>
                    <td>
                      <ul className="rules">
                        {o.rulesApplied.map((r) => (
                          <li key={r}>
                            <code>{r}</code>
                          </li>
                        ))}
                      </ul>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </>
  );
}
