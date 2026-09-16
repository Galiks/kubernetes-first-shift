package testsink

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"
)

const testKey = "test-signing-key-0123456789abcdef"

// hmacHex mirrors worker.Sign framing.
func hmacHex(ts, id string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(testKey))
	mac.Write([]byte(ts))
	mac.Write([]byte("\n"))
	mac.Write([]byte(id))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// signedHeaders builds a valid header set for the given payload using the same
// HMAC framing as worker.Sign.
func signedHeaders(id string, payload []byte) http.Header {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	sig := hmacHex(timestamp, id, payload)
	h := http.Header{}
	h.Set("X-Relay-Id", id)
	h.Set("X-Relay-Timestamp", timestamp)
	h.Set("X-Relay-Signature", "v1="+sig)
	return h
}

func TestVerifyRoundtrip(t *testing.T) {
	id := "relay-a-abc123"
	body := []byte(`{"delivery_id":"relay-a-abc123","event_type":"invoice.created","payload":{"amount":1}}`)
	got, err := VerifyRequest(signedHeaders(id, body), body, []byte(testKey))
	if err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if got != id {
		t.Fatalf("delivery id = %q, want %q", got, id)
	}
}

func TestVerifyRejectsClockSkew(t *testing.T) {
	id := "relay-a-abc123"
	body := []byte(`{}`)
	ts := time.Now().UTC().Add(-10 * time.Minute).Format("2006-01-02T15:04:05Z")
	h := http.Header{}
	h.Set("X-Relay-Id", id)
	h.Set("X-Relay-Timestamp", ts)
	h.Set("X-Relay-Signature", "v1="+hmacHex(ts, id, body))
	_, err := VerifyRequest(h, body, []byte(testKey))
	ve, ok := err.(*VerifyError)
	if !ok || ve.Code != "CLOCK_SKEW" {
		t.Fatalf("expected CLOCK_SKEW error, got %v", err)
	}
}

func TestVerifyRejectsBadSignature(t *testing.T) {
	id := "relay-a-abc123"
	body := []byte(`{}`)
	ts := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	h := http.Header{}
	h.Set("X-Relay-Id", id)
	h.Set("X-Relay-Timestamp", ts)
	h.Set("X-Relay-Signature", "v1=0000000000000000000000000000000000000000000000000000000000000000")
	_, err := VerifyRequest(h, body, []byte(testKey))
	if ve, ok := err.(*VerifyError); !ok || ve.Code != "INVALID_SIGNATURE" {
		t.Fatalf("expected INVALID_SIGNATURE, got %v", err)
	}
}

func TestVerifyRejectsMissingHeaders(t *testing.T) {
	_, err := VerifyRequest(http.Header{}, []byte(`{}`), []byte(testKey))
	if ve, ok := err.(*VerifyError); !ok || ve.Code != "MISSING_HEADERS" {
		t.Fatalf("expected MISSING_HEADERS, got %v", err)
	}
}
