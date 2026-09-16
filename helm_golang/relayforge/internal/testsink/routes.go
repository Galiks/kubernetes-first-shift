package testsink

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// newRouter builds the HTTP handler tree bound to a SinkState (injectable for
// tests; Run uses the shared State).
func newRouter(state *SinkState) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]interface{}{"status": "ok"})
	})

	mux.HandleFunc("POST /events", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, 400, map[string]interface{}{"code": "BAD_BODY", "message": "read body"})
			return
		}
		deliveryID, err := VerifyRequest(r.Header, body, state.VerificationKey)
		if err != nil {
			if ve, ok := err.(*VerifyError); ok {
				writeJSON(w, ve.Status, map[string]interface{}{"code": ve.Code, "message": ve.Msg})
				return
			}
			writeJSON(w, 401, map[string]interface{}{"code": "INVALID", "message": err.Error()})
			return
		}

		state.Receipts.RecordAttempt(deliveryID)
		action := state.Modes.CheckMode(deliveryID)

		switch action {
		case "reject":
			writeJSON(w, 401, map[string]interface{}{"code": "REJECTED", "message": "rejected by mode"})
			return
		case "slow":
			time.Sleep(30 * time.Second)
			// fall through below to accept path after the sleep
		case "drop":
			// accept-and-drop: apply, then abort the connection with no reply.
			state.Receipts.Apply(deliveryID, body,
				r.Header.Get("X-Relay-Signature"), r.Header.Get("X-Relay-Timestamp"))
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close()
				}
			}
			return
		case "fail":
			writeJSON(w, 503, map[string]interface{}{"code": "FAIL", "message": "forced failure"})
			return
		}

		if state.Receipts.IsApplied(deliveryID) {
			// Already applied: 204, apply exactly once.
			w.WriteHeader(204)
			return
		}
		state.Receipts.Apply(deliveryID, body,
			r.Header.Get("X-Relay-Signature"), r.Header.Get("X-Relay-Timestamp"))
		w.WriteHeader(204)
	})

	mux.HandleFunc("GET /received/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		writeJSON(w, 200, map[string]interface{}{
			"applied":  state.Receipts.IsApplied(id),
			"attempts": state.Receipts.Attempts(id),
		})
	})

	mux.HandleFunc("GET /control/state", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, state) {
			unauthorized(w)
			return
		}
		writeJSON(w, 200, map[string]interface{}{
			"mode":           state.Modes.Mode(),
			"fail_first_n":   state.Modes.FailFirstN(),
			"receipts_count": state.Receipts.Count(),
		})
	})

	mux.HandleFunc("POST /control/mode", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, state) {
			unauthorized(w)
			return
		}
		var req struct {
			Mode string `json:"mode"`
			N    int    `json:"n"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]interface{}{"code": "BAD_BODY", "message": err.Error()})
			return
		}
		if req.N == 0 {
			req.N = 1
		}
		state.Modes.SetMode(req.Mode, req.N)
		writeJSON(w, 200, map[string]interface{}{"mode": req.Mode})
	})

	mux.HandleFunc("POST /control/reset", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, state) {
			unauthorized(w)
			return
		}
		state.Receipts.Clear()
		state.Modes.Reset()
		writeJSON(w, 200, map[string]interface{}{"status": "ok"})
	})

	return mux
}

// authorized gates control endpoints on a bearer ControlToken (const-time),
// matching the Python _verify_control helper.
func authorized(r *http.Request, state *SinkState) bool {
	auth := r.Header.Get("Authorization")
	if len(auth) < 7 || auth[:7] != "Bearer " || state.ControlToken == nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(auth[7:]), state.ControlToken) == 1
}

func unauthorized(w http.ResponseWriter) {
	writeJSON(w, 401, map[string]interface{}{
		"code": "UNAUTHORIZED", "message": "invalid or missing control token",
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
