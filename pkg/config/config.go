// Package config loads and validates the backend API server's configuration.
//
// Design follows Phase 5.6: a single place that reads configuration at startup
// and fails fast with a clear message when something required is missing.
//
// Resolution order, last writer wins:
//
//  1. Defaults compiled in below.
//  2. A YAML file (configs/default.yml, path from the -config flag).
//  3. Environment variables prefixed DEPLOYIQ_.
//
// Secrets — the MongoDB URI and the GitHub OAuth client secret — are read from
// the environment only and have no YAML key. Phase 8.4 requires they live in the
// hosting platform's secret manager and never in source control; giving them no
// file key makes committing them impossible rather than merely discouraged.
//
// The CLI (the v1 product) does not use this. Phase 0.7 requires zero
// configuration for the default happy path, so `doiq .` reads no config file at
// all. CLIConfig below covers only the user-level state `doiq login` needs.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/coredgeio/deployiq-optimizer/pkg/constants"
	"github.com/coredgeio/deployiq-optimizer/pkg/logger"
)

// EnvPrefix namespaces every environment variable this package reads.
const EnvPrefix = "DEPLOYIQ_"

// Config is the full backend configuration.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Mongo     MongoConfig     `yaml:"mongo"`
	GitHub    GitHubConfig    `yaml:"github"`
	Auth      AuthConfig      `yaml:"auth"`
	Log       LogConfig       `yaml:"log"`
	RateLimit RateLimitConfig `yaml:"rateLimit"`
}

// ServerConfig holds listener addresses.
//
// Three listeners, mirroring the split the reference storage-plugin service
// uses: an internal gRPC port, a public HTTP port fronted by the gRPC gateway,
// and a loopback-only admin port. The admin port is bound to 127.0.0.1 so
// runtime log-level control is never reachable from the network.
type ServerConfig struct {
	// GRPCAddr is the internal gRPC listener. Not exposed publicly — the
	// gateway dials it over loopback.
	GRPCAddr string `yaml:"grpcAddr"`

	// HTTPAddr is the public REST/JSON listener (Phase 5.2).
	HTTPAddr string `yaml:"httpAddr"`

	// AdminAddr is the loopback-only operational listener.
	AdminAddr string `yaml:"adminAddr"`

	// ShutdownTimeout bounds graceful shutdown before connections are
	// forcibly closed.
	ShutdownTimeout time.Duration `yaml:"shutdownTimeout"`

	// ReadHeaderTimeout guards against slow-header (Slowloris) clients.
	ReadHeaderTimeout time.Duration `yaml:"readHeaderTimeout"`
}

// MongoConfig holds the Phase 4+ document store settings.
type MongoConfig struct {
	// URI is the connection string. Secret: DEPLOYIQ_MONGO_URI only.
	URI string `yaml:"-"`

	// Database is the database name.
	Database string `yaml:"database"`

	// ConnectTimeout bounds the initial connection attempt so a wrong URI
	// surfaces at startup instead of on the first request.
	ConnectTimeout time.Duration `yaml:"connectTimeout"`
}

// GitHubConfig holds OAuth application credentials (Phase 3.1, Phase 5.3).
type GitHubConfig struct {
	// ClientID is the OAuth app's public identifier.
	ClientID string `yaml:"clientId"`

	// ClientSecret is secret: DEPLOYIQ_GITHUB_CLIENT_SECRET only.
	ClientSecret string `yaml:"-"`

	// CallbackURL must match the OAuth app registration exactly. It is the
	// route from Phase 4.5: /api/v1/auth/github/callback.
	CallbackURL string `yaml:"callbackUrl"`
}

// AuthConfig holds session token settings.
type AuthConfig struct {
	// SessionTTL is how long a token issued to the CLI stays valid. Phase
	// 5.3 calls for a short-lived token.
	SessionTTL time.Duration `yaml:"sessionTtl"`

	// SessionSigningKey signs session tokens. Secret:
	// DEPLOYIQ_AUTH_SESSION_SIGNING_KEY only.
	SessionSigningKey string `yaml:"-"`
}

// LogConfig configures the process logger.
type LogConfig struct {
	// Level is debug, info, warn or error. Changeable at runtime via the
	// admin listener.
	Level string `yaml:"level"`

	// Format is json or text. Phase 8.5 wants json in production.
	Format string `yaml:"format"`

	// AddSource includes file:line on every record.
	AddSource bool `yaml:"addSource"`
}

