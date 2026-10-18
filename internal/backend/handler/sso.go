package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/service"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
)

// pkceCookie holds the PKCE verifier between the login redirect and the
// callback. HttpOnly: the browser keeps it, scripts and the provider never see
// it, which is the point of PKCE.
const pkceCookie = "doiq_oidc_pkce"

// SSOName is the label for the SSO button ("Okta", "Microsoft").
type SSOName string

// RegisterSSO mounts the OIDC routes and the providers endpoint.
//
//	GET /api/v1/auth/providers        which sign-in methods this server offers
//	GET /api/v1/auth/oidc/login       ?client=web | client=cli&port=&state=
//	GET /api/v1/auth/oidc/callback    the provider's redirect target
func (h *Auth) RegisterSSO(mux *http.ServeMux, name SSOName) {
	mux.HandleFunc("GET "+constants.APIPathPrefix+"/auth/providers", func(w http.ResponseWriter, _ *http.Request) {
		label := string(name)
		if label == "" {
			label = "SSO"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"github": h.svc.GitHubEnabled(),
			"oidc":   map[string]any{"enabled": h.svc.OIDCEnabled(), "name": label},
		})
	})
	if !h.svc.OIDCEnabled() {
		return
	}
	mux.HandleFunc("GET "+constants.APIPathPrefix+"/auth/oidc/login", h.oidcLogin)
	mux.HandleFunc("GET "+constants.APIPathPrefix+"/auth/oidc/callback", h.oidcCallback)
}

func (h *Auth) oidcLogin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client := q.Get("client")
	if client == "" {
		client = "web"
	}
	port, _ := strconv.Atoi(q.Get("port"))
	state, nonce, err := h.svc.NewLoginStateFor(service.ProviderOIDC, client, port, q.Get("state"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		WriteError(w, r, apperrors.Internal("pkce", err))
		return
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	target := h.svc.OIDCAuthorizeURL(state, nonce, h.oidcRedirectURI(r), challenge)
	if target == "" {
		WriteError(w, r, apperrors.Unavailable("the identity provider"))
		return
	}
	secure := isHTTPS(r, h.publicURL)
	path := constants.APIPathPrefix + "/auth/oidc"
	http.SetCookie(w, &http.Cookie{Name: nonceCookie, Value: nonce, Path: path, MaxAge: 600, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: pkceCookie, Value: verifier, Path: path, MaxAge: 600, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, target, http.StatusFound)
}

func (h *Auth) oidcCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	nonce, verifier := "", ""
	if c, err := r.Cookie(nonceCookie); err == nil {
		nonce = c.Value
	}
	if c, err := r.Cookie(pkceCookie); err == nil {
		verifier = c.Value
	}
	ls, err := h.svc.VerifyLoginStateFor(service.ProviderOIDC, q.Get("state"), nonce)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	path := constants.APIPathPrefix + "/auth/oidc"
	for _, name := range []string{nonceCookie, pkceCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: true})
	}

	if e := q.Get("error"); e != "" {
		code := "access_denied"
		if e != "access_denied" {
			code = "sso_error"
		}
		h.finish(w, r, ls, "", code)
		return
	}
	if verifier == "" {
		WriteError(w, r, apperrors.Invalid("login was started in a different browser session; start it again"))
		return
	}
	_, token, err := h.svc.CompleteOIDCLogin(r.Context(), q.Get("code"), h.oidcRedirectURI(r), verifier, nonce)
	if err != nil {
		if apperrors.KindOf(err) == apperrors.KindInternal {
			slog.ErrorContext(r.Context(), "sso callback failed", slog.String("error", err.Error()))
		}
		if ls.Client == "cli" {
			h.finish(w, r, ls, "", "login_failed")
			return
		}
		code := "login_failed"
		if apperrors.KindOf(err) == apperrors.KindPermissionDenied {
			code = "domain_not_allowed"
		}
		h.finish(w, r, ls, "", code)
		return
	}
	h.finish(w, r, ls, token, "")
}

func (h *Auth) oidcRedirectURI(r *http.Request) string {
	return origin(r, h.publicURL) + constants.APIPathPrefix + "/auth/oidc/callback"
}
