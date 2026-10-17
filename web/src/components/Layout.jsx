import { Link, NavLink, Outlet } from "react-router-dom";
import { useAuth } from "../auth/AuthContext.jsx";
import AuthButton from "./AuthButton.jsx";

export default function Layout() {
  const { token, user } = useAuth();
  return (
    <>
      <a className="skip" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <div className="container topbar-inner">
          <Link to="/" className="brand">
            <span className="brand-mark" aria-hidden="true">
              ▣
            </span>
            DeployIQ <span className="brand-sub">Optimizer</span>
          </Link>
          <nav aria-label="Main">
            {token && <NavLink to="/dashboard">Dashboard</NavLink>}
            {user?.login && <span className="who">@{user.login}</span>}
            <AuthButton className="btn btn-primary btn-sm" />
          </nav>
        </div>
      </header>
      <main id="main" className="container">
        <Outlet />
      </main>
      <footer className="footer container">
        <p>
          DeployIQ Optimizer is free and open source. The CLI runs entirely on your machine; this dashboard only shows
          results you chose to report with <code>doiq -report</code>.
        </p>
      </footer>
    </>
  );
}
