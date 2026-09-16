package testsink

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// verifyDigest checks a bare hex digest against the recomputed HMAC in
// constant time.
func verifyDigest(timestamp, deliveryID string, body, key []byte, sigHex string) bool {
	if len(sigHex) != 64 {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write([]byte(deliveryID))
	mac.Write([]byte("\n"))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sigHex))
}
