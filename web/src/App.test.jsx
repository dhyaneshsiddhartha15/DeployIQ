import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { getToken, listOptimizations, request, setToken, setUnauthorizedHandler } from "./api/client.js";
import { summarizeRepos, totals } from "./api/stats.js";
import App from "./App.jsx";
import SavingsChart from "./components/SavingsChart.jsx";

const runs = [
  { id: "3", repoName: "acme/api", detectedStack: "go", originalEstSizeMB: 900, optimizedEstSizeMB: 9, rulesApplied: ["multi-stage-build", "distroless-base"], createdAt: "2026-10-03T10:00:00Z" },
  { id: "2", repoName: "acme/web", detectedStack: "node", originalEstSizeMB: 1100, optimizedEstSizeMB: 180, rulesApplied: ["alpine-base"], createdAt: "2026-10-02T10:00:00Z" },
  { id: "1", repoName: "acme/api", detectedStack: "go", originalEstSizeMB: 900, optimizedEstSizeMB: 20, rulesApplied: ["multi-stage-build"], createdAt: "2026-10-01T10:00:00Z" },
];

function jsonResponse(status, body) {
  return Promise.resolve(new Response(body === undefined ? "" : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
}

// mockApi routes fetch calls the way the Go API would answer them.
function mockApi({ list = runs, me = { login: "octocat" }, fail } = {}) {
  return vi.spyOn(globalThis, "fetch").mockImplementation((url, opts) => {
    const u = new URL(url, "http://localhost");
    if (fail) return fail(u, opts);
    if (opts.headers.Authorization !== "Bearer tok") return jsonResponse(401, { error: { code: "unauthenticated", message: "no" } });
    if (u.pathname === "/api/v1/me") return jsonResponse(200, me);
    if (u.pathname === "/api/v1/optimizations") {
      const repo = u.searchParams.get("repo");
      return jsonResponse(200, { optimizations: repo ? list.filter((o) => o.repoName === repo) : list });
    }
    return jsonResponse(404, { error: { code: "not_found", message: "not found" } });
  });
}

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="location">{loc.pathname + loc.search}</div>;
}

function renderAt(path) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
      <LocationProbe />
    </MemoryRouter>,
  );
}

describe("api client", () => {
  it("attaches the bearer token and parses the error envelope", async () => {
    setToken("tok");
    const spy = vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      jsonResponse(400, { error: { code: "invalid_argument", message: "must not be empty", field: "repoName" } }),
    );
    await expect(request("/api/v1/optimizations", { method: "POST", body: {} })).rejects.toMatchObject({
      status: 400,
      code: "invalid_argument",
      field: "repoName",
      message: "must not be empty",
    });
    const [, opts] = spy.mock.calls[0];
    expect(opts.headers.Authorization).toBe("Bearer tok");
    expect(opts.headers["Content-Type"]).toBe("application/json");
  });

  it("clears the token and redirects on 401", async () => {
    setToken("expired");
    const onUnauth = vi.fn();
    setUnauthorizedHandler(onUnauth);
    vi.spyOn(globalThis, "fetch").mockImplementation(() => jsonResponse(401, {}));
    await expect(listOptimizations()).rejects.toMatchObject({ status: 401 });
    expect(getToken()).toBeNull();
    expect(onUnauth).toHaveBeenCalledOnce();
  });

  it("reports network failures in plain words", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() => Promise.reject(new TypeError("Failed to fetch")));
    await expect(request("/x")).rejects.toMatchObject({ code: "network" });
  });

  it("builds the list query", async () => {
    setToken("tok");
    const spy = mockApi();
    await listOptimizations({ repo: "acme/api", limit: 10 });
    expect(spy.mock.calls[0][0]).toBe("/api/v1/optimizations?repo=acme%2Fapi&limit=10");
  });
});

describe("stats", () => {
  it("summarises one row per repo with the latest run", () => {
    const repos = summarizeRepos(runs);
    expect(repos.map((r) => r.repoName)).toEqual(["acme/api", "acme/web"]);
    expect(repos[0].runs).toBe(2);
    expect(repos[0].latest.id).toBe("3");
    const t = totals(repos);
    expect(t.repos).toBe(2);
    expect(t.runs).toBe(3);
    expect(t.savedMB).toBe(891 + 920);
    expect(totals([])).toEqual({ repos: 0, runs: 0, avgSavings: 0, savedMB: 0 });
  });
});

