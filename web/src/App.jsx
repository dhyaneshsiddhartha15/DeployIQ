import { Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { AuthProvider, useAuth } from "./auth/AuthContext.jsx";
import Layout from "./components/Layout.jsx";
import Dashboard from "./pages/Dashboard.jsx";
import Landing from "./pages/Landing.jsx";
import { Login, LoginCallback } from "./pages/Login.jsx";
import RepoDetail from "./pages/RepoDetail.jsx";

// Routes from Phase 6.1, plus the OAuth landing route.
//
//   /                 landing
//   /login            GitHub OAuth entry
//   /login/callback   receives the session token
//   /dashboard        repos analysed, most recent savings
//   /dashboard/:repo  history for one repo (a splat: names contain "/")
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
