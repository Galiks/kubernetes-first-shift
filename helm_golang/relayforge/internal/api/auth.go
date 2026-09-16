package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// verifyToken mirrors relayforge/api/auth.py: constant-time bearer compare
// against the mounted client token; 401 before any Kubernetes access.
func verifyToken(w http.ResponseWriter, r *http.Request, st *State) error {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return NewApiError(http.StatusUnauthorized, "UNAUTHORIZED", "missing or malformed Authorization header", nil)
	}
	provided := []byte(auth[len("Bearer "):])
	expected := st.token()
	if subtle.ConstantTimeCompare(provided, expected) != 1 {
		return NewApiError(http.StatusUnauthorized, "UNAUTHORIZED", "invalid client token", nil)
	}
	return nil
}