package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// livez mirrors probes.livez: plain process liveness, no Kubernetes access.
func handleLivez(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz mirrors probes.readyz:
//   - not ready                   -> 503 {"status":"shutting_down"}
//   - no destinations             -> 503 {"status":"no_destinations"}
//   - k8s client uninitialized    -> 503 {"status":"k8s_uninitialized"}
//   - k8s list failure            -> 503 {"status":"k8s_unhealthy","error":...}
func handleReadyz(w http.ResponseWriter, r *http.Request, st *State) {
	if !st.isReady() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "shutting_down"})
		return
	}
	if !st.hasDestinations() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "no_destinations"})
		return
	}
	kc := st.k8sClients()
	if kc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "k8s_uninitialized"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := kc.batch.BatchV1().Jobs(st.configRef().Namespace).List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		msg := err.Error()
		if len(msg) > 100 {
			msg = msg[:100]
		}
		slog.Warn("readiness probe: k8s list failed", "err", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "k8s_unhealthy", "error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}