import { useState } from "react";
import { Link, NavLink, useNavigate, useParams } from "react-router-dom";
import {
  deleteInvitation,
  getOrgSummary,
  inviteMember,
  listMembers,
  listOrgOptimizations,
  listOrgs,
  removeMember,
  updateMember,
  updateSettings,
} from "../api/client.js";
import { formatDate, formatMB, savingsPercent } from "../api/stats.js";
import { useApi } from "../api/useApi.js";
import { useAuth } from "../auth/AuthContext.jsx";
import SavingsChart from "../components/SavingsChart.jsx";
import { EmptyState, ErrorState, Loading } from "../components/States.jsx";

export function formatUSD(v) {
  return `$${(v ?? 0).toLocaleString(undefined, { minimumFractionDigits: 0, maximumFractionDigits: v >= 100 ? 0 : 2 })}`;
}

// useRole returns the caller's role in the organisation, from the org list.
function useRole(slug) {
  const orgs = useApi((signal) => listOrgs(signal), [slug]);
  return orgs.status === "loaded" ? orgs.data.find((o) => o.slug === slug)?.role : undefined;
}

function OrgTabs({ slug, isAdmin }) {
  return (
    <nav className="tabs" aria-label="Organization">
      <NavLink end to={`/orgs/${slug}`}>
        Overview
      </NavLink>
      <NavLink to={`/orgs/${slug}/members`}>Members</NavLink>
      {isAdmin && <NavLink to={`/orgs/${slug}/settings`}>Settings</NavLink>}
    </nav>
  );
}