describe("routing and auth", () => {
  it("renders the landing page for visitors", () => {
    renderAt("/");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("smaller, safer Dockerfile");
    expect(screen.getAllByRole("link", { name: /sign in with github/i })[0]).toHaveAttribute(
      "href",
      "/api/v1/auth/github/login?client=web",
    );
  });

  it("sends visitors to /login from the dashboard", () => {
    renderAt("/dashboard");
    expect(screen.getByTestId("location")).toHaveTextContent("/login");
    expect(screen.getByRole("heading", { name: "Sign in" })).toBeInTheDocument();
  });

  it("shows a readable message for OAuth errors", () => {
    renderAt("/login?error=access_denied");
    expect(screen.getByRole("alert")).toHaveTextContent("cancelled");
  });

  it("stores the token from the callback fragment and opens the dashboard", async () => {
    mockApi();
    renderAt("/login/callback#token=tok");
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/dashboard"));
    expect(getToken()).toBe("tok");
    expect(await screen.findByText("@octocat")).toBeInTheDocument();
  });

  it("rejects a callback without a token", async () => {
    renderAt("/login/callback");
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/login?error=missing_token"));
  });

  it("signs out", async () => {
    setToken("tok");
    mockApi();
    renderAt("/dashboard");
    await userEvent.click(await screen.findByRole("button", { name: "Sign out" }));
    expect(getToken()).toBeNull();
    expect(screen.getByTestId("location")).toHaveTextContent(/^\/$/);
  });
});

describe("dashboard", () => {
  it("goes loading -> loaded with tiles and one row per repo", async () => {
    setToken("tok");
    mockApi();
    renderAt("/dashboard");
    expect(screen.getByRole("status")).toHaveTextContent(/loading/i);
    const table = await screen.findByRole("table");
    const rows = within(table).getAllByRole("row");
    expect(rows).toHaveLength(3); // header + 2 repos
    expect(within(rows[1]).getByRole("link")).toHaveAttribute("href", "/dashboard/acme/api");
    expect(screen.getByText("Repositories").nextSibling).toHaveTextContent("2");
    expect(screen.getByText("Runs reported").nextSibling).toHaveTextContent("3");
  });

  it("shows the empty state with CLI instructions", async () => {
    setToken("tok");
    mockApi({ list: [] });
    renderAt("/dashboard");
    const title = await screen.findByText("No reports yet");
    expect(within(title.parentElement).getByText(/doiq -report/)).toBeInTheDocument();
  });

  it("shows the error state and retries", async () => {
    setToken("tok");
    let calls = 0;
    mockApi({
      fail: (u) => {
        if (u.pathname === "/api/v1/me") return jsonResponse(200, { login: "octocat" });
        calls += 1;
        return calls === 1 ? jsonResponse(500, { error: { code: "internal", message: "internal server error" } }) : jsonResponse(200, { optimizations: runs });
      },
    });
    renderAt("/dashboard");
    expect(await screen.findByRole("alert")).toHaveTextContent("internal server error");
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("table")).toBeInTheDocument();
  });

  it("redirects to login when the session expires mid-use", async () => {
    setToken("tok");
    mockApi({ fail: () => jsonResponse(401, { error: { code: "unauthenticated" } }) });
    renderAt("/dashboard");
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/login?error=session_expired"));
    expect(screen.getByRole("alert")).toHaveTextContent("expired");
  });
});

describe("repo detail", () => {
  it("loads the history of a slash-named repo with a chart and rules", async () => {
    setToken("tok");
    const spy = mockApi();
    renderAt("/dashboard/acme/api");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("acme/api");
    expect(await screen.findByRole("img", { name: /2 runs/ })).toBeInTheDocument();
    expect(screen.getAllByText("multi-stage-build")).toHaveLength(2);
    expect(spy.mock.calls.some(([url]) => url.includes("repo=acme%2Fapi"))).toBe(true);
  });

  it("shows an empty state for an unknown repo", async () => {
    setToken("tok");
    mockApi();
    renderAt("/dashboard/nobody/nothing");
    expect(await screen.findByText("No reports for this repository")).toBeInTheDocument();
  });
});

describe("SavingsChart", () => {
  it("draws one group per run, oldest first, with an accessible summary", () => {
    const { container } = render(<SavingsChart runs={runs} />);
    expect(screen.getAllByTestId("run")).toHaveLength(3);
    expect(screen.getByRole("img")).toHaveAccessibleName(/3 runs.*Latest: 900 MB before, 9 MB after/);
    expect(container.querySelector("figcaption")).toHaveTextContent("estimates");
    expect(container.querySelector("pattern#hatch")).not.toBeNull(); // not colour alone
  });

  it("renders nothing without data", () => {
    const { container } = render(<SavingsChart runs={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});
