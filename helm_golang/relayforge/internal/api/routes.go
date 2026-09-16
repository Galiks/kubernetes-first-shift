package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"relayforge/internal/canonical"
)

// runtimeState is the single API process state (one per pod), mirroring
// state.py. Tests overwrite its fields directly like the Python test suite.
var runtimeState *State

// retryBudget is the shared k8s retry budget of the process (routes.py has a
// module-level RetryBudget()).
var retryBudget = NewRetryBudget()

// handleCreateDelivery mirrors routes.create_delivery:
// verify token -> validate -> perform_create.
func handleCreateDelivery(w http.ResponseWriter, r *http.Request) {
	st := runtimeState
	if err := verifyToken(w, r, st); err != nil {
		writeApiError(w, err)
		return
	}
	vr, err := validateRequest(w, r, st)
	if err != nil {
		writeApiError(w, err)
		return
	}
	statusCode, payload, perr := performCreate(st, vr)
	if perr != nil {
		writeApiError(w, perr)
		return
	}
	writeJSON(w, statusCode, payload)
}

// writeApiError writes an ApiError (or maps an unknown error to 500).
func writeApiError(w http.ResponseWriter, err error) {
	ae, ok := err.(*ApiError)
	if !ok {
		ae = NewApiError(http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
	}
	WriteApiError(w, ae)
}

// performCreate mirrors routes.perform_create: the full accept path used by
// both /v1/deliveries and the UI. Returns (http_status, payload) like Python.
func performCreate(st *State, vr *validatedRequest) (int, map[string]interface{}, error) {
	start := time.Now()

	// canonical request bytes: {destination, event_type, payload} sorted keys.
	canonicalBytes, err := canonicalRequestBytes(vr)
	if err != nil {
		return 0, nil, NewApiError(http.StatusBadRequest, "INVALID_JSON", err.Error(), nil)
	}
	idemKeyHash := sha256.Sum256([]byte(vr.idemKey))
	name := JobName(st.cfg.Release, vr.idemKey)
	deliveryID, err := NewDeliveryID(st.cfg.Release)
	if err != nil {
		return 0, nil, NewApiError(http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
	}
	payloadJSON, err := payloadToJSON(vr.payload)
	if err != nil {
		return 0, nil, NewApiError(http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
	}

	cfg := st.cfg
	job, berr := BuildJob(jobBuildParams{
		Release:            cfg.Release,
		Namespace:          cfg.Namespace,
		Name:               name,
		DeliveryID:         deliveryID,
		CanonicalBytes:     canonicalBytes,
		IdemKeyHash:        hex.EncodeToString(idemKeyHash[:]),
		Destination:        vr.dest,
		EventType:          vr.event,
		PayloadJSON:        payloadJSON,
		Image:              cfg.ImageRepository,
		ImageDigest:        cfg.ImageDigest,
		SigningSecretName:  cfg.SigningSecretName,
		SigningSecretKey:   cfg.SigningSecretKey,
		DestinationsConfig: cfg.DestinationsConfig,
		WorkerSA:           cfg.WorkerSA,
		BackoffLimit:       cfg.WorkerBackoffLimit,
		ActiveDeadline:     cfg.WorkerActiveDeadline,
		TTLAfterFinished:   cfg.WorkerTTL,
		PermanentExitCode:  cfg.WorkerPermanentExit,
		SecretRevision:     cfg.SecretRevision,
	})
	if berr != nil {
		return 0, nil, NewApiError(http.StatusInternalServerError, "INTERNAL", berr.Error(), nil)
	}

	bp := st.backpressureRef()
	registry := st.jobRegistryRef()
	kc := st.k8sClients()
	if kc == nil {
		return 0, nil, NewApiError(http.StatusServiceUnavailable, "UI_UNAVAILABLE", "kubernetes client not initialized", nil)
	}

	// Soft active-job limit: check and reserve atomically right before create,
	// inside the concurrent-create semaphore. A repeat key with a known job is
	// not blocked by the limit.
	var reserved bool
	if bp != nil {
		bp.acquire()
		defer bp.release()
		var rerr error
		reserved, rerr = bp.CheckAndReserve(name)
		if rerr != nil {
			return 0, nil, rerr
		}
	}

	created, cors := createJobIdempotentNoErr(kc, cfg.Namespace, job)
	if cors != nil {
		if reserved && registry != nil {
			registry.Release(name)
		}
		if ae, ok := cors.(*ApiError); ok && ae.Status == http.StatusConflict {
			incIdempotencyConflict()
		}
		slog.Error("create job failed", "code", cors.Error())
		return 0, nil, cors
	}

	finalID := created.Job.Annotations["relayforge/delivery-id"]
	if finalID == "" {
		finalID = deliveryID
	}
	createdAt := created.Job.Annotations["relayforge/created-at"]
	if createdAt == "" {
		createdAt = job.Annotations["relayforge/created-at"]
	}
	duplicate := created.Origin == "existing"
	statusCode := http.StatusAccepted
	if duplicate {
		statusCode = http.StatusOK
		// Python releases the local reservation ONLY on the duplicate path: the
		// job already exists and was confirmed by create, so the pending entry
		// is no longer needed (the watch will drop it anyway).
		if registry != nil {
			registry.Release(name)
		}
	} else {
		// Created path: KEEP the pending reservation. It protects the soft
		// active-job limit until the watch confirms the freshly created job
		// (Python does not release here either).
		incJobsCreated()
	}

	payload := map[string]interface{}{
		"id":         finalID,
		"status":     jobStatus(created.Job),
		"attempts":   0,
		"duplicate":  duplicate,
		"created_at": createdAt,
	}
	observeRequest(statusCode, time.Since(start).Seconds())
	return statusCode, payload, nil
}

// createJobIdempotentNoErr wraps the k8s client call with a process-shared
// context; returns (result, error) where error may be an *ApiError.
func createJobIdempotentNoErr(kc *kubeClients, namespace string, job *batchv1.Job) (*CreateJobResult, error) {
	return createJobIdempotent(context.Background(), *kc, namespace, job, retryBudget, job.Annotations["relayforge/request-hash"])
}

// canonicalRequestBytes mirrors the canonicalize({destination,event_type,payload}) call.
func canonicalRequestBytes(vr *validatedRequest) ([]byte, error) {
	n := canonical.Node{
		Kind: canonical.KindObject,
		Obj: map[string]canonical.Node{
			"destination": {Kind: canonical.KindString, Str: vr.dest},
			"event_type":  {Kind: canonical.KindString, Str: vr.event},
			"payload":     {Kind: canonical.KindObject, Obj: vr.payload},
		},
	}
	var b []byte
	return n.AppendTo(&b)
}

// handleGetDelivery mirrors routes.get_delivery.
func handleGetDelivery(w http.ResponseWriter, r *http.Request) {
	st := runtimeState
	if err := verifyToken(w, r, st); err != nil {
		writeApiError(w, err)
		return
	}
	deliveryID := r.PathValue("id")
	kc := st.k8sClients()
	if kc == nil {
		writeApiError(w, NewApiError(http.StatusServiceUnavailable, "UI_UNAVAILABLE", "kubernetes client not initialized", nil))
		return
	}
	ctx := r.Context()
	cfg := st.cfg
	jobs, err := kc.batch.BatchV1().Jobs(cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "relayforge/delivery-id=" + deliveryID,
	})
	if err != nil {
		writeApiError(w, err)
		return
	}
	if len(jobs.Items) == 0 {
		writeApiError(w, NewApiError(http.StatusNotFound, "DELIVERY_NOT_FOUND", "job no longer exists", nil))
		return
	}
	job := &jobs.Items[0]

	pods, err := kc.pods.ListPods(ctx, cfg.Namespace, metav1.ListOptions{
		LabelSelector: "job-name=" + job.Name,
	})
	if err != nil {
		writeApiError(w, err)
		return
	}
	attempts := len(pods.Items)

	retainedUntil := ""
	if isTerminal(job) {
		finished := job.Status.CompletionTime
		if finished == nil {
			finished = job.Status.StartTime
		}
		if finished != nil {
			retainedUntil = isoFormat(finished.Time.Add(time.Duration(cfg.WorkerTTL) * time.Second))
		}
	}

	var finishedAt interface{}
	if job.Status.CompletionTime != nil {
		finishedAt = isoFormat(job.Status.CompletionTime.Time)
	}
	resp := map[string]interface{}{
		"id":            deliveryID,
		"status":        jobStatus(job),
		"attempts":      attempts,
		"destination":   jobDestination(job),
		"created_at":    annotationOrNil(job, "relayforge/created-at"),
		"finished_at":   finishedAt,
		"retained_until": nilIfEmpty(retainedUntil),
		"failure":       jobFailure(job),
	}
	writeJSON(w, http.StatusOK, resp)
}

func annotationOrNil(job *batchv1.Job, key string) interface{} {
	if job.Annotations == nil {
		return nil
	}
	v, ok := job.Annotations[key]
	if !ok {
		return nil
	}
	return v
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// jobDestination mirrors routes._job_destination: env[1].value of the first
// container (RELAYFORGE_DESTINATION).
func jobDestination(job *batchv1.Job) interface{} {
	containers := job.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return nil
	}
	env := containers[0].Env
	if len(env) < 2 {
		return nil
	}
	return env[1].Value
}

// jobStatus mirrors routes._job_status.
func jobStatus(job *batchv1.Job) string {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return "succeeded"
		}
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return "failed"
		}
	}
	if job.Status.Active > 0 {
		return "running"
	}
	return "queued"
}

// jobFailure mirrors routes._job_failure.
func jobFailure(job *batchv1.Job) interface{} {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			msg := c.Message
			if msg == "" {
				msg = "job failed"
			}
			return map[string]string{"code": "JOB_FAILED", "message": msg}
		}
	}
	return nil
}