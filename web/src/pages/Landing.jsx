import { Link } from "react-router-dom";
import { useAuth } from "../auth/AuthContext.jsx";
import AuthButton from "../components/AuthButton.jsx";

const steps = [
  ["Detect", "Reads package.json, go.mod, requirements.txt or pyproject.toml. Nothing in your repo is executed."],
  ["Generate", "Writes a multi-stage Dockerfile from proven, deterministic rules. No AI guessing at your build."],
  ["Compare", "Shows an estimated before/after size before you change anything."],
  ["Confirm", "Writes Dockerfile.optimized only when you say so. Your Dockerfile is never touched."],
];

export default function Landing() {
  const { token } = useAuth();
  return (
    <>
      <section className="hero">
        <p className="eyebrow">Free, open-source CLI</p>
        <h1>Point it at a repo. Get back a smaller, safer Dockerfile.</h1>
        <p className="lead">
          No code changes. No Docker expertise. Typical Node.js, Go and Python images shrink from gigabytes to a few
          hundred megabytes — or a few megabytes for Go.
        </p>
        <pre className="terminal" aria-label="Example commands">
          <code>
            <span className="prompt">$</span> go install github.com/dhyaneshsiddhartha15/DeployIQ/cmd/doiq@latest{"\n"}
            <span className="prompt">$</span> doiq .{"\n"}
            <span className="muted">Estimated reduction: ~959 MB (84%)</span>
            {"\n"}
            <span className="prompt">$</span> doiq -write .
          </code>
        </pre>
        <div className="hero-cta">
          {token ? (
            <Link className="btn btn-primary" to="/dashboard">
              Open your dashboard
            </Link>
          ) : (
            <AuthButton />
          )}
          <a className="btn" href="https://github.com/dhyaneshsiddhartha15/DeployIQ">
            View on GitHub
          </a>
        </div>
      </section>

      <section aria-labelledby="how" className="section">
        <h2 id="how">How it works</h2>
        <ol className="steps">
          {steps.map(([title, body], i) => (
            <li key={title}>
              <span className="step-n" aria-hidden="true">
                {i + 1}
              </span>
              <h3>{title}</h3>
              <p>{body}</p>
            </li>
          ))}
        </ol>
      </section>

      <section aria-labelledby="dash" className="section">
        <h2 id="dash">The dashboard is optional</h2>
        <p>
          The CLI works fully offline, forever. Sign in only if you want history: run <code>doiq login</code>, then add{" "}
          <code>-report</code> to a run. Only the repository name, stack, the two size estimates and the rule names are
          sent — never your code or Dockerfile.
        </p>
      </section>
    </>
  );
}
