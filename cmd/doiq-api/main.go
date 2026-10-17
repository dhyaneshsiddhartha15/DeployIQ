// Command doiq-api is the optional backend API server (Phase 4+ of the build
// plan). It accepts opt-in optimization reports from the CLI and serves the
// dashboard (Phase 2.2).
//
// Optional is load-bearing: the CLI must stay fully useful with zero backend,
// forever, even after the dashboard ships (Phase 2.1). Nothing under cmd/doiq
// imports anything this binary needs.
//
// Configuration comes from a YAML file plus DEPLOYIQ_-prefixed environment
// variables, with secrets available from the environment only (Phase 5.6,
// Phase 8.4). Startup fails fast and lists every problem at once.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/backend"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/config"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/logger"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/version"
)

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet — a config failure happens before
		// it is configured — so this goes to stderr directly.
		fmt.Fprintf(os.Stderr, "%s: %v\n", constants.ServiceName, err)
		os.Exit(constants.ExitFailure)
	}
}

func run() error {
	var (
		configPath  = flag.String("config", "/opt/config.yml", "path to the configuration file")
		showVersion = flag.Bool("version", false, "print version information and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", constants.ServiceName, version.String())
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	if err := logger.Init(cfg.LoggerOptions()); err != nil {
		return err
	}

	srv, err := backend.New(cfg)
	if err != nil {
		return err
	}

	if err := srv.Run(context.Background()); err != nil {
		slog.Error("api server exited with error", slog.String("error", err.Error()))
		return err
	}
	return nil
}
