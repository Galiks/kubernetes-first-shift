package worker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDeliverRoundtrip drives worker.Deliver against an httptest sink that
// recomputes the HMAC using the exact framing, then asserts the bytes.
func TestDeliverRoundtrip(t *testing.T) {
	ts := "2026-08-06T12:00:00Z"
	id := "relay-a-abc123"
	body := []byte(`{"a":1}`)
	sig := "v1=" + Sign(ts, id, body, []byte(testKey))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/events" {
			t.Errorf("path = %s, want /events", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		if got := r.Header.Get("X-Relay-Id"); got != id {
			t.Errorf("x-relay-id = %q", got)
		}
		if got := r.Header.Get("X-Relay-Timestamp"); got != ts {
			t.Errorf("x-relay-timestamp = %q", got)
		}
		if got := r.Header.Get("X-Relay-Signature"); got != sig {
			t.Errorf("x-relay-signature = %q", got)
		}
		rb, _ := io.ReadAll(r.Body)
		if !Verify(ts, id, rb, []byte(testKey), r.Header.Get("X-Relay-Signature")) {
			t.Error("verifier rejected worker signature")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewClient(2*time.Second, 5*time.Second)
	headers := map[string]string{
		"X-Relay-Id":        id,
		"X-Relay-Timestamp": ts,
		"X-Relay-Signature": sig,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, err := Deliver(ctx, client, srv.URL+"/events", body, headers)
	if err != nil {
		t.Fatalf("deliver error: %v", err)
	}
	if status != 204 {
		t.Fatalf("status = %d, want 204", status)
	}
}

// TestDeliverNetworkError ensures transport failures surface as errors.
func TestDeliverNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // connection refused afterwards

	client := NewClient(300*time.Millisecond, 300*time.Millisecond)
	ctx := context.Background()
	_, err := Deliver(ctx, client, url, []byte(`{}`), map[string]string{})
	if err == nil {
		t.Fatal("expected a network error, got nil")
	}
}
