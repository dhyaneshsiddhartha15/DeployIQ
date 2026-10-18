import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { createOrg, listOrgs } from "../api/client.js";
import { useApi } from "../api/useApi.js";
import { EmptyState, ErrorState, Loading } from "../components/States.jsx";

// Orgs lists the caller's organisations and creates new ones.
export default function Orgs() {
  const { status, data, error, retry } = useApi((signal) => listOrgs(signal), []);
  return (
    <>
      <div className="page-head">
        <h1>Organizations</h1>
        <p className="muted">Share results with your team: everyone in an organization sees what its members report.</p>
      </div>
      {status === "loading" && <Loading label="Loading organizations" />}
      {status === "error" && <ErrorState error={error} onRetry={retry} />}
      {status === "loaded" &&
        (data.length === 0 ? (
          <EmptyState title="You are not in any organization yet">
            <p>Create one below, or ask an admin to invite you.</p>
          </EmptyState>
        ) : (
          <ul className="org-list">
            {data.map((o) => (
              <li key={o.id} className="tile">
                <Link to={`/orgs/${o.slug}`} className="org-link">
                  <strong>{o.name}</strong>
                  <span className="muted small">{o.slug}</span>
                </Link>
                <span className="badge">{o.role}</span>
              </li>
            ))}
          </ul>
        ))}
      <CreateOrg />
    </>
  );
}

function CreateOrg() {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [touched, setTouched] = useState(false);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);

  const autoSlug = (n) =>
    n
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 40);

  async function submit(e) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const org = await createOrg(slug, name);
      navigate(`/orgs/${org.slug}`);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="card form" onSubmit={submit} aria-labelledby="create-org">
      <h2 id="create-org">Create an organization</h2>
      <label>
        Name
        <input
          value={name}
          onChange={(e) => {
            setName(e.target.value);
            if (!touched) setSlug(autoSlug(e.target.value));
          }}
          required
          maxLength={100}
        />
      </label>
      <label>
        URL name
        <input
          value={slug}
          onChange={(e) => {
            setTouched(true);
            setSlug(e.target.value);
          }}
          required
          pattern="[a-z0-9][a-z0-9-]{1,38}[a-z0-9]"
          title="3-40 lowercase letters, digits or hyphens"
        />
      </label>
      {error && (
        <p className="notice notice-error" role="alert">
          {error.field ? `${error.field}: ${error.message}` : error.message}
        </p>
      )}
      <button type="submit" className="btn btn-primary" disabled={busy}>
        {busy ? "Creating…" : "Create organization"}
      </button>
    </form>
  );
}
