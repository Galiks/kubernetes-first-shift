package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"relayforge/internal/config"
	"relayforge/internal/secrets"
)

const metricsInterval = 5 * time.Second

// Run mirrors relayforge/api/app.py main(): load destinations, build the k8s
// clients, start the JobRegistry watch and the metrics loop, serve HTTP until
// SIGTERM/SIGINT, then perform graceful shutdown:
//   - readiness=false immediately on signal
//   - drain in-flight requests (30s cap, like uvicorn timeout_graceful_shutdown)
//   - stop the JobRegistry watch and the metrics loop
//
// It blocks until the server exits and returns nil on graceful shutdown.
func Run(cfg *config.Config) error {
	logging := slog.Default()
	_ = logging

	st := newState(cfg)
	runtimeState = st

	token, err := secrets.Read(secrets.ClientTokenPath)
	if err != nil {
		return err
	}
	st.setToken(token)

	dests, err := config.LoadDestinations(cfg.DestinationsPath)
	if err != nil {
		return err
	}
	st.setDestinations(dests)

	kc, err := createK8sClients()
	if err != nil {
		return err
	}
	st.setK8s(kc)

	registry := NewJobRegistry(kc.batch, cfg.Namespace, cfg.Release)
	st.setJobRegistry(registry)

	bp := NewBackpressure(cfg.APIBackpressureActive, cfg.APIBackpressureCreate, registry)
	st.setBackpressure(bp)

	if cfg.TestFaultCreateTimeout {
		createJobFault.enabled.Store(true)
		armCreateJobFault()
	}

	registry.Start()

	// Startup complete: readiness flips true (app.py sets state.ready = True
	// after the lifespan startup block). Without this /readyz would stay 503
	// and the pod would never become Ready.
	st.setReady(true)

	// Metrics loop: publish active-job gauges every 5s (app._metrics_loop).
	stopMetrics := make(chan struct{})
	metricsDone := make(chan struct{})
	go func() {
		defer close(metricsDone)
		t := time.NewTicker(metricsInterval)
		defer t.Stop()
		for {
			select {
			case <-stopMetrics:
				return
			case <-t.C:
				if reg := st.jobRegistryRef(); reg != nil {
					setActiveJobs(reg.ActiveCount(), reg.OldestActiveSeconds())
				}
			}
		}
	}()

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.APIPort),
		Handler:           buildRouter(st),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("api server listening", "addr", srv.Addr)
		serverErr <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-serverErr:
		// The server failed to start or stopped on its own (e.g. bind failure
		// on an occupied port). Fail fast instead of blocking forever on a
		// signal — Python/uvicorn exits immediately in this case.
		st.setReady(false)
		close(stopMetrics)
		<-metricsDone
		registry.Stop()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case sig := <-sigCh:
		slog.Info("received signal; shutting down", "signal", sig.String())
	}
	st.setReady(false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)

	close(stopMetrics)
	<-metricsDone
	registry.Stop()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	default:
	}
	return nil
}

// buildRouter wires the routes of the Python app: v1 deliveries, probes,
// metrics, UI group.
func buildRouter(st *State) http.Handler {
	mux := http.NewServeMux()

	// v1 API
	mux.HandleFunc("POST /v1/deliveries", handleCreateDelivery)
	mux.HandleFunc("GET /v1/deliveries/{id}", handleGetDelivery)

	// probes
	mux.HandleFunc("GET /livez", handleLivez)
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) { handleReadyz(w, r, st) })

	// metrics
	mux.Handle("GET /metrics", metricsHandler())

	// ui group (page, root redirect, /ui/api/*)
	mux.HandleFunc("GET /", handleIndex)
	mux.HandleFunc("GET /ui", handleUIPage)
	mux.HandleFunc("GET /ui/api/config", handleUIConfig)
	mux.HandleFunc("GET /ui/api/stats", handleUIStats)
	mux.HandleFunc("GET /ui/api/deliveries", handleUIDeliveries)
	mux.HandleFunc("POST /ui/api/deliveries", handleUISubmitDelivery)
	mux.HandleFunc("GET /ui/api/sink/state", handleUISinkState)
	mux.HandleFunc("POST /ui/api/sink/mode", handleUISinkMode)
	mux.HandleFunc("POST /ui/api/sink/reset", handleUISinkReset)
	mux.HandleFunc("GET /ui/api/tests", handleUITestsHistory)
	mux.HandleFunc("POST /ui/api/tests/run", handleUIRunTest)
	mux.HandleFunc("GET /ui/api/deliveries/{id}/logs", handleUIDeliveryLogs)

	return withMetricsMiddleware(mux)
}

// withMetricsMiddleware mirrors app.metrics_middleware: observe every response
// by status code and duration.
func withMetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		observeRequest(rec.status, time.Since(start).Seconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}