// OrgOverview is the team dashboard: what every member reported, what it
// would save, and what that is worth per month.
export function OrgOverview() {
  const { slug } = useParams();
  const role = useRole(slug);
  const { status, data, error, retry } = useApi((signal) => getOrgSummary(slug, signal), [slug]);
  return (
    <>
      <p className="crumbs">
        <Link to="/orgs">← Organizations</Link>
      </p>
      <div className="page-head">
        <h1>{data?.org?.name ?? slug}</h1>
        <p className="muted">Latest result of every repository your team reported.</p>
      </div>
      <OrgTabs slug={slug} isAdmin={role === "admin"} />
      {status === "loading" && <Loading label="Loading the organization" />}
      {status === "error" && <ErrorState error={error} onRetry={retry} />}
      {status === "loaded" && data.repos.length === 0 && (
        <EmptyState title="No reports yet">
          <p>Members report into this organization with:</p>
          <pre className="terminal">
            <code>
              <span className="prompt">$</span> doiq -report -org {slug} .{"\n"}
              <span className="prompt">$</span> doiq scan -report -org {slug} ~/src
            </code>
          </pre>
        </EmptyState>
      )}
      {status === "loaded" && data.repos.length > 0 && (
        <>
          <dl className="tiles tiles-5">
            <div className="tile">
              <dt>Repositories</dt>
              <dd>{data.repos.length}</dd>
            </div>
            <div className="tile">
              <dt>Members</dt>
              <dd>{data.members}</dd>
            </div>
            <div className="tile">
              <dt>Average saving</dt>
              <dd>{Math.round(data.avgSavedPercent)}%</dd>
            </div>
            <div className="tile">
              <dt>Image size saved</dt>
              <dd>{formatMB(data.savedMB)}</dd>
            </div>
            <div className="tile tile-accent">
              <dt>Est. saving per month</dt>
              <dd>{formatUSD(data.monthlySavedUSD)}</dd>
            </div>
          </dl>
          <div className="table-wrap">
            <table className="table">
              <caption className="sr-only">Repositories by estimated monthly saving</caption>
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
                    Per month
                  </th>
                  <th scope="col">Reported by</th>
                  <th scope="col">Last run</th>
                </tr>
              </thead>
              <tbody>
                {data.repos.map((r) => (
                  <tr key={r.repoName}>
                    <th scope="row">
                      <Link to={`/orgs/${slug}/repos/${r.repoName}`}>{r.repoName}</Link>
                    </th>
                    <td>
                      <span className="badge">{r.latest.detectedStack}</span>
                    </td>
                    <td className="num">{formatMB(r.latest.originalEstSizeMB)}</td>
                    <td className="num">{formatMB(r.latest.optimizedEstSizeMB)}</td>
                    <td className="num">
                      <strong>{Math.round(savingsPercent(r.latest))}%</strong>
                    </td>
                    <td className="num">{formatUSD(r.monthlySavedUSD)}</td>
                    <td>{r.reportedBy || "—"}</td>
                    <td>{formatDate(r.latest.createdAt)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="muted small">
            Monthly saving = smaller images × retained versions × storage price, plus × pulls per day × transfer price.
            {role === "admin" ? (
              <>
                {" "}
                Adjust the prices in <Link to={`/orgs/${slug}/settings`}>Settings</Link>.
              </>
            ) : null}
          </p>
        </>
      )}
    </>
  );
}

// OrgRepo is one repository's history inside the organisation.
export function OrgRepo() {
  const { slug, "*": repo } = useParams();
  const { status, data, error, retry } = useApi((signal) => listOrgOptimizations(slug, { repo }, signal), [slug, repo]);
  return (
    <>
      <p className="crumbs">
        <Link to={`/orgs/${slug}`}>← {slug}</Link>
      </p>
      <div className="page-head">
        <h1>{repo}</h1>
      </div>
      {status === "loading" && <Loading label="Loading history" />}
      {status === "error" && <ErrorState error={error} onRetry={retry} />}
      {status === "loaded" && data.length === 0 && <EmptyState title="No reports for this repository" />}
      {status === "loaded" && data.length > 0 && (
        <>
          <SavingsChart runs={data} />
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Date</th>
                  <th scope="col" className="num">
                    Before (est.)
                  </th>
                  <th scope="col" className="num">
                    After (est.)
                  </th>
                  <th scope="col">Reported by</th>
                  <th scope="col">Rules applied</th>
                </tr>
              </thead>
              <tbody>
                {data.map((o) => (
                  <tr key={o.id}>
                    <td>{formatDate(o.createdAt)}</td>
                    <td className="num">{formatMB(o.originalEstSizeMB)}</td>
                    <td className="num">{formatMB(o.optimizedEstSizeMB)}</td>
                    <td>{o.reportedBy || "—"}</td>
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

// OrgMembers lists members and invitations; admins manage them.
export function OrgMembers() {
  const { slug } = useParams();
  const role = useRole(slug);
  const isAdmin = role === "admin";
  const { user } = useAuth();
  const navigate = useNavigate();
  const { status, data, error, retry } = useApi((signal) => listMembers(slug, signal), [slug]);
  const [actionError, setActionError] = useState(null);

  async function act(fn) {
    setActionError(null);
    try {
      await fn();
      retry();
    } catch (err) {
      setActionError(err);
    }
  }

  return (
    <>
      <p className="crumbs">
        <Link to="/orgs">← Organizations</Link>
      </p>
      <div className="page-head">
        <h1>Members</h1>
      </div>
      <OrgTabs slug={slug} isAdmin={isAdmin} />
      {actionError && (
        <p className="notice notice-error" role="alert">
          {actionError.message}
        </p>
      )}
      {status === "loading" && <Loading label="Loading members" />}
      {status === "error" && <ErrorState error={error} onRetry={retry} />}
      {status === "loaded" && (
        <>
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Member</th>
                  <th scope="col">Sign-in</th>
                  <th scope="col">Role</th>
                  <th scope="col">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {data.members.map((m) => {
                  const self = user?.id === m.user.id;
                  return (
                    <tr key={m.user.id}>
                      <th scope="row">
                        {m.user.login || m.user.name || m.user.email}
                        {m.user.email && <div className="muted small">{m.user.email}</div>}
                      </th>
                      <td>{m.user.provider === "oidc" ? "SSO" : "GitHub"}</td>
                      <td>
                        {isAdmin ? (
                          <select
                            aria-label={`Role of ${m.user.login || m.user.email}`}
                            value={m.role}
                            onChange={(e) => act(() => updateMember(slug, m.user.id, e.target.value))}
                          >
                            <option value="member">member</option>
                            <option value="admin">admin</option>
                          </select>
                        ) : (
                          m.role
                        )}
                      </td>
                      <td className="num">
                        {(isAdmin || self) && (
                          <button
                            type="button"
                            className="btn btn-sm"
                            onClick={() =>
                              act(async () => {
                                await removeMember(slug, m.user.id);
                                if (self) navigate("/orgs");
                              })
                            }
                          >
                            {self ? "Leave" : "Remove"}
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {data.invitations.length > 0 && (
            <>
              <h2>Pending invitations</h2>
              <ul className="invites">
                {data.invitations.map((inv) => (
                  <li key={inv.id}>
                    <span>
                      {inv.email || `@${inv.login}`} <span className="badge">{inv.role}</span>
                    </span>
                    {isAdmin && (
                      <button type="button" className="btn btn-sm" onClick={() => act(() => deleteInvitation(slug, inv.id))}>
                        Withdraw
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            </>
          )}
          {isAdmin && <InviteForm slug={slug} onDone={retry} />}
        </>
      )}
    </>
  );
}

function InviteForm({ slug, onDone }) {
  const [who, setWho] = useState("");
  const [role, setRole] = useState("member");
  const [msg, setMsg] = useState(null);
  const [error, setError] = useState(null);

  async function submit(e) {
    e.preventDefault();
    setMsg(null);
    setError(null);
    const value = who.trim();
    // "@hubot" is a GitHub handle, not an email: only an @ after the first
    // character makes an address.
    const isEmail = value.indexOf("@") > 0;
    const body = isEmail ? { email: value, role } : { login: value.replace(/^@/, ""), role };
    try {
      const res = await inviteMember(slug, body);
      setMsg(res.member ? `${value} was added.` : `${value} will join when they first sign in.`);
      setWho("");
      onDone();
    } catch (err) {
      setError(err);
    }
  }

  return (
    <form className="card form" onSubmit={submit} aria-labelledby="invite">
      <h2 id="invite">Invite someone</h2>
      <label>
        Email address or GitHub username
        <input value={who} onChange={(e) => setWho(e.target.value)} required placeholder="pat@acme.com or octocat" />
      </label>
      <label>
        Role
        <select value={role} onChange={(e) => setRole(e.target.value)}>
          <option value="member">member — sees and reports</option>
          <option value="admin">admin — also manages members and settings</option>
        </select>
      </label>
      {msg && (
        <p className="notice" role="status">
          {msg}
        </p>
      )}
      {error && (
        <p className="notice notice-error" role="alert">
          {error.message}
        </p>
      )}
      <button type="submit" className="btn btn-primary">
        Invite
      </button>
    </form>
  );
}

const settingFields = [
  ["storagePerGBMonthUSD", "Registry storage, $ per GB-month", "0.01"],
  ["transferPerGBUSD", "Registry transfer, $ per GB", "0.01"],
  ["pullsPerDay", "Pulls per image per day", "1"],
  ["versionsKept", "Image versions kept per repository", "1"],
];

// OrgSettings edits the cost-estimate parameters. Admins only.
export function OrgSettings() {
  const { slug } = useParams();
  const role = useRole(slug);
  const summary = useApi((signal) => getOrgSummary(slug, signal), [slug]);
  const [form, setForm] = useState(null);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState(null);

  const values = form ?? summary.data?.org?.settings ?? {};

  async function submit(e) {
    e.preventDefault();
    setError(null);
    setSaved(false);
    const body = Object.fromEntries(settingFields.map(([k]) => [k, Number(values[k] ?? 0)]));
    try {
      await updateSettings(slug, body);
      setSaved(true);
      summary.retry();
      setForm(null);
    } catch (err) {
      setError(err);
    }
  }

  return (
    <>
      <p className="crumbs">
        <Link to="/orgs">← Organizations</Link>
      </p>
      <div className="page-head">
        <h1>Settings</h1>
        <p className="muted">Prices used for the monthly saving. Defaults are typical public-cloud list prices.</p>
      </div>
      <OrgTabs slug={slug} isAdmin={role === "admin"} />
      {summary.status === "loading" && <Loading label="Loading settings" />}
      {summary.status === "error" && <ErrorState error={summary.error} onRetry={summary.retry} />}
      {summary.status === "loaded" && role !== undefined && role !== "admin" && (
        <EmptyState title="Only admins can change settings" />
      )}
      {summary.status === "loaded" && role === "admin" && (
        <form className="card form" onSubmit={submit}>
          {settingFields.map(([key, label, step]) => (
            <label key={key}>
              {label}
              <input
                type="number"
                min="0"
                step={step}
                value={values[key] ?? ""}
                onChange={(e) => setForm({ ...values, [key]: e.target.value })}
                required
              />
            </label>
          ))}
          {saved && (
            <p className="notice" role="status">
              Saved.
            </p>
          )}
          {error && (
            <p className="notice notice-error" role="alert">
              {error.field ? `${error.field}: ${error.message}` : error.message}
            </p>
          )}
          <button type="submit" className="btn btn-primary">
            Save
          </button>
        </form>
      )}
    </>
  );
}
