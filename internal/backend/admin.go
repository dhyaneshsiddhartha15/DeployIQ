package backend

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/logger"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/version"
)

// adminMux builds the operational endpoints.
//
// Bound to 127.0.0.1 by config default and never routed through the public
// listener: changing the log level of a running process is an operator action,
// not an API. The reference project makes the same split for the
// same reason.
//
//	GET  /internal/healthz    liveness — process is up and serving
//	GET  /internal/version    build metadata
//	GET  /internal/log-level  current level
//	PUT  /internal/log-level  {"level":"debug"} — takes effect immediately
//
// healthz deliberately does not check MongoDB. Phase 11/12 use it to decide
// whether to restart or roll back this process; failing it during an Atlas
// outage would restart a healthy binary and turn a dependency blip into an
// outage of our own (Phase 12.2 step 3: wait it out, no failover).
func adminMux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET "+constants.AdminPathPrefix+"/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET "+constants.AdminPathPrefix+"/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"service": constants.ServiceName,
			"version": version.Version,
			"commit":  version.Commit,
			"built":   version.BuildDate,
		})
	})

	mux.HandleFunc("GET "+constants.AdminPathPrefix+"/log-level", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"level": logger.Level()})
	})

	mux.HandleFunc("PUT "+constants.AdminPathPrefix+"/log-level", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Level string `json:"level"`
		}
		// 1 KiB is far more than {"level":"debug"} needs and caps what an
		// operator typo can make the process allocate.
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be {\"level\":\"debug|info|warn|error\"}"})
			return
		}
		if err := logger.SetLevel(body.Level); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		slog.Info("log level changed", slog.String("level", logger.Level()))
		writeJSON(w, http.StatusOK, map[string]string{"level": logger.Level()})
	})

	return mux
}

// writeJSON writes v as a JSON response. Encoding a map of strings cannot fail
// mid-write in practice; if it somehow does the status line is already sent, so
// the only useful action left is a log line.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writing admin response", slog.String("error", err.Error()))
	}
}
