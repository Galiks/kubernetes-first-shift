package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"relayforge/internal/canonical"
	"relayforge/internal/secrets"
)

// uiTesting slots: control-token cache and test history (module globals in
// ui.py).
var (
	controlTokenMu  sync.Mutex
	controlTokenCache  []byte
	controlTokenErr    string
	uiTestHistoryMu    sync.Mutex
	uiTestHistory      []map[string]interface{}
)

// uiControlToken mirrors ui._control_token: cached read of the sink control
// token; a read failure is cached and raises SINK_CONTROL_UNAVAILABLE.
func uiControlToken(cfgPath string) ([]byte, error) {
	controlTokenMu.Lock()
	defer controlTokenMu.Unlock()
	if controlTokenCache != nil {
		return controlTokenCache, nil
	}
	if controlTokenErr != "" {
		return nil, NewApiError(503, "SINK_CONTROL_UNAVAILABLE", controlTokenErr, nil)
	}
	t, err := secrets.Read(cfgPath)
	if err != nil {
		controlTokenErr = fmt.Sprintf("control-token not mounted: %v", err)
		return nil, NewApiError(503, "SINK_CONTROL_UNAVAILABLE", controlTokenErr, nil)
	}
	controlTokenCache = t
	return t, nil
}

// uiSinkRequest mirrors ui._sink_request: calls the test-sink control endpoints
// with a 5s timeout.
func uiSinkRequest(ctx context.Context, sinkURL, path, method string, token bool, payload interface{}) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, sinkURL+path, nil)
	if err != nil {
		return nil, nil, NewApiError(503, "SINK_UNREACHABLE", fmt.Sprintf("test-sink недоступен: %v", err), nil)
	}
	if token {
		t, terr := uiControlToken(runtimeState.configRef().ControlTokenSinkPath)
		if terr != nil {
			return nil, nil, terr
		}
		req.Header.Set("Authorization", "Bearer "+string(t))
	}
	if payload != nil {
		body, _ := json.Marshal(payload)
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, NewApiError(503, "SINK_UNREACHABLE", fmt.Sprintf("test-sink недоступен: %v", err), nil)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, NewApiError(503, "SINK_UNREACHABLE", fmt.Sprintf("test-sink недоступен: %v", err), nil)
	}
	return resp, body, nil
}

// handleIndex mirrors ui.index: redirect to /ui.
func handleIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/ui", http.StatusTemporaryRedirect)
}

// handleUIPage mirrors ui.ui_page: serves the embedded static HTML verbatim.
func handleUIPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(uiHTML)
}

// handleUIConfig mirrors ui.ui_config.
func handleUIConfig(w http.ResponseWriter, r *http.Request) {
	cfg := runtimeState.configRef()
	dests := runtimeState.destinationsCopy()
	names := make([]string, 0, len(dests))
	for n := range dests {
		names = append(names, n)
	}
	sort.Strings(names)
	var maxActive interface{}
	if bp := runtimeState.backpressureRef(); bp != nil {
		maxActive = bp.maxActive
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"release":        cfg.Release,
		"namespace":      cfg.Namespace,
		"destinations":   names,
		"max_active_jobs": maxActive,
		"ttl_seconds":    cfg.WorkerTTL,
		"sink_url":       cfg.SinkURL,
		"sink_modes":     []string{"normal", "fail-first", "accept-and-drop", "reject", "slow"},
	})
}

// metricLineValue mirrors ui._metric_line.
func metricLineValue(text, name string) float64 {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == name {
			if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
				return v
			}
		}
	}
	return 0.0
}

// metricLabeledValue mirrors ui._metric_labeled.
func metricLabeledValue(text, name, label, value string) float64 {
	prefix := fmt.Sprintf(`%s{%s="%s"`, name, label, value)
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			return v
		}
	}
	return 0.0
}

