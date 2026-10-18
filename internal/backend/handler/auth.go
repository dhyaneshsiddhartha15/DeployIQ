package handler

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/service"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
)

// nonceCookie binds the OAuth state to the browser that started the login.
const nonceCookie = "doiq_oauth_nonce"

// Auth serves the two OAuth redirect routes. They are plain HTTP handlers, not
// RPCs: their whole job is to issue 302s, which the gRPC gateway cannot.
//
//	GET /api/v1/auth/github/login?client=cli&port=P&state=S   (or client=web)
//	GET /api/v1/auth/github/callback?code=...&state=...       (Phase 4.5)
type Auth struct {
	svc *service.Auth

	// publicURL is the API's external origin; "" derives it per request.
	publicURL string

	// callback is the configured callback path or absolute URL.
	callback string

	// dashboard is where browser logins land; "" means same origin.
	dashboard string
}

// NewAuth returns the handler.
func NewAuth(svc *service.Auth, publicURL, callback, dashboard string) *Auth {
	return &Auth{
		svc: svc, publicURL: strings.TrimRight(publicURL, "/"),
		callback: callback, dashboard: strings.TrimRight(dashboard, "/"),
	}
}

// Register mounts the routes on mux.
func (h *Auth) Register(mux *http.ServeMux) {
	if !h.svc.GitHubEnabled() {
		return // SSO-only deployment: the GitHub routes 404
	}
	mux.HandleFunc("GET "+constants.APIPathPrefix+"/auth/github/login", h.login)
	mux.HandleFunc("GET "+constants.APIPathPrefix+"/auth/github/callback", h.callbackHandler)
}

func (h *Auth) login(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client := q.Get("client")
	if client == "" {
		client = "web"
	}
	port, _ := strconv.Atoi(q.Get("port"))
	state, nonce, err := h.svc.NewLoginState(client, port, q.Get("state"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     nonceCookie,
		Value:    nonce,
		Path:     constants.APIPathPrefix + "/auth/github",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   isHTTPS(r, h.publicURL),
		// Lax, not Strict: the callback is a top-level navigation from
		// github.com, which Strict would strip the cookie from.
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, h.svc.AuthorizeURL(state, h.redirectURI(r)), http.StatusFound)
}

func (h *Auth) callbackHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	nonce := ""
	if c, err := r.Cookie(nonceCookie); err == nil {
		nonce = c.Value
	}
	ls, err := h.svc.VerifyLoginState(q.Get("state"), nonce)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	// One-shot: clear the nonce whatever happens next.
	http.SetCookie(w, &http.Cookie{Name: nonceCookie, Value: "", Path: constants.APIPathPrefix + "/auth/github", MaxAge: -1, HttpOnly: true})

	if ghErr := q.Get("error"); ghErr != "" {
		// The user declined consent. Only GitHub's fixed error codes are
		// passed on, never free text.
		code := "access_denied"
		if ghErr != "access_denied" {
			code = "github_error"
		}
		h.finish(w, r, ls, "", code)
		return
	}

	_, token, err := h.svc.CompleteLogin(r.Context(), q.Get("code"), h.redirectURI(r))
	if err != nil {
		if apperrors.KindOf(err) == apperrors.KindInternal {
			slog.ErrorContext(r.Context(), "oauth callback failed", slog.String("error", err.Error()))
		}
		if ls.Client == "cli" {
			h.finish(w, r, ls, "", "login_failed")
			return
		}
		WriteError(w, r, err)
		return
	}
	h.finish(w, r, ls, token, "")
}

// finish redirects to the client that started the login. The CLI target is
// always 127.0.0.1 on the port signed into the state; the web target is the
// configured dashboard. Neither is taken from the request, so the callback
// cannot be turned into an open redirect.
func (h *Auth) finish(w http.ResponseWriter, r *http.Request, ls service.LoginState, token, errCode string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if ls.Client == "cli" {
		v := url.Values{}
		v.Set("state", ls.CLIState)
		if errCode != "" {
			v.Set("error", errCode)
		} else {
			v.Set("token", token)
		}
		http.Redirect(w, r, "http://127.0.0.1:"+strconv.Itoa(ls.Port)+"/callback?"+v.Encode(), http.StatusFound)
		return
	}
	if errCode != "" {
		http.Redirect(w, r, h.dashboard+"/login?error="+url.QueryEscape(errCode), http.StatusFound)
		return
	}
	// The token travels in the fragment, which browsers never send to a
	// server, so it does not land in any access log on the way.
	http.Redirect(w, r, h.dashboard+"/login/callback#token="+url.QueryEscape(token), http.StatusFound)
}

// redirectURI is the absolute callback URL GitHub must redirect to. It must
// match the OAuth app registration exactly.
func (h *Auth) redirectURI(r *http.Request) string {
	if strings.HasPrefix(h.callback, "http://") || strings.HasPrefix(h.callback, "https://") {
		return h.callback
	}
	return origin(r, h.publicURL) + h.callback
}

func origin(r *http.Request, publicURL string) string {
	if publicURL != "" {
		return publicURL
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func isHTTPS(r *http.Request, publicURL string) bool {
	return strings.HasPrefix(origin(r, publicURL), "https://")
}
