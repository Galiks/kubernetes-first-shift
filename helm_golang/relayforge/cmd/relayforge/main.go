// Command relayforge is the single application image in five modes (api,
// worker, test-sink, helm-test, cleanup), mirroring __main__.py.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"relayforge/internal/api"
	"relayforge/internal/cleanup"
	"relayforge/internal/config"
	"relayforge/internal/helmtest"
	"relayforge/internal/logging"
	"relayforge/internal/testsink"
	"relayforge/internal/worker"
)

var modes = map[string]bool{
	"api":       true,
	"worker":    true,
	"test-sink": true,
	"helm-test": true,
	"cleanup":   true,
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: relayforge <api|worker|test-sink|helm-test|cleanup>\n")
	os.Exit(2)
}

func main() {
	logging.Setup()
	// Mode validation comes first, like __main__.py: an unknown mode must print
	// usage and exit 2 regardless of environment configuration.
	if len(os.Args) < 2 || !modes[os.Args[1]] {
		usage()
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err.Error())
		os.Exit(1)
	}
	var runErr error
	switch os.Args[1] {
	case "api":
		runErr = api.Run(cfg)
	case "worker":
		runErr = worker.Run(cfg)
	case "test-sink":
		runErr = testsink.Run(cfg)
	case "helm-test":
		runErr = helmtest.Run(cfg)
	case "cleanup":
		runErr = cleanup.Run(cfg)
	}
	if runErr != nil {
		slog.Error("run", "err", runErr.Error())
		os.Exit(1)
	}
}