// handleUIStats mirrors ui.ui_stats.
func handleUIStats(w http.ResponseWriter, r *http.Request) {
	rec := httptest.NewRecorder()
	metricsHandler().ServeHTTP(rec, r)
	text := rec.Body.String()
	results := []string{"success", "conflict", "backpressure", "invalid", "unauthorized", "error"}
	total := 0.0
	for _, res := range results {
		total += metricLabeledValue(text, "relayforge_requests_total", "result", res)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"requests_success":       metricLabeledValue(text, "relayforge_requests_total", "result", "success"),
		"requests_conflict":      metricLabeledValue(text, "relayforge_requests_total", "result", "conflict"),
		"requests_backpressure":  metricLabeledValue(text, "relayforge_requests_total", "result", "backpressure"),
		"requests_invalid":       metricLabeledValue(text, "relayforge_requests_total", "result", "invalid"),
		"requests_unauthorized":  metricLabeledValue(text, "relayforge_requests_total", "result", "unauthorized"),
		"jobs_created_total":     metricLineValue(text, "relayforge_jobs_created_total"),
		"active_jobs":            metricLineValue(text, "relayforge_active_jobs"),
		"oldest_active_job_seconds": metricLineValue(text, "relayforge_oldest_active_job_seconds"),
		"requests_total":         total,
	})
}

// handleUIDeliveries mirrors ui.ui_deliveries.
func handleUIDeliveries(w http.ResponseWriter, r *http.Request) {
	cfg := runtimeState.configRef()
	kc := runtimeState.k8sClients()
	if kc == nil {
		writeApiError(w, NewApiError(503, "UI_UNAVAILABLE", "kubernetes client not initialized", nil))
		return
	}
	ctx := r.Context()
	jobs, err := kc.batch.BatchV1().Jobs(cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + cfg.Release + ",app.kubernetes.io/component=delivery",
		Limit:         50,
	})
	if err != nil {
		writeApiError(w, err)
		return
	}
	pods, err := kc.pods.ListPods(ctx, cfg.Namespace, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + cfg.Release + ",app.kubernetes.io/component=delivery",
		Limit:         2000,
	})
	if err != nil {
		writeApiError(w, err)
		return
	}
	attempts := map[string]int{}
	for i := range pods.Items {
		name := pods.Items[i].Labels["job-name"]
		if name != "" {
			attempts[name]++
		}
	}
	items := make([]map[string]interface{}, 0, len(jobs.Items))
	for i := range jobs.Items {
		job := &jobs.Items[i]
		items = append(items, jobToDelivery(job, attempts[job.Name]))
	}
	sort.SliceStable(items, func(a, b int) bool {
		ca, _ := items[a]["created_at"].(string)
		cb, _ := items[b]["created_at"].(string)
		return ca > cb
	})
	// sink state for the first 15 deliveries (best-effort).
	limit := len(items)
	if limit > 15 {
		limit = 15
	}
	for _, it := range items[:limit] {
		if id, ok := it["id"].(string); ok {
			it["sink_state"] = uiSinkReceived(ctx, cfg.SinkURL, id)
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"deliveries": items, "count": len(items)})
}

// uiSinkReceived mirrors ui._sink_received: GET /received/{id} with a 3s
// timeout; returns nil on any failure.
func uiSinkReceived(ctx context.Context, sinkURL, deliveryID string) interface{} {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sinkURL+"/received/"+deliveryID, nil)
	if err != nil {
		return nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	var v interface{}
	if json.Unmarshal(body, &v) == nil {
		return v
	}
	return nil
}

