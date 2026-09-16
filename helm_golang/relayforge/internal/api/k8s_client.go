package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// createK8sClients mirrors create_k8s_client: in-cluster config, shared
// connection pool and request timeout; falls back to kubeconfig for local runs
// (e.g. tests) so the binary can also run outside the cluster.
func createK8sClients() (*kubeClients, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		kubeCfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
		cfg, err = kubeCfg.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("load kubernetes config (in-cluster and kubeconfig): %w", err)
		}
	}
	cfg.Transport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       90 * time.Second,
		MaxConnsPerHost:       50,
		ResponseHeaderTimeout: 10 * time.Second,
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
		}).DialContext,
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes clientset: %w", err)
	}
	return &kubeClients{batch: clientset, pods: &livePodReader{cs: clientset}}, nil
}

// livePodReader reads pods/pod-logs through the real typed clientset.
type livePodReader struct{ cs kubernetes.Interface }

func (r *livePodReader) ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error) {
	return r.cs.CoreV1().Pods(namespace).List(ctx, opts)
}

func (r *livePodReader) ReadPodLog(ctx context.Context, namespace, name string, opts *corev1.PodLogOptions) ([]byte, error) {
	return r.cs.CoreV1().Pods(namespace).GetLogs(name, opts).DoRaw(ctx)
}

// RetryBudget mirrors k8s_client.RetryBudget: rolling 60-per-minute window plus
// a 12-acquisition total cap.
type RetryBudget struct {
	mu           sync.Mutex
	maxPerMinute int
	maxTotal     int
	window       []time.Time
	total        int
}

func NewRetryBudget() *RetryBudget {
	return &RetryBudget{maxPerMinute: 60, maxTotal: 12}
}

// NewRetryBudgetWith builds a budget with explicit caps (used by tests to shape
// the budget for scenario coverage).
func NewRetryBudgetWith(maxPerMinute, maxTotal int) *RetryBudget {
	return &RetryBudget{maxPerMinute: maxPerMinute, maxTotal: maxTotal}
}

// TryAcquire mirrors try_acquire.
func (b *RetryBudget) TryAcquire() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	cut := now.Add(-60 * time.Second)
	kept := b.window[:0]
	for _, t := range b.window {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	b.window = kept
	if b.total >= b.maxTotal || len(b.window) >= b.maxPerMinute {
		return false
	}
	b.window = append(b.window, now)
	b.total++
	return true
}

func (b *RetryBudget) reset() {
	b.mu.Lock()
	b.window = nil
	b.total = 0
	b.mu.Unlock()
}

// k8sStatusError is returned by test reactors and wraps a synthetic Kubernetes
// error status; it satisfies k8serrors.APIStatus so real and fake paths share
// the same classification.
type k8sStatusError struct {
	code    int32
	message string
	ra      string // Retry-After value, if any
}

func (e *k8sStatusError) Error() string { return e.message }

func (e *k8sStatusError) Status() metav1.Status {
	return metav1.Status{
		Status: metav1.StatusFailure,
		Reason: metav1.StatusReasonUnknown,
		Code:   e.code,
		Message: e.message,
	}
}

// mustK8sError maps a client error to its HTTP status code; 0 means a
// client-side timeout/transport failure (no HTTP response), mirroring the
// Python client ApiException.status == 0 case.
func mustK8sError(err error) int {
	var se *k8sStatusError
	if errors.As(err, &se) {
		return int(se.code)
	}
	var statusErr *k8serrors.StatusError
	if errors.As(err, &statusErr) {
		return int(statusErr.Status().Code)
	}
	var apiErr k8serrors.APIStatus
	if errors.As(err, &apiErr) {
		return int(apiErr.Status().Code)
	}
	return 0
}

// parseRetryAfter mirrors _retry_after_seconds: Retry-After value clamped to
// [0.5, 30], default 5.0 when absent/unparseable.
func parseRetryAfter(err error) float64 {
	secs := 5.0
	var se *k8sStatusError
	if errors.As(err, &se) && se.ra != "" {
		if f, e := strconv.ParseFloat(strings.TrimSpace(se.ra), 64); e == nil {
			secs = f
		}
	}
	var statusErr *k8serrors.StatusError
	if errors.As(err, &statusErr) {
		if d := statusErr.ErrStatus.Details; d != nil {
			// Real API-server 429s carry the delay in Details.RetryAfterSeconds
			// (int32; 0 means unset). Prefer it over the header Causes.
			if d.RetryAfterSeconds > 0 {
				secs = float64(d.RetryAfterSeconds)
			}
			for _, c := range d.Causes {
				if c.Field == "retryAfter" {
					if f, e := strconv.ParseFloat(strings.TrimSpace(c.Message), 64); e == nil {
						secs = f
					}
				}
			}
		}
	}
	if secs < 0.5 {
		secs = 0.5
	}
	if secs > 30 {
		secs = 30
	}
	return secs
}