// RateLimitConfig configures the per-IP limiter. Phase 9.2 states this ships
// with the Phase 4 launch and is explicitly not deferred, so it defaults on.
type RateLimitConfig struct {
	Enabled           bool `yaml:"enabled"`
	RequestsPerMinute int  `yaml:"requestsPerMinute"`
	Burst             int  `yaml:"burst"`
}

// Default returns the compiled-in defaults. Every field a developer can run
// locally without setting is populated here; only secrets are left empty.
func Default() Config {
	return Config{
		Server: ServerConfig{
			GRPCAddr:          "127.0.0.1:8090",
			HTTPAddr:          ":8080",
			AdminAddr:         "127.0.0.1:8081",
			ShutdownTimeout:   15 * time.Second,
			ReadHeaderTimeout: 5 * time.Second,
		},
		Mongo: MongoConfig{
			Database:       "deployiq",
			ConnectTimeout: 10 * time.Second,
		},
		GitHub: GitHubConfig{
			CallbackURL: constants.APIPathPrefix + "/auth/github/callback",
		},
		Auth: AuthConfig{
			SessionTTL: 24 * time.Hour,
		},
		Log: LogConfig{
			Level:  "info",
			Format: string(logger.FormatJSON),
		},
		RateLimit: RateLimitConfig{
			Enabled:           true,
			RequestsPerMinute: 120,
			Burst:             30,
		},
	}
}

// Load builds a Config from defaults, then the YAML file at path (skipped when
// path is empty), then the environment. It returns a validated config or an
// error naming exactly what is wrong.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path != "" {
		if err := cfg.loadFile(path); err != nil {
			return nil, err
		}
	}
	if err := cfg.applyEnv(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// loadFile decodes YAML over the current values. Unknown keys are an error, so
// a typo in a config file fails at startup rather than being silently ignored.
func (c *Config) loadFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("config: %q is a directory, not a file", path)
	}

	f, err := os.Open(path) //nolint:gosec // operator-supplied path, by design
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		return fmt.Errorf("config: parsing %s: %w", path, err)
	}
	return nil
}

// applyEnv overlays DEPLOYIQ_-prefixed environment variables.
func (c *Config) applyEnv() error {
	envString(&c.Server.GRPCAddr, "SERVER_GRPC_ADDR")
	envString(&c.Server.HTTPAddr, "SERVER_HTTP_ADDR")
	envString(&c.Server.AdminAddr, "SERVER_ADMIN_ADDR")
	envString(&c.Mongo.Database, "MONGO_DATABASE")
	envString(&c.GitHub.ClientID, "GITHUB_CLIENT_ID")
	envString(&c.GitHub.CallbackURL, "GITHUB_CALLBACK_URL")
	envString(&c.Log.Level, "LOG_LEVEL")
	envString(&c.Log.Format, "LOG_FORMAT")

	// Secrets: environment only, no YAML equivalent (Phase 8.4).
	envString(&c.Mongo.URI, "MONGO_URI")
	envString(&c.GitHub.ClientSecret, "GITHUB_CLIENT_SECRET")
	envString(&c.Auth.SessionSigningKey, "AUTH_SESSION_SIGNING_KEY")

	if err := envDuration(&c.Server.ShutdownTimeout, "SERVER_SHUTDOWN_TIMEOUT"); err != nil {
		return err
	}
	if err := envDuration(&c.Mongo.ConnectTimeout, "MONGO_CONNECT_TIMEOUT"); err != nil {
		return err
	}
	if err := envDuration(&c.Auth.SessionTTL, "AUTH_SESSION_TTL"); err != nil {
		return err
	}
	if err := envBool(&c.Log.AddSource, "LOG_ADD_SOURCE"); err != nil {
		return err
	}
	if err := envBool(&c.RateLimit.Enabled, "RATE_LIMIT_ENABLED"); err != nil {
		return err
	}
	if err := envInt(&c.RateLimit.RequestsPerMinute, "RATE_LIMIT_REQUESTS_PER_MINUTE"); err != nil {
		return err
	}
	return envInt(&c.RateLimit.Burst, "RATE_LIMIT_BURST")
}

