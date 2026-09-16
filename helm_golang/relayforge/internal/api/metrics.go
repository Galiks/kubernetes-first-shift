package api

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics registry with the SAME names as relayforge/api/metrics.py.
var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "relayforge_requests_total", Help: "HTTP requests processed by result"},
		[]string{"result"},
	)
	jobsCreatedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{Name: "relayforge_jobs_created_total", Help: "Delivery jobs created"},
	)
	idempotencyConflictsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{Name: "relayforge_idempotency_conflicts_total", Help: "Idempotency key conflicts"},
	)
	kubernetesErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "relayforge_kubernetes_errors_total", Help: "Kubernetes API errors by operation"},
		[]string{"operation"},
	)
	kubernetesRetriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "relayforge_kubernetes_retries_total", Help: "Kubernetes client retries by reason"},
		[]string{"reason"},
	)
	requestDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: "relayforge_request_duration_seconds", Help: "HTTP request duration seconds"},
		[]string{"result"},
	)
	activeJobs = prometheus.NewGauge(
		prometheus.GaugeOpts{Name: "relayforge_active_jobs", Help: "Active delivery jobs observed by this pod"},
	)
	oldestActiveJobSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{Name: "relayforge_oldest_active_job_seconds", Help: "Age in seconds of the oldest active delivery job"},
	)
)

func init() {
	prometheus.MustRegister(
		requestsTotal, jobsCreatedTotal, idempotencyConflictsTotal,
		kubernetesErrorsTotal, kubernetesRetriesTotal, requestDurationSeconds,
		activeJobs, oldestActiveJobSeconds,
	)
}

// resultLabel mirrors metrics.result_label.
func resultLabel(statusCode int) string {
	switch {
	case statusCode == 401:
		return "unauthorized"
	case statusCode == 409:
		return "conflict"
	case statusCode == 429 || statusCode == 503:
		return "backpressure"
	case statusCode >= 200 && statusCode < 300:
		return "success"
	case statusCode >= 400 && statusCode < 500:
		return "invalid"
	default:
		return "error"
	}
}

func observeRequest(statusCode int, durationSeconds float64) {
	result := resultLabel(statusCode)
	requestsTotal.WithLabelValues(result).Inc()
	requestDurationSeconds.WithLabelValues(result).Observe(durationSeconds)
}

func incJobsCreated()          { jobsCreatedTotal.Inc() }
func incIdempotencyConflict()  { idempotencyConflictsTotal.Inc() }
func incK8sError(operation string) {
	kubernetesErrorsTotal.WithLabelValues(operation).Inc()
}
func incK8sRetry(reason string) {
	kubernetesRetriesTotal.WithLabelValues(reason).Inc()
}

func setActiveJobs(count int, oldestSeconds float64) {
	activeJobs.Set(float64(count))
	oldestActiveJobSeconds.Set(oldestSeconds)
}

// metricsHandler serves /metrics in the prometheus text exposition format,
// mirroring metrics.router GET /metrics.
func metricsHandler() http.Handler {
	return promhttp.Handler()
}