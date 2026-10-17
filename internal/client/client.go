// Package client is the CLI's HTTP client for the optional DeployIQ API
// (Phase 4.6: CLI run --(user opts in)--> POST /api/v1/optimizations).
//
// Nothing in the default `doiq .` path reaches this package. It is used only by
// `doiq login`, `doiq whoami` and the -report flag, each an explicit opt-in, so
// FR-8 — no required network calls — holds for every run that does not ask.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/model"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/version"
)

// ErrUnauthenticated means the stored session is missing, expired or revoked.
var ErrUnauthenticated = errors.New("not logged in or session expired; run `doiq login`")

// Client talks to the API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New returns a client with sane timeouts.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// APIError is a non-2xx response.
type APIError struct {
	Status  int
	Code    string
	Message string
	Field   string
}

func (e *APIError) Error() string {
	msg := e.Message
	if e.Field != "" && !strings.HasPrefix(msg, e.Field) {
		msg = e.Field + ": " + msg
	}
	return fmt.Sprintf("api: %d %s: %s", e.Status, e.Code, msg)
}

// ReportOptimization sends one opt-in result.
func (c *Client) ReportOptimization(ctx context.Context, r model.OptimizationReport) (*model.Optimization, error) {
	var out model.Optimization
	if err := c.do(ctx, http.MethodPost, constants.APIPathPrefix+"/optimizations", r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Me returns the logged-in user.
func (c *Client) Me(ctx context.Context) (*model.User, error) {
	var out model.User
	if err := c.do(ctx, http.MethodGet, constants.APIPathPrefix+"/me", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", constants.AppName+"/"+version.Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("api: reading response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthenticated
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Field   string `json:"field"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &env)
		return &APIError{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message, Field: env.Error.Field}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("api: decoding response: %w", err)
		}
	}
	return nil
}

// LoginURL is the address the browser opens to start the OAuth flow for a CLI
// waiting on loopback port with the given state.
func LoginURL(baseURL string, port int, state string) string {
	q := url.Values{}
	q.Set("client", "cli")
	q.Set("port", strconv.Itoa(port))
	q.Set("state", state)
	return strings.TrimRight(baseURL, "/") + constants.APIPathPrefix + "/auth/github/login?" + q.Encode()
}

// Login runs the browser half of Phase 5.3:
//
//	doiq login -> browser -> GitHub consent -> /api/v1/auth/github/callback
//	           -> API issues a session token -> redirect to 127.0.0.1:<port>
//
// It listens on a random loopback port (never a public interface), opens the
// consent page, and waits for the API to redirect back with a token and the
// state it was given. A mismatched state is rejected: it means the redirect did
// not come from the flow this process started.
func Login(ctx context.Context, baseURL string, open func(string) error, notify func(string)) (string, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("login: opening loopback listener: %w", err)
	}
	defer lis.Close()
	port := lis.Addr().(*net.TCPAddr).Port

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	state := hex.EncodeToString(raw)

	type result struct {
		token string
		err   error
	}
	done := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch {
		case q.Get("state") != state:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, page("Login failed", "The response did not match this login attempt. Run doiq login again."))
			select {
			case done <- result{err: errors.New("login: state mismatch; refusing the token")}:
			default:
			}
		case q.Get("error") != "":
			fmt.Fprint(w, page("Login failed", "GitHub did not authorise the login: "+html.EscapeString(q.Get("error"))))
			select {
			case done <- result{err: fmt.Errorf("login: %s", q.Get("error"))}:
			default:
			}
		case q.Get("token") == "":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, page("Login failed", "No session token was returned."))
			select {
			case done <- result{err: errors.New("login: no token returned")}:
			default:
			}
		default:
			fmt.Fprint(w, page("Logged in", "You can close this tab and return to the terminal."))
			select {
			case done <- result{token: q.Get("token")}:
			default:
			}
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(lis) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	u := LoginURL(baseURL, port, state)
	notify(u)
	if open != nil {
		if err := open(u); err != nil {
			notify("")
		}
	}

	select {
	case r := <-done:
		return r.token, r.err
	case <-ctx.Done():
		return "", fmt.Errorf("login: %w", ctx.Err())
	}
}

func page(title, body string) string {
	return "<!doctype html><meta charset=utf-8><title>DeployIQ - " + title + "</title>" +
		"<body style=\"font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem\">" +
		"<h1>" + title + "</h1><p>" + body + "</p></body>"
}