// Validate reports every problem it finds at once, so an operator fixing a
// misconfiguration does not have to restart repeatedly to discover the next
// missing value.
func (c *Config) Validate() error {
	var problems []string

	require := func(value, name string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, name+" is required")
		}
	}

	require(c.Server.GRPCAddr, "server.grpcAddr")
	require(c.Server.HTTPAddr, "server.httpAddr")
	require(c.Mongo.Database, "mongo.database")

	require(c.Mongo.URI, EnvPrefix+"MONGO_URI")
	require(c.GitHub.ClientID, "github.clientId")
	require(c.GitHub.ClientSecret, EnvPrefix+"GITHUB_CLIENT_SECRET")
	require(c.Auth.SessionSigningKey, EnvPrefix+"AUTH_SESSION_SIGNING_KEY")

	if _, err := logger.ParseLevel(c.Log.Level); err != nil {
		problems = append(problems, "log.level: "+err.Error())
	}
	switch logger.Format(c.Log.Format) {
	case logger.FormatJSON, logger.FormatText:
	default:
		problems = append(problems, fmt.Sprintf("log.format: %q is not json or text", c.Log.Format))
	}

	if c.Auth.SessionTTL <= 0 {
		problems = append(problems, "auth.sessionTtl must be positive")
	}
	if c.Server.ShutdownTimeout <= 0 {
		problems = append(problems, "server.shutdownTimeout must be positive")
	}
	if c.RateLimit.Enabled {
		if c.RateLimit.RequestsPerMinute <= 0 {
			problems = append(problems, "rateLimit.requestsPerMinute must be positive when rate limiting is enabled")
		}
		if c.RateLimit.Burst <= 0 {
			problems = append(problems, "rateLimit.burst must be positive when rate limiting is enabled")
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// LoggerOptions adapts the log section to the logger package, so main does not
// have to know how the two line up.
func (c *Config) LoggerOptions() logger.Options {
	return logger.Options{
		Level:     c.Log.Level,
		Format:    logger.Format(c.Log.Format),
		AddSource: c.Log.AddSource,
	}
}

// --- CLI-side configuration ----------------------------------------------

// CLIConfig is the user-level state the CLI keeps between runs. It exists only
// for opt-in features: v1's default path reads nothing (Phase 0.7).
//
// Phase 5.3 requires the session token be stored locally and never committed,
// which is why this lives under the OS user config directory and not in the
// repository being analysed.
type CLIConfig struct {
	// SessionToken is issued by the API after `doiq login`.
	SessionToken string `yaml:"sessionToken,omitempty"`

	// APIEndpoint overrides the default reporting endpoint.
	APIEndpoint string `yaml:"apiEndpoint,omitempty"`

	// TelemetryEnabled must be set explicitly. Phase 0.6 and 1.2 require
	// telemetry be off by default, so the zero value is the private one.
	TelemetryEnabled bool `yaml:"telemetryEnabled,omitempty"`
}

// CLIConfigPath returns the path to the user-level CLI config file, e.g.
// ~/.config/doiq/config.yml on Linux. Nothing is created or read here; the file
// is written only by an explicit `doiq login`.
func CLIConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating user config dir: %w", err)
	}
	return filepath.Join(dir, constants.AppName, "config.yml"), nil
}

// --- env helpers ----------------------------------------------------------

// lookup returns the value of the prefixed variable, and whether it was set to
// a non-empty value. An empty variable is treated as unset so an accidentally
// blank platform secret does not silently clear a good default.
func lookup(key string) (string, bool) {
	v, ok := os.LookupEnv(EnvPrefix + key)
	if !ok || strings.TrimSpace(v) == "" {
		return "", false
	}
	return v, true
}

func envString(dst *string, key string) {
	if v, ok := lookup(key); ok {
		*dst = v
	}
}

func envInt(dst *int, key string) error {
	v, ok := lookup(key)
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("config: %s%s: %q is not an integer", EnvPrefix, key, v)
	}
	*dst = n
	return nil
}

func envBool(dst *bool, key string) error {
	v, ok := lookup(key)
	if !ok {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fmt.Errorf("config: %s%s: %q is not a boolean", EnvPrefix, key, v)
	}
	*dst = b
	return nil
}

func envDuration(dst *time.Duration, key string) error {
	v, ok := lookup(key)
	if !ok {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("config: %s%s: %q is not a duration (e.g. 30s, 5m, 24h)", EnvPrefix, key, v)
	}
	*dst = d
	return nil
}
