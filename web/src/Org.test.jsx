import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { setToken } from "./api/client.js";
import App from "./App.jsx";

const org = {
  id: "o1",
  slug: "acme",
  name: "Acme Platform",
  role: "admin",
  settings: { storagePerGBMonthUSD: 0.1, transferPerGBUSD: 0.09, pullsPerDay: 20, versionsKept: 30 },
};
const summary = {
  org,
  members: 3,
  runs: 4,
  savedMB: 1810,
  avgSavedPercent: 90,
  monthlySavedUSD: 1234.5,
  repos: [
    {
      repoName: "acme/web",
      runs: 2,
      reportedBy: "octocat",
      monthlySavedUSD: 900,
      latest: { id: "1", repoName: "acme/web", detectedStack: "node", originalEstSizeMB: 1100, optimizedEstSizeMB: 180, rulesApplied: [], createdAt: "2026-10-02T10:00:00Z" },
    },
  ],
};
const members = {
  members: [
    { user: { id: "u1", login: "octocat", provider: "github" }, role: "admin" },
    { user: { id: "u2", name: "Pat", email: "pat@acme.com", provider: "oidc" }, role: "member" },
  ],
  invitations: [{ id: "i1", email: "new@acme.com", role: "member" }],
};

function json(status, body) {
  return Promise.resolve(new Response(JSON.stringify(body ?? {}), { status, headers: { "Content-Type": "application/json" } }));
}

function mockApi(overrides = {}) {
  const calls = [];
  vi.spyOn(globalThis, "fetch").mockImplementation((url, opts) => {
    const u = new URL(url, "http://localhost");
    calls.push({ method: opts.method, path: u.pathname, body: opts.body && JSON.parse(opts.body) });
    const key = `${opts.method} ${u.pathname}`;
    if (overrides[key]) return overrides[key](opts);
    switch (key) {
      case "GET /api/v1/auth/providers":
        return json(200, { github: true, oidc: { enabled: true, name: "Okta" } });
      case "GET /api/v1/me":
        return json(200, { id: "u1", login: "octocat" });
      case "GET /api/v1/orgs":
        return json(200, { orgs: [org] });
      case "GET /api/v1/orgs/acme/summary":
        return json(200, summary);
      case "GET /api/v1/orgs/acme/members":
        return json(200, members);
      case "POST /api/v1/orgs":
        return json(200, { ...org, slug: JSON.parse(opts.body).slug });
      case "POST /api/v1/orgs/acme/members":
        return json(200, { invitation: { id: "i2" } });
      case "PATCH /api/v1/orgs/acme/members/u2":
        return json(200, {});
      case "PUT /api/v1/orgs/acme/settings":
        return json(200, org);
    }
    return json(404, { error: { code: "not_found", message: "not found" } });
  });
  return { calls };
}

function Where() {
  const l = useLocation();
  return <div data-testid="where">{l.pathname}</div>;
}

function renderAt(path) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
      <Where />
    </MemoryRouter>,
  );
}

describe("sign-in options", () => {
  it("offers SSO when the server has it", async () => {
    mockApi();
    renderAt("/login");
    const sso = await screen.findByRole("link", { name: "Sign in with Okta" });
    expect(sso).toHaveAttribute("href", "/api/v1/auth/oidc/login?client=web");
    expect(screen.getAllByRole("link", { name: /sign in with github/i }).length).toBeGreaterThan(0);
  });

  it("explains a refused domain", async () => {
    mockApi();
    renderAt("/login?error=domain_not_allowed");
    expect(await screen.findByRole("alert")).toHaveTextContent("not in a domain allowed");
  });
});

describe("organizations", () => {
  it("lists organizations and creates one", async () => {
    setToken("tok");
    const { calls } = mockApi();
    renderAt("/orgs");
    expect(await screen.findByText("Acme Platform")).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("Name"), "Data Team");
    expect(screen.getByLabelText("URL name")).toHaveValue("data-team");
    await userEvent.click(screen.getByRole("button", { name: "Create organization" }));
    await waitFor(() => expect(screen.getByTestId("where")).toHaveTextContent("/orgs/data-team"));
    expect(calls.find((c) => c.method === "POST").body).toEqual({ slug: "data-team", name: "Data Team" });
  });

  it("shows the team dashboard with the monthly saving", async () => {
    setToken("tok");
    mockApi();
    renderAt("/orgs/acme");
    expect(await screen.findByText("$1,235")).toBeInTheDocument(); // whole dollars above $100
    const row = screen.getByRole("row", { name: /acme\/web/ });
    expect(within(row).getByText("octocat")).toBeInTheDocument();
    expect(within(row).getByText("$900")).toBeInTheDocument();
    expect((await screen.findAllByRole("link", { name: "Settings" })).length).toBeGreaterThan(0); // admin tab
  });

  it("manages members as an admin", async () => {
    setToken("tok");
    const { calls } = mockApi();
    renderAt("/orgs/acme/members");
    expect(await screen.findByText("Pat")).toBeInTheDocument();
    expect(screen.getByText("SSO")).toBeInTheDocument();
    expect(screen.getByText("new@acme.com")).toBeInTheDocument();
    await userEvent.selectOptions(await screen.findByLabelText("Role of pat@acme.com"), "admin");
    await waitFor(() => expect(calls.some((c) => c.method === "PATCH" && c.body.role === "admin")).toBe(true));
    await userEvent.type(screen.getByLabelText(/Email address or GitHub username/), "@hubot");
    await userEvent.click(screen.getByRole("button", { name: "Invite" }));
    expect(await screen.findByText("@hubot will join when they first sign in.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST").body).toEqual({ login: "hubot", role: "member" });
  });

  it("hides admin controls from members", async () => {
    setToken("tok");
    mockApi({ "GET /api/v1/orgs": () => json(200, { orgs: [{ ...org, role: "member" }] }) });
    renderAt("/orgs/acme/members");
    expect(await screen.findByText("Pat")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("button", { name: "Invite" })).not.toBeInTheDocument());
    expect(screen.queryByRole("link", { name: "Settings" })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });

  it("saves cost settings and shows field errors", async () => {
    setToken("tok");
    let fail = true;
    const { calls } = mockApi({
      "PUT /api/v1/orgs/acme/settings": () => {
        if (fail) {
          fail = false;
          return json(400, { error: { code: "invalid_argument", message: "must be between 0 and 1000000", field: "pullsPerDay" } });
        }
        return json(200, org);
      },
    });
    renderAt("/orgs/acme/settings");
    const pulls = await screen.findByLabelText("Pulls per image per day");
    await userEvent.clear(pulls);
    await userEvent.type(pulls, "50");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("pullsPerDay: must be between");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Saved.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT").at(-1).body.pullsPerDay).toBe(50);
  });

  it("shows how to report into an empty organization", async () => {
    setToken("tok");
    mockApi({ "GET /api/v1/orgs/acme/summary": () => json(200, { ...summary, repos: [] }) });
    renderAt("/orgs/acme");
    expect(await screen.findByText("No reports yet")).toBeInTheDocument();
    expect(screen.getByText(/doiq -report -org acme/)).toBeInTheDocument();
  });
});