// jobToDelivery mirrors ui._job_to_delivery.
func jobToDelivery(job *batchv1.Job, attempts int) map[string]interface{} {
	annotations := job.Annotations
	var complete, failed *batchv1.JobCondition
	for i := range job.Status.Conditions {
		c := &job.Status.Conditions[i]
		if complete == nil && c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			complete = c
		}
		if failed == nil && c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			failed = c
		}
	}
	finishedAt := ""
	if complete != nil || failed != nil {
		cond := complete
		if cond == nil {
			cond = failed
		}
		if !cond.LastTransitionTime.IsZero() {
			finishedAt = isoFormat(cond.LastTransitionTime.Time)
		} else if job.Status.CompletionTime != nil {
			finishedAt = isoFormat(job.Status.CompletionTime.Time)
		}
	}
	status := "queued"
	switch {
	case complete != nil:
		status = "succeeded"
	case failed != nil:
		status = "failed"
	case job.Status.Active > 0:
		status = "running"
	}
	retainedUntil := ""
	if finishedAt != "" {
		if t, err := parseISO(finishedAt); err == nil {
			retainedUntil = isoFormat(t.Add(time.Duration(runtimeState.configRef().WorkerTTL) * time.Second))
		}
	}
	envMap := map[string]string{}
	containers := job.Spec.Template.Spec.Containers
	if len(containers) > 0 {
		for _, e := range containers[0].Env {
			if e.Value != "" {
				envMap[e.Name] = e.Value
			}
		}
	}
	var failure interface{}
	if failed != nil {
		msg := failed.Message
		if msg == "" {
			msg = "job failed"
		}
		failure = map[string]string{"code": "JOB_FAILED", "message": msg}
	}
	return map[string]interface{}{
		"id":             annotations["relayforge/delivery-id"],
		"job":            job.Name,
		"status":         status,
		"attempts":       attempts,
		"destination":    nilIfEmpty(envMap["RELAYFORGE_DESTINATION"]),
		"event_type":     nilIfEmpty(envMap["RELAYFORGE_EVENT_TYPE"]),
		"created_at":     annotations["relayforge/created-at"],
		"finished_at":    nilIfEmpty(finishedAt),
		"retained_until": nilIfEmpty(retainedUntil),
		"failure":        failure,
	}
}

// handleUISubmitDelivery mirrors ui.ui_submit_delivery: the "Начать" button.
func handleUISubmitDelivery(w http.ResponseWriter, r *http.Request) {
	st := runtimeState
	if st.k8sClients() == nil {
		writeApiError(w, NewApiError(503, "UI_UNAVAILABLE", "kubernetes client not initialized", nil))
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeApiError(w, NewApiError(400, "INVALID_JSON", fmt.Sprintf("invalid JSON body: %v", err), nil))
		return
	}
	data, err := canonical.StrictJSONLoads(raw)
	if err != nil {
		writeApiError(w, NewApiError(400, "INVALID_JSON", fmt.Sprintf("invalid JSON body: %v", err), nil))
		return
	}
	for _, field := range []string{"destination", "event_type", "payload"} {
		if _, ok := data[field]; !ok {
			writeApiError(w, NewApiError(422, "MISSING_FIELD", fmt.Sprintf("missing field: %s", field), nil))
			return
		}
	}
	destNode := data["destination"]
	evNode := data["event_type"]
	payloadNode := data["payload"]
	if destNode.Kind != canonical.KindString || evNode.Kind != canonical.KindString || payloadNode.Kind != canonical.KindObject {
		writeApiError(w, NewApiError(422, "INVALID_FIELD", "payload must be object", nil))
		return
	}
	if st.hasDestinations() {
		if _, ok := st.destinationsCopy()[destNode.Str]; !ok {
			writeApiError(w, NewApiError(422, "UNKNOWN_DESTINATION", fmt.Sprintf("unknown destination: %s", destNode.Str), nil))
			return
		}
	}
	if !eventTypeRe.MatchString(evNode.Str) {
		writeApiError(w, NewApiError(422, "INVALID_EVENT_TYPE", "event_type does not match pattern", nil))
		return
	}
	idemKey := data["idempotency_key"].Str // may be absent; empty -> generated
	if idemKey == "" {
		var b [16]byte
		_, _ = rand.Read(b[:])
		idemKey = "ui:" + hex.EncodeToString(b[:])
	}
	if !idempotencyKeyRe.MatchString(idemKey) {
		writeApiError(w, NewApiError(422, "INVALID_IDEMPOTENCY_KEY", "key does not match pattern", nil))
		return
	}
	vr := &validatedRequest{
		idemKey: idemKey,
		dest:    destNode.Str,
		event:   evNode.Str,
		payload: payloadNode.Obj,
	}
	statusCode, payload, perr := performCreate(st, vr)
	if perr != nil {
		writeApiError(w, perr)
		return
	}
	writeJSON(w, statusCode, payload)
}

