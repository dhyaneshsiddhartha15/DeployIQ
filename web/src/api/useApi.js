import { useCallback, useEffect, useState } from "react";

// useApi runs an async loader and exposes the three explicit states every
// data-fetching component must render (Phase 6.4): loading, error, loaded.
// There is no fourth "nothing yet" state that could render as a blank screen.
export function useApi(loader, deps) {
  const [state, setState] = useState({ status: "loading", data: null, error: null });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const ctrl = new AbortController();
    setState({ status: "loading", data: null, error: null });
    loader(ctrl.signal)
      .then((data) => {
        if (!ctrl.signal.aborted) setState({ status: "loaded", data, error: null });
      })
      .catch((error) => {
        if (!ctrl.signal.aborted && error?.name !== "AbortError") {
          setState({ status: "error", data: null, error });
        }
      });
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, attempt]);

  const retry = useCallback(() => setAttempt((n) => n + 1), []);
  return { ...state, retry };
}
