package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend/handler"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/config"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
	apperrors "github.com/dhyaneshsiddhartha15/DeployIQ/pkg/errors"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/middleware"
)

// statusRecorder captures the response status for logging and alerting.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// withRecovery turns a panic in any HTTP handler into a 500, mirroring the
// gRPC recovery interceptor for the routes that do not go through gRPC.
func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				slog.ErrorContext(r.Context(), "recovered from panic in http handler",
					slog.String("path", r.URL.Path),
					slog.String("panic", middleware.Redact(stringify(p))),
					slog.String("stack", string(debug.Stack())))
				handler.WriteError(w, r, apperrors.Internal("panic", nil))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func stringify(p any) string {
	b, _ := json.Marshal(p)
	return string(b)
}

// withAccessLog emits one structured record per request (Phase 8.5) and feeds
// the alert monitor. The query string is not logged: the OAuth callback carries
// a code in it.
func withAccessLog(mon *alertMonitor, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		mon.observe(rec.status)

		attrs := []any{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
		}
		switch {
		case rec.status >= 500:
			slog.ErrorContext(r.Context(), "http request", attrs...)
		case r.Method == http.MethodGet && rec.status < 400:
			slog.DebugContext(r.Context(), "http request", attrs...)
		default:
			slog.InfoContext(r.Context(), "http request", attrs...)
		}
	})
}

// withSecurityHeaders sets conservative defaults on every response, and caps
// request bodies so one client cannot make the process buffer megabytes.
func withSecurityHeaders(maxBody int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if strings.HasPrefix(r.URL.Path, constants.APIPathPrefix) {
			h.Set("Cache-Control", "no-store")
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		next.ServeHTTP(w, r)
	})
}

// withCORS allows the configured dashboard origins to call the API with a
// bearer token. No cookies are involved, so credentials are not allowed.
func withCORS(origins []string, next http.Handler) http.Handler {
	if len(origins) == 0 {
		return next
	}
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[strings.TrimRight(strings.TrimSpace(o), "/")] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// --- rate limiting (Phase 9.2: per-IP, shipped at launch) ---------------------

// rateLimiter is a per-client token bucket. Hand-rolled rather than
// golang.org/x/time/rate plus an eviction map: the whole thing is forty lines
// and needs no dependency.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   float64
	trust   bool
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(cfg config.RateLimitConfig, trustProxy bool) *rateLimiter {
	return &rateLimiter{
		buckets: map[string]*bucket{},
		rate:    float64(cfg.RequestsPerMinute) / 60,
		burst:   float64(cfg.Burst),
		trust:   trustProxy,
		now:     time.Now,
	}
}

// allow takes one token for key, reporting whether it was available and, if
// not, how long until one is.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// sweep drops buckets idle long enough to have refilled, bounding memory to the
// set of recently active clients.
func (l *rateLimiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	full := time.Duration(l.burst/l.rate*float64(time.Second)) + time.Minute
	cutoff := l.now().Add(-full)
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}

func (l *rateLimiter) run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.sweep()
		}
	}
}

func (l *rateLimiter) clientIP(r *http.Request) string {
	if l.trust {
		if ip := strings.TrimSpace(r.Header.Get("Fly-Client-IP")); ip != "" {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, constants.APIPathPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		ok, wait := l.allow(l.clientIP(r))
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
			handler.WriteError(w, r, apperrors.RateLimited())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- alerting (Phase 12.1) ----------------------------------------------------

// alertMonitor counts responses per window and posts to a webhook when the 5xx
// rate stays above the threshold for Sustain consecutive windows — "sustained",
// so one bad minute during a deploy does not page anyone.
type alertMonitor struct {
	cfg  config.AlertConfig
	post func(ctx context.Context, url string, body []byte) error

	mu        sync.Mutex
	total     int
	errors    int
	badStreak int
	lastAlert time.Time
	now       func() time.Time
}

func newAlertMonitor(cfg config.AlertConfig) *alertMonitor {
	return &alertMonitor{cfg: cfg, post: postWebhook, now: time.Now}
}

func (m *alertMonitor) observe(status int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.total++
	if status >= 500 {
		m.errors++
	}
	m.mu.Unlock()
}

// tick closes the current window. It returns the alert text when one fires.
func (m *alertMonitor) tick() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	total, errs := m.total, m.errors
	m.total, m.errors = 0, 0

	if total < m.cfg.MinRequests {
		m.badStreak = 0
		return ""
	}
	rate := float64(errs) / float64(total)
	if rate < m.cfg.Threshold {
		m.badStreak = 0
		return ""
	}
	m.badStreak++
	if m.badStreak < m.cfg.Sustain {
		return ""
	}
	if !m.lastAlert.IsZero() && m.now().Sub(m.lastAlert) < m.cfg.Cooldown {
		return ""
	}
	m.lastAlert = m.now()
	return "DeployIQ API: sustained 5xx rate of " + strconv.FormatFloat(rate*100, 'f', 1, 64) +
		"% (" + strconv.Itoa(errs) + "/" + strconv.Itoa(total) + " requests) for " +
		strconv.Itoa(m.badStreak) + " consecutive windows of " + m.cfg.Window.String() +
		". Runbook: check platform status and logs; roll back a bad deploy (docs/incidents/README.md)."
}

func (m *alertMonitor) run(ctx context.Context) {
	t := time.NewTicker(m.cfg.Window)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			msg := m.tick()
			if msg == "" {
				continue
			}
			slog.Error("alert fired", slog.String("alert", msg))
			if m.cfg.WebhookURL == "" {
				continue
			}
			body, _ := json.Marshal(map[string]string{"text": msg})
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := m.post(pctx, m.cfg.WebhookURL, body); err != nil {
				slog.Error("alert webhook failed", slog.String("error", err.Error()))
			}
			cancel()
		}
	}
}

func postWebhook(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return &webhookError{status: resp.StatusCode}
	}
	return nil
}

type webhookError struct{ status int }

func (e *webhookError) Error() string { return "webhook returned status " + strconv.Itoa(e.status) }
