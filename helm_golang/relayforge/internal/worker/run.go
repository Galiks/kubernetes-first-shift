package worker

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"relayforge/internal/body"
	"relayforge/internal/config"
	"relayforge/internal/logging"
	"relayforge/internal/secrets"
)

// Run is the frozen worker entrypoint. It exits the process via os.Exit using
// the documented exit-code contract; the returned error is never surfaced to a
// caller (matches the Python main() that always sys.exit()).
func Run(cfg *config.Config) error {
	os.Exit(run(cfg))
	return nil
}

// run performs a single delivery and returns the process exit code. It is
// separated from Run so the logic is unit-testable without os.Exit.
func run(cfg *config.Config) int {
	logging.Setup()

	release := os.Getenv("RELAYFORGE_RELEASE")
	deliveryID := os.Getenv("RELAYFORGE_DELIVERY_ID")
	destination := os.Getenv("RELAYFORGE_DESTINATION")
	eventType := os.Getenv("RELAYFORGE_EVENT_TYPE")
	payloadRaw := os.Getenv("RELAYFORGE_PAYLOAD")
	podName := os.Getenv("HOSTNAME")
	if podName == "" {
		podName = "unknown"
	}

	// Startup failures (config, secret) are fatal pre-network errors; Python
	// lets these raise and exit with code 1.
	dests, err := config.LoadDestinations(cfg.DestinationsPath)
	if err != nil {
		slog.Error("load destinations", "error", trunc(err.Error()))
		return 1
	}
	baseURL, err := config.GetDestinationURL(destination, dests)
	if err != nil {
		slog.Error("resolve destination", "error", trunc(err.Error()))
		return 1
	}
	signingKey, err := secrets.Read(secrets.SigningKeyPath)
	if err != nil {
		slog.Error("read signing key", "error", trunc(err.Error()))
		return 1
	}

	// Body must byte-match orjson.dumps({delivery_id,event_type,payload},
	// OPT_SORT_KEYS). payload is parsed so numeric literals keep types.
	payloadVal, err := body.Unmarshal([]byte(payloadRaw))
	if err != nil {
		slog.Error("parse payload", "error", trunc(err.Error()))
		return 1
	}
	payloadObj, ok := payloadVal.(map[string]interface{})
	if !ok {
		slog.Error("payload root must be an object")
		return 1
	}

	bodyBytes, err := body.Marshal(map[string]interface{}{
		"delivery_id": deliveryID,
		"event_type":  eventType,
		"payload":     payloadObj,
	})
	if err != nil {
		slog.Error("marshal body", "error", trunc(err.Error()))
		return 1
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	sig := Sign(timestamp, deliveryID, bodyBytes, signingKey)
	headers := map[string]string{
		"X-Relay-Id":        deliveryID,
		"X-Relay-Timestamp": timestamp,
		"X-Relay-Signature": "v1=" + sig,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// SIGTERM -> transient (mirrors Python shutdown event -> wait_for abort).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Overall deadline: WorkerActiveDeadline - 5s (Python wait_for).
	overall := time.Duration(cfg.WorkerActiveDeadline-5) * time.Second
	ctx, cancel = context.WithTimeout(ctx, overall)
	defer cancel()

	client := NewClient(
		time.Duration(cfg.WorkerConnectTimeout*float64(time.Second)),
		time.Duration(cfg.WorkerReadTimeout*float64(time.Second)),
	)

	start := time.Now()
	status, err := Deliver(ctx, client, baseURL+"/events", bodyBytes, headers)
	if err != nil {
		if ctx.Err() != nil {
			// Timeout or SIGTERM: transient.
			slog.Info("request timeout", "release", release, "delivery_id", deliveryID,
				"destination", destination, "pod", podName)
			return ExitTransient
		}
		slog.Info("network error", "release", release, "delivery_id", deliveryID,
			"destination", destination, "pod", podName, "error", trunc(err.Error()))
		return ExitNetwork
	}

	durationMS := int(time.Since(start).Milliseconds())
	slog.Info("request completed", "release", release, "delivery_id", deliveryID,
		"destination", destination, "pod", podName, "http_status", status, "duration_ms", durationMS)

	switch classify(status) {
	case "success":
		return ExitSuccess
	case "permanent":
		return ExitPermanent
	default:
		return ExitTransient
	}
}

// trunc limits error text to 200 chars for logging (matches Python [:200]).
func trunc(s string) string {
	if len(s) <= 200 {
		return s
	}
	return s[:200]
}
