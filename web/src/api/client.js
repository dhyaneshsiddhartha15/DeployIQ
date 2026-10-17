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

  if (res.status === 401) {
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

export function loginURL() {
  return `${API_BASE}/api/v1/auth/github/login?client=web`;
}
