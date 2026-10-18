import { createContext, startTransition, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { getMe, getToken, setToken as storeToken, setUnauthorizedHandler } from "../api/client.js";

// Plain context, no state library (Phase 6.2): one token, one user, a handful
// of screens.
const AuthContext = createContext(null);

export function AuthProvider({ children, navigate }) {
  const [token, setTokenState] = useState(() => getToken());
  const [user, setUser] = useState(null);

  const setToken = useCallback((t) => {
    storeToken(t);
    setTokenState(t);
    if (!t) setUser(null);
  }, []);

  // The router applies navigations as transitions, so the token is cleared in
  // one too: both commit together and RequireAuth never sees "signed out" on
  // the old page (which would redirect to /login and win over ours).
  const logout = useCallback(() => {
    storeToken(null);
    navigate?.("/");
    startTransition(() => {
      setTokenState(null);
      setUser(null);
    });
  }, [navigate]);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      if (navigate) navigate("/login?error=session_expired");
      else window.location.assign("/login?error=session_expired");
      startTransition(() => {
        setTokenState(null);
        setUser(null);
      });
    });
  }, [navigate]);

  useEffect(() => {
    if (!token) return undefined;
    const ctrl = new AbortController();
    getMe(ctrl.signal)
      .then(setUser)
      .catch(() => {
        /* a 401 is handled centrally; anything else just leaves user unset */
      });
    return () => ctrl.abort();
  }, [token]);

  const value = useMemo(() => ({ token, user, setToken, logout }), [token, user, setToken, logout]);
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}
