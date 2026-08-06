// Package logger configures the process-wide structured logger.
//
// It is a thin wrapper over the standard library's log/slog: JSON output for
// the backend (Phase 8.5 requires structured JSON logs shipped to the hosting
// platform's log viewer) and plain text for the CLI, whose stderr a human reads
// directly. No logging dependency is pulled in — slog covers both cases, and
// the CLI is a single static binary where every dependency is dead weight.
//
// The level is held in a slog.LevelVar so it can be changed at runtime without
// a restart; internal/backend exposes that over a loopback-only endpoint.
package logger

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
)

// Format selects the encoding of log records.
type Format string

const (
	// FormatJSON emits one JSON object per record. Used by the API server.
	FormatJSON Format = "json"

	// FormatText emits key=value pairs. Used by the CLI.
	FormatText Format = "text"
)

// level backs the active handler so SetLevel takes effect immediately for all
// handlers created by Init.
var level = new(slog.LevelVar)

// Options configures Init. The zero value is valid: info level, text format,
// stderr output.
type Options struct {
	// Level is one of debug, info, warn, error. Empty means info.
	Level string

	// Format is json or text. Empty means text.
	Format Format

	// Output defaults to os.Stderr. Logs go to stderr, never stdout, so the
	// CLI's stdout stays a clean, pipeable channel for its report output.
	Output io.Writer

	// AddSource includes file:line on every record. Useful in development,
	// noisy in production.
	AddSource bool
}

// Init installs a slog handler as the default logger and returns an error only
// for an unparseable level. Safe to call once at process start.
func Init(opts Options) error {
	lvl, err := ParseLevel(opts.Level)
	if err != nil {
		return err
	}
	level.Set(lvl)

	out := opts.Output
	if out == nil {
		out = os.Stderr
	}

	handlerOpts := &slog.HandlerOptions{Level: level, AddSource: opts.AddSource}

	var handler slog.Handler
	switch opts.Format {
	case FormatJSON:
		handler = slog.NewJSONHandler(out, handlerOpts)
	case FormatText, "":
		handler = slog.NewTextHandler(out, handlerOpts)
	default:
		return fmt.Errorf("logger: unknown format %q (want json or text)", opts.Format)
	}

	slog.SetDefault(slog.New(handler))

	// Redirect the standard library's default logger into the same handler.
	// A dependency that calls log.Printf would otherwise put a plain-text
	// line in an otherwise-JSON stream, which the hosting platform's log
	// viewer cannot parse (Phase 8.5). Treated as error level: nothing in
	// our own code uses the stdlib logger, so anything arriving here is a
	// library complaining.
	//
	// gRPC does not use this — it has its own logger, redirected in
	// internal/backend so the CLI never links gRPC just to configure it.
	log.SetFlags(0)
	log.SetOutput(slog.NewLogLogger(handler, slog.LevelError).Writer())

	return nil
}

// ParseLevel maps a level name to a slog.Level. An empty name means info.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logger: unknown level %q (want debug, info, warn or error)", name)
	}
}

// SetLevel changes the active log level for every handler created by Init.
// Concurrency-safe — slog.LevelVar is designed for exactly this.
func SetLevel(name string) error {
	lvl, err := ParseLevel(name)
	if err != nil {
		return err
	}
	level.Set(lvl)
	return nil
}

// Level reports the active log level as a lowercase name.
func Level() string {
	return strings.ToLower(level.Level().String())
}
