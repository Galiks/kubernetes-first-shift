// Package testsink ports relayforge/test_sink to Go: a blocking HTTP server
// that verifies signed deliveries, records receipts, and exposes test modes.
package testsink

import (
	"fmt"
	"net/http"
	"time"
)

// VerifyError carries the 401 API error contract (status + code) surfaced by
// the verifier, mirroring relayforge.worker.verifier + api.errors.ApiError.
type VerifyError struct {
	Status int
	Code   string
	Msg    string
}

func (e *VerifyError) Error() string { return e.Msg }

func newVerifyError(code, msg string) *VerifyError {
	return &VerifyError{Status: http.StatusUnauthorized, Code: code, Msg: msg}
}

// clockSkewWindow is the max acceptable |now-ts| in seconds (5 minutes).
const clockSkewWindow = 300 * time.Second

// VerifyRequest validates the X-Relay-* headers against the secret key and
// recomputes the HMAC over `ts\n+delivery_id\n+body`. On success it returns the
// verified delivery id; on failure it returns a *VerifyError. Mirrors
// test_sink/verifier.py.verify_request.
func VerifyRequest(hdr http.Header, body, key []byte) (string, error) {
	deliveryID := hdr.Get("X-Relay-Id")
	timestamp := hdr.Get("X-Relay-Timestamp")
	signature := hdr.Get("X-Relay-Signature")
	if deliveryID == "" || timestamp == "" || signature == "" {
		return "", newVerifyError("MISSING_HEADERS", "required headers missing")
	}

	ts, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return "", newVerifyError("INVALID_TIMESTAMP", fmt.Sprintf("invalid timestamp: %v", err))
	}

	now := time.Now().UTC()
	if d := now.Sub(ts); d > clockSkewWindow || d < -clockSkewWindow {
		return "", newVerifyError("CLOCK_SKEW", "timestamp too old or too new")
	}

	if !verifyHMAC(timestamp, deliveryID, body, key, signature) {
		return "", newVerifyError("INVALID_SIGNATURE", "HMAC mismatch")
	}

	return deliveryID, nil
}

// verifyHMAC recomputes the expected signature and compares const-time against
// the `v1=<sig>` header. Identical logic to worker.Sign.
func verifyHMAC(timestamp, deliveryID string, body, key []byte, signature string) bool {
	if len(signature) < 3 || signature[:3] != "v1=" {
		return false
	}
	return verifyDigest(timestamp, deliveryID, body, key, signature[3:])
}
