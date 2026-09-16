package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relayforge/internal/config"
)

type testApp struct {
	t   *testing.T
	srv *httptest.Server
	st  *State
}

// newTestApp builds an HTTP app against a freshly-arranged runtimeState.
func newTestApp(t *testing.T, withK8s bool) *testApp {
	t.Helper()
	cfg := &config.Config{
		Release:            "relay-test",
		Namespace:          "default",
		APIPort:            8080,
		SinkURL:            "http://relay-test-relayforge-test-sink:8080",
		ControlTokenSinkPath: "/nonexistent/control-token",
		WorkerTTL:          600,
		APIBackpressureActive: 10,
		APIBackpressureCreate: 2,
	}
	st := newState(cfg)
	st.setToken([]byte("valid-client-token"))
	st.setDestinations(map[string]config.Destination{
		"test": {URL: "http://relay-test-relayforge-test-sink:8080"},
	})
	runtimeState = st
	if withK8s {
		h := newHarness(t)
		st.setK8s(h.clients())
		reg := &JobRegistry{jobRecs: map[string]jobRecord{}, pending: map[string]float64{}}
		st.setJobRegistry(reg)
		st.setBackpressure(NewBackpressure(10, 2, reg))
	}
	srv := httptest.NewServer(buildRouter(st))
	t.Cleanup(srv.Close)
	return &testApp{t: t, srv: srv, st: st}
}

func (a *testApp) do(method, path, idemKey, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, a.srv.URL+path, strings.NewReader(body))
	req.Host = strings.TrimPrefix(a.srv.URL, "http://")
	req.Header.Set("Authorization", "Bearer valid-client-token")
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	if headers != nil {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	buildRouter(a.st).ServeHTTP(rec, req)
	return rec
}

func (a *testApp) apiErr(rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	a.t.Helper()
	if rec.Code != wantStatus {
		a.t.Fatalf("status = %d, want %d; body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	var parsed map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		a.t.Fatalf("invalid json body: %v (%s)", err, rec.Body.String())
	}
	if parsed["error"] == nil || parsed["error"]["code"] != wantCode {
		a.t.Fatalf("error code = %+v, want %q; body=%s", parsed["error"], wantCode, rec.Body.String())
	}
}

func (a *testApp) withK8s(harness *testHarness) {
	a.t.Helper()
	a.st.setK8s(harness.clients())
}

// --- validation error-code table (ported from validation-errors-test.py) ---

func TestValidationUnknownField422(t *testing.T) {
	a := newTestApp(t, true)
	rec := a.do(http.MethodPost, "/v1/deliveries", "test:valid:key:1",
		`{"destination":"test","event_type":"test.ping","payload":{},"bogus":1}`, nil)
	a.apiErr(rec, 422, "UNKNOWN_FIELDS")
}

func TestValidationMissingIdempotencyKey422(t *testing.T) {
	a := newTestApp(t, true)
	rec := a.do(http.MethodPost, "/v1/deliveries", "",
		`{"destination":"test","event_type":"test.ping","payload":{}}`, nil)
	a.apiErr(rec, 422, "INVALID_IDEMPOTENCY_KEY")
}

func TestValidationDuplicateJSONKeys400(t *testing.T) {
	a := newTestApp(t, true)
	rec := a.do(http.MethodPost, "/v1/deliveries", "test:valid:key:2",
		`{"destination":"test","event_type":"a.b.c","payload":{},"payload":{"x":1}}`, nil)
	a.apiErr(rec, 400, "INVALID_JSON")
}

func TestValidationBodyTooLarge413(t *testing.T) {
	a := newTestApp(t, true)
	pad := strings.Repeat("x", 16*1024)
	body := fmt.Sprintf(`{"destination":"test","event_type":"test.ping","payload":{"pad":%q}}`, pad)
	rec := a.do(http.MethodPost, "/v1/deliveries", "test:valid:key:3", body, nil)
	a.apiErr(rec, 413, "BODY_TOO_LARGE")
}

// --- auth (ported from auth-test.py) ---

func TestAuthMissingToken401(t *testing.T) {
	a := newTestApp(t, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, a.srv.URL+"/v1/deliveries", strings.NewReader(`{}`))
	req.Host = strings.TrimPrefix(a.srv.URL, "http://")
	buildRouter(a.st).ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401 (no auth header)", rec.Code)
	}
}

func TestAuthWrongToken401WithoutK8s(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodPost, "/v1/deliveries", "", `{}`, map[string]string{
		"Authorization": "Bearer wrong",
	})
	a.apiErr(rec, 401, "UNAUTHORIZED")
}

func TestAuthMalformedHeader401(t *testing.T) {
	a := newTestApp(t, false)
	rec := a.do(http.MethodPost, "/v1/deliveries", "", `{}`, map[string]string{
		"Authorization": "Basic abc",
	})
	a.apiErr(rec, 401, "UNAUTHORIZED")
}

// --- happy path: create then get ---

func TestCreateThenGet(t *testing.T) {
	a := newTestApp(t, true)
	rec := a.do(http.MethodPost, "/v1/deliveries", "test:valid:key:9",
		`{"destination":"test","event_type":"test.ping","payload":{"n":1}}`, nil)
	if rec.Code != 202 {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	id, _ := resp["id"].(string)
	if id == "" {
		t.Fatalf("no id in create response: %s", rec.Body.String())
	}
	if resp["status"] != "queued" {
		t.Fatalf("create status field = %v", resp["status"])
	}

	// duplicate with same key -> 200 duplicate=true
	rec2 := a.do(http.MethodPost, "/v1/deliveries", "test:valid:key:9",
		`{"destination":"test","event_type":"test.ping","payload":{"n":1}}`, nil)
	if rec2.Code != 200 {
		t.Fatalf("duplicate status = %d, body=%s", rec2.Code, rec2.Body.String())
	}
	var resp2 map[string]interface{}
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if resp2["duplicate"] != true {
		t.Fatalf("duplicate flag = %v", resp2["duplicate"])
	}

	// GET by id (fake tracker returns all jobs; presence is what matters)
	rec3 := a.do(http.MethodGet, "/v1/deliveries/"+id, "", "", nil)
	if rec3.Code != 200 {
		t.Fatalf("get status = %d, body=%s", rec3.Code, rec3.Body.String())
	}
	var got map[string]interface{}
	_ = json.Unmarshal(rec3.Body.Bytes(), &got)
	if got["id"] != id {
		t.Fatalf("get id = %v, want %v", got["id"], id)
	}
}

// --- backpressure: active count at the limit -> 503 BACKPRESSURE ---

func TestBackpressure503(t *testing.T) {
	a := newTestApp(t, true)
	reg := a.st.jobRegistryRef()
	reg.mu.Lock()
	// 10 non-terminal jobs == maxActive(10); the next create must be denied.
	for i := 0; i < 10; i++ {
		reg.jobRecs[fmt.Sprintf("active-%d", i)] = jobRecord{terminal: false, created: time.Now()}
	}
	reg.mu.Unlock()
	rec := a.do(http.MethodPost, "/v1/deliveries", "test:valid:key:bp",
		`{"destination":"test","event_type":"test.ping","payload":{}}`, nil)
	a.apiErr(rec, 503, "BACKPRESSURE")
}