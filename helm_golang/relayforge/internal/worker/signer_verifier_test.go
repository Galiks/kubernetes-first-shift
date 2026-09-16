package worker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// Known vectors ported verbatim from tests/signer-verifier-test.py.
const testKey = "test-signing-key-0123456789abcdef"

func expectSign(t *testing.T, ts, id string, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(testKey))
	mac.Write([]byte(ts))
	mac.Write([]byte("\n"))
	mac.Write([]byte(id))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestExactHMACFraming(t *testing.T) {
	ts := "2026-08-06T12:00:00Z"
	id := "relay-a-abc123"
	body := []byte(`{"a":1}`)
	got := Sign(ts, id, body, []byte(testKey))
	if got != expectSign(t, ts, id, body) {
		t.Fatalf("sign mismatch:\n  got  %s\n  want %s", got, expectSign(t, ts, id, body))
	}
}

func TestVerifyRoundtrip(t *testing.T) {
	ts := "2026-08-06T12:00:00Z"
	id := "relay-a-abc123"
	body := []byte(`{"a":1}`)
	sig := "v1=" + Sign(ts, id, body, []byte(testKey))
	if !Verify(ts, id, body, []byte(testKey), sig) {
		t.Fatal("valid signature rejected")
	}
	if Verify(ts, id, append(body, 'x'), []byte(testKey), sig) {
		t.Fatal("tampered body accepted")
	}
	if Verify(ts, id, body, []byte(testKey), "v1=deadbeef") {
		t.Fatal("wrong signature accepted")
	}
}
