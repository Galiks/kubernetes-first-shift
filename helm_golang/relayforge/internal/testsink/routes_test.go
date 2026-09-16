package testsink

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestState() *SinkState {
	st := &SinkState{
		VerificationKey: []byte(testKey),
		ControlToken:    []byte("control-secret"),
		Modes:           NewModeController(),
		Receipts:        NewReceiptStore(),
	}
	return st
}

func TestPostEventsAcceptsAndApplies(t *testing.T) {
	st := newTestState()
	router := newRouter(st)
	id := "relay-a-abc123"
	body := []byte(`{"delivery_id":"relay-a-abc123","event_type":"x","payload":{"a":1}}`)

	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
	req.Header = signedHeaders(id, body)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if !st.Receipts.IsApplied(id) {
		t.Fatal("delivery not applied")
	}
	if st.Receipts.Attempts(id) != 1 {
		t.Fatalf("attempts = %d, want 1", st.Receipts.Attempts(id))
	}
}

func TestPostEventsIdempotent(t *testing.T) {
	st := newTestState()
	router := newRouter(st)
	id := "relay-a-abc123"
	body := []byte(`{"delivery_id":"relay-a-abc123","event_type":"x","payload":{"a":1}}`)

	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
		req.Header = signedHeaders(id, body)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	if do() != 204 {
		t.Fatal("first request failed")
	}
	if do() != 204 {
		t.Fatal("retry should also return 204")
	}
	if st.Receipts.Attempts(id) != 2 {
		t.Fatalf("attempts = %d, want 2", st.Receipts.Attempts(id))
	}
}

func TestPostEventsRejectsBadSignature(t *testing.T) {
	st := newTestState()
	router := newRouter(st)
	id := "relay-a-abc123"
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
	h := signedHeaders(id, body)
	h.Set("X-Relay-Signature", "v1=0000000000000000000000000000000000000000000000000000000000000000")
	req.Header = h
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestReceivedEndpoint(t *testing.T) {
	st := newTestState()
	router := newRouter(st)
	id := "relay-a-abc123"
	body := []byte(`{"delivery_id":"relay-a-abc123"}`)
	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
	req.Header = signedHeaders(id, body)
	router.ServeHTTP(httptest.NewRecorder(), req)

	greq := httptest.NewRequest(http.MethodGet, "/received/"+id, nil)
	grec := httptest.NewRecorder()
	router.ServeHTTP(grec, greq)
	if grec.Code != 200 {
		t.Fatalf("status = %d", grec.Code)
	}
	var out map[string]interface{}
	if err := json.NewDecoder(grec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["applied"] != true || out["attempts"] != float64(1) {
		t.Fatalf("unexpected body: %v", out)
	}
}

func TestControlRequiresToken(t *testing.T) {
	st := newTestState()
	router := newRouter(st)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/control/state", nil))
	if rec.Code != 401 {
		t.Fatalf("no-token status = %d, want 401", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/control/state", nil)
	req.Header.Set("Authorization", "Bearer control-secret")
	router.ServeHTTP(rec2, req)
	if rec2.Code != 200 {
		t.Fatalf("with-token status = %d, want 200", rec2.Code)
	}
}

func TestControlDropModeAppliesThenAborts(t *testing.T) {
	st := newTestState()
	router := newRouter(st)
	id := "relay-a-abc123"
	body := []byte(`{"delivery_id":"relay-a-abc123"}`)

	// set reject-off / drop mode
	modReq := httptest.NewRequest(http.MethodPost, "/control/mode",
		bytes.NewReader([]byte(`{"mode":"accept-and-drop","n":1}`)))
	modReq.Header.Set("Authorization", "Bearer control-secret")
	router.ServeHTTP(httptest.NewRecorder(), modReq)

	// Under httptest the hijacker path closes the connection; assert applied.
	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
	req.Header = signedHeaders(id, body)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	_ = rec
	if !st.Receipts.IsApplied(id) {
		t.Fatal("drop mode should still apply the receipt")
	}
}