// handleUISinkState mirrors ui.ui_sink_state.
func handleUISinkState(w http.ResponseWriter, r *http.Request) {
	cfg := runtimeState.configRef()
	resp, body, err := uiSinkRequest(r.Context(), cfg.SinkURL, "/control/state", http.MethodGet, true, nil)
	if err != nil {
		writeApiError(w, err)
		return
	}
	if resp.StatusCode != 200 {
		writeApiError(w, NewApiError(502, "SINK_ERROR", fmt.Sprintf("test-sink вернул %d", resp.StatusCode), nil))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

// handleUISinkMode mirrors ui.ui_sink_mode.
func handleUISinkMode(w http.ResponseWriter, r *http.Request) {
	cfg := runtimeState.configRef()
	var req map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&req)
	mode, _ := req["mode"].(string)
	allowed := map[string]bool{"normal": true, "fail-first": true, "accept-and-drop": true, "reject": true, "slow": true}
	if !allowed[mode] {
		keys := []string{"accept-and-drop", "fail-first", "normal", "reject", "slow"}
		writeApiError(w, NewApiError(422, "INVALID_MODE", fmt.Sprintf("mode must be one of %v", keys), nil))
		return
	}
	n := 1
	if v, ok := req["n"]; ok {
		switch t := v.(type) {
		case float64:
			n = int(t)
		case string:
			if t != "" {
				if p, err := strconv.Atoi(t); err == nil {
					n = p
				}
			}
		}
	}
	if n < 1 {
		n = 1
	}
	resp, body, err := uiSinkRequest(r.Context(), cfg.SinkURL, "/control/mode", http.MethodPost, true, map[string]interface{}{"mode": mode, "n": n})
	if err != nil {
		writeApiError(w, err)
		return
	}
	if resp.StatusCode != 200 {
		writeApiError(w, NewApiError(502, "SINK_ERROR", fmt.Sprintf("test-sink вернул %d", resp.StatusCode), nil))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

// handleUISinkReset mirrors ui.ui_sink_reset.
func handleUISinkReset(w http.ResponseWriter, r *http.Request) {
	cfg := runtimeState.configRef()
	resp, body, err := uiSinkRequest(r.Context(), cfg.SinkURL, "/control/reset", http.MethodPost, true, nil)
	if err != nil {
		writeApiError(w, err)
		return
	}
	if resp.StatusCode != 200 {
		writeApiError(w, NewApiError(502, "SINK_ERROR", fmt.Sprintf("test-sink вернул %d", resp.StatusCode), nil))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

// handleUITestsHistory mirrors ui.ui_tests_history.
func handleUITestsHistory(w http.ResponseWriter, r *http.Request) {
	uiTestHistoryMu.Lock()
	out := append([]map[string]interface{}{}, uiTestHistory...)
	uiTestHistoryMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{"tests": out})
}

// handleUIRunTest mirrors ui.ui_run_test: full scenario
// create -> duplicate -> terminal -> sink.
func handleUIRunTest(w http.ResponseWriter, r *http.Request) {
	st := runtimeState
	if st.k8sClients() == nil {
		writeApiError(w, NewApiError(503, "UI_UNAVAILABLE", "kubernetes client not initialized", nil))
		return
	}
	if !st.hasDestinations() {
		writeApiError(w, NewApiError(503, "NO_DESTINATIONS", "destinations not loaded", nil))
		return
	}
	started := time.Now()
	destinations := st.destinationsCopy()
	destination := "test"
	if _, ok := destinations["test"]; !ok {
		names := make([]string, 0, len(destinations))
		for n := range destinations {
			names = append(names, n)
		}
		sort.Strings(names)
		destination = names[0]
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	key := "ui:test:" + hex.EncodeToString(b[:])
	steps := []map[string]interface{}{}
	deliveryID := ""
	vr := &validatedRequest{
		idemKey: key,
		dest:    destination,
		event:   "ui.test",
		payload: canonical.Node{Kind: canonical.KindObject, Obj: map[string]canonical.Node{
			"source": {Kind: canonical.KindString, Str: "ui"},
		}}.Obj,
	}
	code, payload, perr := performCreate(st, vr)
	if perr != nil {
		if ae, ok := perr.(*ApiError); ok {
			steps = append(steps, map[string]interface{}{"name": "create", "ok": false, "detail": ae.Code + ": " + ae.Message})
		} else {
			steps = append(steps, map[string]interface{}{"name": "create", "ok": false, "detail": perr.Error()})
		}
		result := finishTest(steps, "", started)
		writeJSON(w, http.StatusOK, result)
		return
	}
	deliveryID, _ = payload["id"].(string)
	dupVal, _ := payload["duplicate"].(bool)
	steps = append(steps, map[string]interface{}{
		"name": "create",
		"ok":   code == 202,
		"detail": fmt.Sprintf("HTTP %d, id=%v, duplicate=%v", code, payload["id"], dupVal),
	})
	code2, payload2, perr2 := performCreate(st, vr)
	if perr2 != nil {
		steps = append(steps, map[string]interface{}{"name": "duplicate (тот же ключ)", "ok": false, "detail": perr2.Error()})
		steps = append(steps, map[string]interface{}{"name": "wait terminal", "ok": false, "detail": "status=queued"})
		steps = append(steps, map[string]interface{}{"name": "sink received", "ok": false, "detail": "{}"})
		result := finishTest(steps, deliveryID, started)
		writeJSON(w, http.StatusOK, result)
		return
	}
	dup2, _ := payload2["duplicate"].(bool)
	id2, _ := payload2["id"].(string)
	okDup := code2 == 200 && dup2 && id2 == deliveryID
	steps = append(steps, map[string]interface{}{
		"name":   "duplicate (тот же ключ)",
		"ok":     okDup,
		"detail": fmt.Sprintf("HTTP %d, id=%v, duplicate=%v", code2, payload2["id"], dup2),
	})

	status := uiDeliveryStatus(deliveryID)
	steps = append(steps, map[string]interface{}{"name": "wait terminal", "ok": status == "succeeded" || status == "failed", "detail": "status=" + status})

	sink := uiSinkReceived(r.Context(), runtimeState.configRef().SinkURL, deliveryID)
	okSink := false
	if m, ok := sink.(map[string]interface{}); ok {
		if applied, ok := m["applied"].(bool); ok && applied {
			okSink = true
		}
	}
	steps = append(steps, map[string]interface{}{"name": "sink received", "ok": okSink, "detail": fmt.Sprintf("%v", sink)})

	result := finishTest(steps, deliveryID, started)
	writeJSON(w, http.StatusOK, result)
}

// uiDeliveryStatus mirrors ui._delivery_status.
func uiDeliveryStatus(deliveryID string) string {
	cfg := runtimeState.configRef()
	kc := runtimeState.k8sClients()
	if kc == nil {
		return "deleted"
	}
	jobs, err := kc.batch.BatchV1().Jobs(cfg.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "relayforge/delivery-id=" + deliveryID,
	})
	if err != nil || len(jobs.Items) == 0 {
		return "deleted"
	}
	d := jobToDelivery(&jobs.Items[0], 0)
	if s, ok := d["status"].(string); ok {
		return s
	}
	return "queued"
}

// finishTest mirrors ui._finish_test.
func finishTest(steps []map[string]interface{}, deliveryID string, started time.Time) map[string]interface{} {
	passed := len(steps) > 0
	for _, s := range steps {
		if ok, _ := s["ok"].(bool); !ok {
			passed = false
			break
		}
	}
	result := map[string]interface{}{
		"passed":      passed,
		"delivery_id": deliveryID,
		"duration_ms": int(time.Since(started).Milliseconds()),
		"steps":       steps,
		"finished_at": isoFormat(time.Now().UTC()),
	}
	uiTestHistoryMu.Lock()
	uiTestHistory = append([]map[string]interface{}{result}, uiTestHistory...)
	if len(uiTestHistory) > 20 {
		uiTestHistory = uiTestHistory[:20]
	}
	uiTestHistoryMu.Unlock()
	return result
}

// handleUIDeliveryLogs mirrors ui.ui_delivery_logs: JSON-lines stream of
// worker pod logs with the pod name inside each record.
func handleUIDeliveryLogs(w http.ResponseWriter, r *http.Request) {
	deliveryID := r.PathValue("id")
	cfg := runtimeState.configRef()
	kc := runtimeState.k8sClients()
	if kc == nil {
		writeApiError(w, NewApiError(503, "UI_UNAVAILABLE", "kubernetes client not initialized", nil))
		return
	}
	ctx := r.Context()
	jobs, err := kc.batch.BatchV1().Jobs(cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "relayforge/delivery-id=" + deliveryID,
	})
	if err != nil {
		writeApiError(w, err)
		return
	}
	if len(jobs.Items) == 0 {
		writeApiError(w, NewApiError(404, "DELIVERY_NOT_FOUND", "job no longer exists", nil))
		return
	}
	jobName := jobs.Items[0].Name
	pods, err := kc.pods.ListPods(ctx, cfg.Namespace, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	sorted := append([]corev1.Pod{}, pods.Items...)
	sort.SliceStable(sorted, func(a, b int) bool {
		return sorted[a].CreationTimestamp.Before(&sorted[b].CreationTimestamp)
	})
	var lines []string
	for i := range sorted {
		pod := &sorted[i]
		logBody, err := kc.pods.ReadPodLog(ctx, cfg.Namespace, pod.Name, &corev1.PodLogOptions{})
		if err != nil {
			lines = append(lines, mustJSONLine(map[string]interface{}{
				"pod": pod.Name, "level": "ERROR", "logger": "relayforge.ui",
				"msg": fmt.Sprintf("лог недоступен: HTTP %d", k8sErrorStatus(err)),
			}))
			continue
		}
		for _, rawLine := range strings.Split(string(logBody), "\n") {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			var rec map[string]interface{}
			if json.Unmarshal([]byte(line), &rec) == nil && rec != nil {
				out := map[string]interface{}{"pod": pod.Name}
				for k, v := range rec {
					out[k] = v
				}
				lines = append(lines, mustJSONLine(out))
			} else {
				lines = append(lines, mustJSONLine(map[string]interface{}{"pod": pod.Name, "level": "RAW", "line": line}))
			}
		}
	}
	text := strings.Join(lines, "\n")
	truncated := len(text) > 200_000
	if truncated {
		text = text[len(text)-200_000:]
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"job": jobName, "logs": text, "truncated": truncated})
}

// k8sErrorStatus extracts the HTTP status from a k8s client error (404 in
// tests), defaulting to 0.
func k8sErrorStatus(err error) int {
	return mustK8sError(err)
}

func mustJSONLine(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"pod":"","level":"ERROR","logger":"relayforge.ui","msg":"marshal error"}`
	}
	return string(b)
}