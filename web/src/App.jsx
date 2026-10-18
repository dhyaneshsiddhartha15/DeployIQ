import { Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { AuthProvider, useAuth } from "./auth/AuthContext.jsx";
import Layout from "./components/Layout.jsx";
import Dashboard from "./pages/Dashboard.jsx";
import Landing from "./pages/Landing.jsx";
import { Login, LoginCallback } from "./pages/Login.jsx";
import RepoDetail from "./pages/RepoDetail.jsx";
import { OrgMembers, OrgOverview, OrgRepo, OrgSettings } from "./pages/Org.jsx";
import Orgs from "./pages/Orgs.jsx";

// Routes from Phase 6.1, plus the OAuth landing route.
//
//   /                 landing
//   /login            GitHub OAuth entry
//   /login/callback   receives the session token
//   /dashboard        repos analysed, most recent savings
//   /dashboard/:repo  history for one repo (a splat: names contain "/")
//   /orgs             your organizations; create one
//   /orgs/:slug       team dashboard with monthly cost saving
//   /orgs/:slug/members, /settings, /repos/*
export function AppRoutes() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Landing />} />
        <Route path="login" element={<Login />} />
        <Route path="login/callback" element={<LoginCallback />} />
        <Route
          path="dashboard"
          element={
            <RequireAuth>
              <Dashboard />
            </RequireAuth>
          }
        />
        <Route
          path="dashboard/*"
          element={
            <RequireAuth>
              <RepoDetail />
            </RequireAuth>
          }
        />
        <Route path="orgs" element={<RequireAuth><Orgs /></RequireAuth>} />
        <Route path="orgs/:slug" element={<RequireAuth><OrgOverview /></RequireAuth>} />
        <Route path="orgs/:slug/members" element={<RequireAuth><OrgMembers /></RequireAuth>} />
        <Route path="orgs/:slug/settings" element={<RequireAuth><OrgSettings /></RequireAuth>} />
        <Route path="orgs/:slug/repos/*" element={<RequireAuth><OrgRepo /></RequireAuth>} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}

function RequireAuth({ children }) {
  const { token } = useAuth();
  if (!token) return <Navigate to="/login" replace />;
  return children;
}

function NotFound() {
  return (
    <section className="card narrow">
      <h1>Page not found</h1>
      <p>
        <a href="/">Go to the home page</a>
      </p>
    </section>
  );
}

export default function App() {
  const navigate = useNavigate();
  return (
    <AuthProvider navigate={navigate}>
      <AppRoutes />
    </AuthProvider>
  );
}
