package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setSecrets populates the three env-only secrets so Validate can pass.
func setSecrets(t *testing.T) {
	t.Helper()
	t.Setenv(EnvPrefix+"MONGO_URI", "mongodb://localhost:27017")
	t.Setenv(EnvPrefix+"GITHUB_CLIENT_SECRET", "test-secret")
	t.Setenv(EnvPrefix+"AUTH_SESSION_SIGNING_KEY", "test-signing-key")
	t.Setenv(EnvPrefix+"GITHUB_CLIENT_ID", "test-client-id")
}

func TestLoadDefaultsWithSecretsFromEnv(t *testing.T) {
	setSecrets(t)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.Server.HTTPAddr)
	}
	if cfg.Mongo.URI != "mongodb://localhost:27017" {
		t.Errorf("Mongo.URI not taken from env: %q", cfg.Mongo.URI)
	}
	// Phase 9.2: rate limiting ships with Phase 4, so it must default on.
	if !cfg.RateLimit.Enabled {
		t.Error("RateLimit.Enabled = false, want true by default")
	}
}

func TestLoadMissingSecretsReportsAllProblems(t *testing.T) {
	// No secrets set: every required env-only value must be named at once.
	_, err := Load("")
	if err == nil {
		t.Fatal("Load succeeded with no secrets set, want error")
	}
	for _, want := range []string{"MONGO_URI", "GITHUB_CLIENT_SECRET", "AUTH_SESSION_SIGNING_KEY", "github.clientId"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s:\n%v", want, err)
		}
	}
}

func TestEnvOverridesFileWhichOverridesDefaults(t *testing.T) {
	setSecrets(t)

	path := filepath.Join(t.TempDir(), "config.yml")
	body := "server:\n  httpAddr: \":9999\"\nlog:\n  level: warn\n  format: text\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// File beats default.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.HTTPAddr != ":9999" {
		t.Errorf("HTTPAddr = %q, want :9999 from file", cfg.Server.HTTPAddr)
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("Log.Level = %q, want warn from file", cfg.Log.Level)
	}

	// Env beats file.
	t.Setenv(EnvPrefix+"SERVER_HTTP_ADDR", ":7777")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.HTTPAddr != ":7777" {
		t.Errorf("HTTPAddr = %q, want :7777 from env", cfg.Server.HTTPAddr)
	}
}

func TestUnknownYAMLKeyIsRejected(t *testing.T) {
	setSecrets(t)

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("server:\n  htpAddr: \":9999\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a misspelled key, want error")
	}
}

func TestSecretsHaveNoYAMLKey(t *testing.T) {
	setSecrets(t)

	// Phase 8.4: a secret placed in the config file must not be picked up.
	// KnownFields(true) turns the attempt into a startup error, which is the
	// loud failure we want rather than a silently honoured committed secret.
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("mongo:\n  uri: mongodb://committed-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted mongo.uri from the config file, want it rejected")
	}
}

func TestBadEnvValuesAreNamedPrecisely(t *testing.T) {
	setSecrets(t)
	t.Setenv(EnvPrefix+"AUTH_SESSION_TTL", "not-a-duration")

	_, err := Load("")
	if err == nil {
		t.Fatal("Load accepted an unparseable duration, want error")
	}
	if !strings.Contains(err.Error(), "AUTH_SESSION_TTL") {
		t.Errorf("error does not name the offending variable:\n%v", err)
	}
}

func TestBlankEnvVarDoesNotClearDefault(t *testing.T) {
	setSecrets(t)
	t.Setenv(EnvPrefix+"SERVER_HTTP_ADDR", "   ")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want the default retained", cfg.Server.HTTPAddr)
	}
}

func TestValidateRejectsNonPositiveDurations(t *testing.T) {
	cfg := Default()
	cfg.Mongo.URI = "mongodb://localhost"
	cfg.GitHub.ClientID = "id"
	cfg.GitHub.ClientSecret = "secret"
	cfg.Auth.SessionSigningKey = "key"
	cfg.Auth.SessionTTL = 0
	cfg.Server.ShutdownTimeout = -time.Second

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted non-positive durations, want error")
	}
	for _, want := range []string{"auth.sessionTtl", "server.shutdownTimeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s:\n%v", want, err)
		}
	}
}