// readJobOrNone mirrors _read_job_or_none: 403 -> K8S_FORBIDDEN (500, no
// retry), 404 -> nil.
func readJobOrNone(ctx context.Context, c kubeClients, namespace, name string) (*batchv1.Job, error) {
	job, err := c.batch.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if mustK8sError(err) == 403 {
			return nil, NewApiError(500, "K8S_FORBIDDEN", "API cannot read jobs", nil)
		}
		if k8serrors.IsNotFound(err) || mustK8sError(err) == 404 {
			return nil, nil
		}
		return nil, err
	}
	return job, nil
}

// checkCompatible mirrors _check_compatible: the request-hash annotation of the
// existing job must match the canonical hash of this request.
func checkCompatible(existing *batchv1.Job, canonicalHash string) error {
	actual := existing.Annotations["relayforge/request-hash"]
	if actual != canonicalHash {
		return NewApiError(409, "IDEMPOTENCY_CONFLICT", "idempotency key belongs to another request", nil)
	}
	return nil
}

// jobClient is the minimal typed BatchV1 job interface the idempotent creator
// needs; both the real clientset and the k8s fake clientset satisfy it.
type jobClient interface {
	Create(ctx context.Context, job *batchv1.Job, opts metav1.CreateOptions) (*batchv1.Job, error)
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*batchv1.Job, error)
}

// One-shot ambiguity fault (scenario 02), gated by TestFaultCreateTimeout:
// the API server accepts the Job but the caller sees an indeterminate 5xx.
var createJobFault struct {
	enabled atomic.Bool
	armed   atomic.Bool
}

// armCreateJobFault arms the one-shot fault (used by Run when the config flag
// is set and by tests to reproduce scenario 02).
func armCreateJobFault() { createJobFault.armed.Store(true) }

func consumeCreateJobFault() bool {
	if createJobFault.enabled.Load() && createJobFault.armed.Swap(false) {
		return true
	}
	return false
}

// CreateJobResult mirrors the Python (job, origin) tuple; Origin is "created"
// or "existing".
type CreateJobResult struct {
	Job    *batchv1.Job
	Origin string
}

// createJobIdempotent mirrors k8s_client.create_job_idempotent with EXACT
// semantics:
//
//   - 403 -> K8S_FORBIDDEN (500), no retry
//   - 409 -> read existing job by name and verify request-hash; mismatch ->
//     409 IDEMPOTENCY_CONFLICT; job vanished -> retry create
//   - 429 -> honor Retry-After (clamped 0.5..30s), retry within budget
//   - 5xx or status 0 (client timeout) -> read job by name; if exists and
//     hash-compatible return it as "existing", else retry create
//   - budget exhausted -> RETRY_BUDGET_EXHAUSTED
//
// One-shot fault injection (TestFaultCreateTimeout): create was accepted
// server-side but the client returns 503 CREATE_AMBIGUOUS.
func createJobIdempotent(ctx context.Context, c kubeClients, namespace string, job *batchv1.Job, budget *RetryBudget, canonicalHash string) (*CreateJobResult, error) {
	name := job.Name
	var jobs jobClient = c.batch.BatchV1().Jobs(namespace)
	for {
		created, err := jobs.Create(ctx, job, metav1.CreateOptions{})
		if err == nil {
			if consumeCreateJobFault() {
				return nil, NewApiError(503, "CREATE_AMBIGUOUS", "create result ambiguous (test fault injection)", nil)
			}
			return &CreateJobResult{Job: created, Origin: "created"}, nil
		}
		status := mustK8sError(err)
		switch {
		case status == 403:
			return nil, NewApiError(500, "K8S_FORBIDDEN", "API cannot create jobs", nil)

		case status == 409:
			existing, rerr := readJobOrNone(ctx, c, namespace, name)
			if rerr != nil {
				return nil, rerr
			}
			if existing == nil {
				continue // job disappeared before the read — retry the create
			}
			if cerr := checkCompatible(existing, canonicalHash); cerr != nil {
				return nil, cerr
			}
			return &CreateJobResult{Job: existing, Origin: "existing"}, nil

		case status == 429:
			if !budget.TryAcquire() {
				return nil, NewApiError(503, "RETRY_BUDGET_EXHAUSTED", "k8s retry budget exhausted", nil)
			}
			incK8sRetry("rate_limited")
			incK8sError("create_job")
			time.Sleep(time.Duration(parseRetryAfter(err) * float64(time.Second)))
			continue

		case status >= 500 || status == 0:
			if !budget.TryAcquire() {
				return nil, NewApiError(503, "RETRY_BUDGET_EXHAUSTED", "k8s retry budget exhausted", nil)
			}
			incK8sRetry("server_error")
			incK8sError("create_job")
			existing, rerr := readJobOrNone(ctx, c, namespace, name)
			if rerr != nil {
				return nil, rerr
			}
			if existing != nil {
				if cerr := checkCompatible(existing, canonicalHash); cerr != nil {
					return nil, cerr
				}
				return &CreateJobResult{Job: existing, Origin: "existing"}, nil
			}
			continue

		default:
			return nil, fmt.Errorf("create job: %w", err)
		}
	}
}