// The three explicit states (Phase 6.4). Every data view renders exactly one of
// these or its loaded content — never a silent blank screen.

export function Loading({ label = "Loading" }) {
  return (
    <div className="state" role="status" aria-live="polite">
      <span className="spinner" aria-hidden="true" />
      <span>{label}…</span>
    </div>
  );
}

export function ErrorState({ error, onRetry }) {
  return (
    <div className="state state-error" role="alert">
      <p className="state-title">Something went wrong</p>
      <p>{error?.message || "The request failed."}</p>
      {onRetry && (
        <button type="button" className="btn" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}

export function EmptyState({ title, children }) {
  return (
    <div className="state">
      <p className="state-title">{title}</p>
      {children}
    </div>
  );
}
