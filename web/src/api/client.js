// Thin fetch wrapper over the Go API (Phase 6.3).
//
// It is the only place that knows about the session token: every request gets
// the Authorization header here, and every 401 clears the token and sends the
// user to /login here (Phase 6.4), so no component handles auth itself.

const TOKEN_KEY = "doiq_token";

// Same origin by default (Phase 2.3: one host serves API and dashboard).
// VITE_API_URL points the dashboard at an API on another host.
export const API_BASE = (import.meta.env?.VITE_API_URL ?? "").replace(/\/$/, "");

// Storage can throw (private windows, blocked site data); auth must degrade to
// "logged out", never crash the page.
export function getToken() {
  try {
    return window.localStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export function setToken(token) {
  try {
    if (token) window.localStorage.setItem(TOKEN_KEY, token);
    else window.localStorage.removeItem(TOKEN_KEY);
  } catch {
    /* storage unavailable: the session lasts for this page only */
  }
}

export class ApiError extends Error {
  constructor(status, code, message, field) {
    super(message || `Request failed (${status})`);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.field = field;
  }
}

// onUnauthorized is replaceable so tests (and the auth provider) can observe
// the redirect instead of performing a real navigation.
let onUnauthorized = () => {
  window.location.assign("/login");
};

export function setUnauthorizedHandler(fn) {
  onUnauthorized = fn;
}

export async function request(path, { method = "GET", body, signal } = {}) {
  const headers = { Accept: "application/json" };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";

  let res;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (err) {
    if (err?.name === "AbortError") throw err;
    throw new ApiError(0, "network", "Could not reach the DeployIQ API. Check your connection and try again.");
  }

  // Only a request that carried a session can have its session expire; a 401
  // without one (a stale render after sign-out) must not bounce the user.
  if (res.status === 401 && token) {
    setToken(null);
    onUnauthorized();
    throw new ApiError(401, "unauthenticated", "Your session has expired. Please sign in again.");
  }

  let data = null;
  const text = await res.text();
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    const e = data?.error ?? {};
    throw new ApiError(res.status, e.code ?? "error", e.message, e.field);
  }
  return data;
}

export function getMe(signal) {
  return request("/api/v1/me", { signal });
}

export async function listOptimizations({ repo, limit = 500 } = {}, signal) {
  const q = new URLSearchParams();
  if (repo) q.set("repo", repo);
  if (limit) q.set("limit", String(limit));
  const data = await request(`/api/v1/optimizations?${q}`, { signal });
  return data?.optimizations ?? [];
}

// loginURL starts a browser login through "github" or "oidc" (company SSO).
export function loginURL(provider = "github") {
  return `${API_BASE}/api/v1/auth/${provider}/login?client=web`;
}

// getProviders says which sign-in methods the server offers. It needs no
// session, and a failure falls back to GitHub only.
export async function getProviders(signal) {
  try {
    return await request("/api/v1/auth/providers", { signal });
  } catch (err) {
    if (err?.name === "AbortError") throw err;
    return { github: true, oidc: { enabled: false } };
  }
}

// --- organizations ---------------------------------------------------------

const org = (slug) => `/api/v1/orgs/${encodeURIComponent(slug)}`;

export async function listOrgs(signal) {
  const data = await request("/api/v1/orgs", { signal });
  return data?.orgs ?? [];
}

export function createOrg(slug, name) {
  return request("/api/v1/orgs", { method: "POST", body: { slug, name } });
}

export function getOrgSummary(slug, signal) {
  return request(`${org(slug)}/summary`, { signal });
}

export async function listOrgOptimizations(slug, { repo, limit = 500 } = {}, signal) {
  const q = new URLSearchParams();
  if (repo) q.set("repo", repo);
  if (limit) q.set("limit", String(limit));
  const data = await request(`${org(slug)}/optimizations?${q}`, { signal });
  return data?.optimizations ?? [];
}

export function listMembers(slug, signal) {
  return request(`${org(slug)}/members`, { signal });
}

export function inviteMember(slug, { email, login, role }) {
  return request(`${org(slug)}/members`, { method: "POST", body: { email, login, role } });
}

export function updateMember(slug, userId, role) {
  return request(`${org(slug)}/members/${encodeURIComponent(userId)}`, { method: "PATCH", body: { role } });
}

export function removeMember(slug, userId) {
  return request(`${org(slug)}/members/${encodeURIComponent(userId)}`, { method: "DELETE" });
}

export function deleteInvitation(slug, id) {
  return request(`${org(slug)}/invitations/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export function updateSettings(slug, settings) {
  return request(`${org(slug)}/settings`, { method: "PUT", body: settings });
}
