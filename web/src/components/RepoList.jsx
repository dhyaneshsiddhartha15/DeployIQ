import { Link } from "react-router-dom";
import { formatDate, formatMB, savingsPercent } from "../api/stats.js";

// RepoList shows one row per analysed repository with its most recent saving.
export default function RepoList({ repos }) {
  return (
    <div className="table-wrap">
      <table className="table">
        <caption className="sr-only">Repositories analysed, most recent first</caption>
        <thead>
          <tr>
            <th scope="col">Repository</th>
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
            <th scope="col" className="num">
              Runs
            </th>
            <th scope="col">Last run</th>
          </tr>
        </thead>
        <tbody>
          {repos.map((r) => (
            <tr key={r.repoName}>
              <th scope="row">
                <Link to={`/dashboard/${r.repoName}`}>{r.repoName}</Link>
              </th>
              <td>
                <span className={`badge badge-${r.latest.detectedStack}`}>{r.latest.detectedStack}</span>
              </td>
              <td className="num">{formatMB(r.latest.originalEstSizeMB)}</td>
              <td className="num">{formatMB(r.latest.optimizedEstSizeMB)}</td>
              <td className="num">
                <strong>{Math.round(savingsPercent(r.latest))}%</strong>
              </td>
              <td className="num">{r.runs}</td>
              <td>{formatDate(r.latest.createdAt)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
