// Package worker ports relayforge/worker to Go: a single-shot delivery process
// that signs JSON via HMAC-SHA256 and exits with the agreed exit-code contract.
//
// Exit codes (must match Python worker/run.py):
//
//	SUCCESS=0, TRANSIENT=11, PERMANENT=12, NETWORK=13.
package worker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Exit codes mirroring relayforge/worker/run.py.
const (
	ExitSuccess   = 0
	ExitTransient = 11
	ExitPermanent = 12
	ExitNetwork   = 13
)

// Sign computes the HMAC-SHA256 hex digest over
// `timestamp + "\n" + delivery_id + "\n" + body`, matching the Python signer.
func Sign(timestamp, deliveryID string, body, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write([]byte(deliveryID))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify recomputes the expected signature and compares it const-time against
// the provided `v1=<sig>` header value, mirroring Python hmac.compare_digest.
func Verify(timestamp, deliveryID string, body, key []byte, signature string) bool {
	expected := "v1=" + Sign(timestamp, deliveryID, body, key)
	return hmac.Equal([]byte(expected), []byte(signature))
}
