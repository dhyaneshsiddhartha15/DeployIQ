import { useEffect } from "react";
import { Navigate, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { useAuth } from "../auth/AuthContext.jsx";
import AuthButton from "../components/AuthButton.jsx";

const errors = {
  access_denied: "GitHub sign-in was cancelled.",
  github_error: "GitHub could not complete the sign-in. Please try again.",
  session_expired: "Your session has expired. Please sign in again.",
  missing_token: "The sign-in did not return a session. Please try again.",
};

export function Login() {
  const { token } = useAuth();
  const [params] = useSearchParams();
  if (token) return <Navigate to="/dashboard" replace />;
  const err = params.get("error");
  return (
    <section className="card narrow">
      <h1>Sign in</h1>
      <p>Your dashboard shows the optimization results you report from the CLI with <code>doiq -report</code>.</p>
      {err && (
        <p className="notice notice-error" role="alert">
          {errors[err] ?? "Sign-in failed. Please try again."}
        </p>
      )}
      <AuthButton />
      <p className="muted small">
        DeployIQ asks GitHub only for your public profile and email address. It never reads your repositories.
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
