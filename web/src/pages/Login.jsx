import { useEffect } from "react";
import { Navigate, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { getProviders } from "../api/client.js";
import { useApi } from "../api/useApi.js";
import { useAuth } from "../auth/AuthContext.jsx";
import AuthButton from "../components/AuthButton.jsx";
import { Loading } from "../components/States.jsx";

const errors = {
  access_denied: "Sign-in was cancelled.",
  github_error: "GitHub could not complete the sign-in. Please try again.",
  sso_error: "Your identity provider could not complete the sign-in. Please try again.",
  login_failed: "Sign-in failed. Please try again.",
  domain_not_allowed: "Your account is not in a domain allowed to use this dashboard. Ask your administrator.",
  session_expired: "Your session has expired. Please sign in again.",
  missing_token: "The sign-in did not return a session. Please try again.",
};

export function Login() {
  const { token } = useAuth();
  const [params] = useSearchParams();
  const providers = useApi((signal) => getProviders(signal), []);
  if (token) return <Navigate to="/dashboard" replace />;
  const err = params.get("error");
  return (
    <section className="card narrow">
      <h1>Sign in</h1>
      <p>
        Your dashboard shows the optimization results you report from the CLI with <code>doiq -report</code>.
      </p>
      {err && (
        <p className="notice notice-error" role="alert">
          {errors[err] ?? "Sign-in failed. Please try again."}
        </p>
      )}
      {providers.status === "loading" && <Loading label="Loading sign-in options" />}
      {providers.status !== "loading" && (
        <div className="stack">
          {providers.data?.oidc?.enabled && (
            <AuthButton provider="oidc" label={`Sign in with ${providers.data.oidc.name || "SSO"}`} />
          )}
          {(providers.data?.github ?? true) && (
            <AuthButton className={providers.data?.oidc?.enabled ? "btn" : "btn btn-primary"} />
          )}
        </div>
      )}
      <p className="muted small">
        DeployIQ asks only for your profile and email address. It never reads your repositories.
      </p>
    </section>
  );
}

// LoginCallback receives the session token in the URL fragment (never sent to
// any server, so it stays out of access logs), stores it and moves on.
export function LoginCallback() {
  const { setToken } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    const params = new URLSearchParams(location.hash.replace(/^#/, ""));
    const token = params.get("token");
    // Drop the token from the address bar and history either way.
    window.history.replaceState(null, "", "/login/callback");
    if (token) {
      setToken(token);
      navigate("/dashboard", { replace: true });
    } else {
      navigate("/login?error=missing_token", { replace: true });
    }
  }, [location.hash, navigate, setToken]);

  return (
    <div className="state" role="status">
      Signing you in…
    </div>
  );
}
