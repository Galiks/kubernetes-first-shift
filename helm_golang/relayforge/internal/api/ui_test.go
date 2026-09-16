package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUIPageServed(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodGet, "/ui", "", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "Начать") || !strings.Contains(rec.Body.String(), "RelayForge") {
		t.Fatalf("ui page missing expected markers")
	}
}

func TestRootRedirectsToUI(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodGet, "/", "", "", nil)
	if rec.Code != http.StatusTemporaryRedirect && rec.Code != http.StatusFound && rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/ui" {
		t.Fatalf("location = %q, want /ui", loc)
	}
}

func TestUIConfig(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodGet, "/ui/api/config", "", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var data map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &data)
	if data["release"] != "relay-test" {
		t.Fatalf("release = %v", data["release"])
	}
	dests, ok := data["destinations"].([]interface{})
	if !ok || len(dests) != 1 || dests[0] != "test" {
		t.Fatalf("destinations = %v", data["destinations"])
	}
	// extended contract
	if data["sink_url"] != "http://relay-test-relayforge-test-sink:8080" {
		t.Fatalf("sink_url = %v", data["sink_url"])
	}
	modes, _ := data["sink_modes"].([]interface{})
	want := []string{"normal", "fail-first", "accept-and-drop", "reject", "slow"}
	if len(modes) != len(want) {
		t.Fatalf("sink_modes = %v", modes)
	}
	for i, m := range modes {
		if m != want[i] {
			t.Fatalf("sink_modes[%d] = %v", i, m)
		}
	}
}

func TestUIStatsShape(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodGet, "/ui/api/stats", "", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var data map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &data)
	for _, k := range []string{"requests_total", "active_jobs", "jobs_created_total"} {
		if _, ok := data[k]; !ok {
			t.Fatalf("stats missing key %q: %v", k, data)
		}
	}
}

func TestUIListRequiresK8s(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodGet, "/ui/api/deliveries", "", "", nil)
	a.apiErr(rec, 503, "UI_UNAVAILABLE")
}

func TestUICreateRequiresK8s(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodPost, "/ui/api/deliveries", "", `{}`, nil)
	a.apiErr(rec, 503, "UI_UNAVAILABLE")
}

func TestUIListWithEmptyK8s(t *testing.T) {
	h := newHarness(t)
	// One delivery job in the API namespace (config: "default").
	job := mkDeliveryJobNS("default", "relay-test-job1", "h", "d1")
	h.addJob(job)
	a := newTestApp(t, false)
	a.withK8s(h)
	rec := a.do(http.MethodGet, "/ui/api/deliveries", "", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var data map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &data)
	if data["count"] != float64(1) {
		t.Fatalf("count = %v, want 1", data["count"])
	}
}

func TestUISinkStateWithoutControlToken(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodGet, "/ui/api/sink/state", "", "", nil)
	a.apiErr(rec, 503, "SINK_CONTROL_UNAVAILABLE")
}

func TestUISinkModeInvalid(t *testing.T) {
	a := newTestApp(t, false)
	req := httptest.NewRequest(http.MethodPost, a.srv.URL+"/ui/api/sink/mode", strings.NewReader(`{"mode":"unknown"}`))
	req.Host = strings.TrimPrefix(a.srv.URL, "http://")
	rec := httptest.NewRecorder()
	buildRouter(a.st).ServeHTTP(rec, req)
	if rec.Code != 422 {
		t.Fatalf("status = %d", rec.Code)
	}
	var parsed map[string]map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	if parsed["error"]["code"] != "INVALID_MODE" {
		t.Fatalf("code = %v", parsed["error"])
	}
}

func TestUITestsRequireK8s(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodPost, "/ui/api/tests/run", "", "", nil)
	a.apiErr(rec, 503, "UI_UNAVAILABLE")
}

func TestUITestsHistoryEmpty(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodGet, "/ui/api/tests", "", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var data map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &data)
	if tests, ok := data["tests"].([]interface{}); !ok || len(tests) != 0 {
		t.Fatalf("tests = %v", data["tests"])
	}
}

func TestUILogsRequireK8s(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodGet, "/ui/api/deliveries/some-id/logs", "", "", nil)
	a.apiErr(rec, 503, "UI_UNAVAILABLE")
}

func TestUILogsPodNameInsideJSON(t *testing.T) {
	h := newHarness(t)
	job := mkDeliveryJobNS("default", "job-x", "hash", "d1")
	h.addJob(job)
	h.addPod(mkPodNS("default", "relay-a-abc123xyz", "job-x"))
	h.addPod(mkPodNS("default", "relay-a-def456uvw", "job-x"))
	h.setLog("relay-a-abc123xyz", `{"ts":"2026-09-14T00:00:00Z","level":"INFO","logger":"httpx","msg":"HTTP Request: POST http://sink:8080/events \"HTTP/1.1 503 Service Unavailable\""}
plain text line
`)
	// relay-a-def456uvw: no log configured -> ReadPodLog returns 404 -> ERROR record.

	a := newTestApp(t, false)
	a.withK8s(h)
	rec := a.do(http.MethodGet, "/ui/api/deliveries/d1/logs", "", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var data map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &data)
	if data["job"] != "job-x" {
		t.Fatalf("job = %v", data["job"])
	}
	logs, _ := data["logs"].(string)
	if strings.Contains(logs, "---") {
		t.Fatalf("logs must not contain pod separators: %s", logs)
	}
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 log records, got %d (%s)", len(lines), logs)
	}
	var r0, r1, r2 map[string]interface{}
	_ = json.Unmarshal([]byte(lines[0]), &r0)
	_ = json.Unmarshal([]byte(lines[1]), &r1)
	_ = json.Unmarshal([]byte(lines[2]), &r2)
	if r0["pod"] != "relay-a-abc123xyz" || r0["logger"] != "httpx" {
		t.Fatalf("rec0 = %v", r0)
	}
	if !strings.Contains(r0["msg"].(string), "503 Service Unavailable") {
		t.Fatalf("rec0 msg = %v", r0["msg"])
	}
	if r1["pod"] != "relay-a-abc123xyz" || r1["level"] != "RAW" || r1["line"] != "plain text line" {
		t.Fatalf("rec1 = %v", r1)
	}
	if r2["pod"] != "relay-a-def456uvw" || r2["level"] != "ERROR" {
		t.Fatalf("rec2 = %v", r2)
	}
	if !strings.Contains(r2["msg"].(string), "404") {
		t.Fatalf("rec2 msg = %v", r2["msg"])
	}
}

func TestUILogsMissingDelivery404(t *testing.T) {
	h := newHarness(t)
	a := newTestApp(t, false)
	a.withK8s(h)
	rec := a.do(http.MethodGet, "/ui/api/deliveries/ghost/logs", "", "", nil)
	a.apiErr(rec, 404, "DELIVERY_NOT_FOUND")
}

// --- UI submit (Начать) happy path ---

func TestUISubmitDelivery(t *testing.T) {
	h := newHarness(t)
	a := newTestApp(t, false)
	a.withK8s(h)
	rec := a.do(http.MethodPost, "/ui/api/deliveries", "",
		`{"destination":"test","event_type":"ui.test","payload":{"source":"ui"}}`, nil)
	if rec.Code != 202 {
		t.Fatalf("submit status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if id, _ := resp["id"].(string); id == "" {
		t.Fatalf("submit id missing: %s", rec.Body.String())
	}
}