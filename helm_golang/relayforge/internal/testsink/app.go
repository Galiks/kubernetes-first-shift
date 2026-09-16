package testsink

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"relayforge/internal/config"
	"relayforge/internal/logging"
	"relayforge/internal/secrets"
)

// Run is the frozen test-sink entrypoint: a blocking HTTP server that never
// returns unless the server fails to start or exits with an error.
func Run(cfg *config.Config) error {
	logging.Setup()

	vkey, err := secrets.Read(secrets.VerificationKeyPath)
	if err != nil {
		return fmt.Errorf("read verification key: %w", err)
	}
	ctl, err := secrets.Read(secrets.ControlTokenPath)
	if err != nil {
		return fmt.Errorf("read control token: %w", err)
	}

	State.VerificationKey = vkey
	State.ControlToken = ctl
	State.Modes = NewModeController()
	State.Receipts = NewReceiptStore()

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.TestSinkPort)
	srv := &http.Server{
		Addr:              addr,
		Handler:           newRouter(State),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("test-sink listening", "addr", addr)
	return srv.ListenAndServe()
}
