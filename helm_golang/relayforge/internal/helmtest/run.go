// Package helmtest ports relayforge/helm_test/run.py: it drives a full
// end-to-end test delivery through the API and asserts idempotency, terminal
// status and test-sink cross-check.
package helmtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"relayforge/internal/config"
	"relayforge/internal/logging"
	"relayforge/internal/secrets"
)

// hostnameOrDefault mirrors helm_test/run.py's os.environ.get("HOSTNAME", "unknown").
func hostnameOrDefault() string {
	if h := os.Getenv("HOSTNAME"); h != "" {
		return h
	}
	return "unknown"
}

// Run is the frozen helm-test entrypoint. It returns a non-nil error on any
// failure (callers map that to a non-zero exit).
func Run(cfg *config.Config) error {
	logging.Setup()

	apiURL := os.Getenv("RELAYFORGE_API_URL")
	destination := os.Getenv("RELAYFORGE_DESTINATION")
	if apiURL == "" {
		return fmt.Errorf("RELAYFORGE_API_URL is not set")
	}
	clientToken, err := secrets.Read(secrets.ClientTokenPath)
	if err != nil {
		return fmt.Errorf("read client token: %w", err)
	}
	controlToken, err := secrets.Read(secrets.ControlTokenPath)
	if err != nil {
		return fmt.Errorf("read control token: %w", err)
	}

	idemKey := fmt.Sprintf("helm-test:%s:v1", hostnameOrDefault())
	body := map[string]interface{}{
		"destination": destination,
		"event_type":  "test.ping",
		"payload":     map[string]interface{}{"x": float64(1)},
	}

	client := &http.Client{Timeout: 30 * time.Second}
	bearer := "Bearer " + string(clientToken)

	// 1. Submit the delivery.
	deliveryID, err := createDelivery(client, apiURL, bearer, idemKey, body)
	if err != nil {
		return err
	}
	logInfo("created", map[string]any{"delivery_id": deliveryID})

	// 2. Repeat with the same idempotency key -> duplicate confirmed.
	dupID, duplicate, err := resubmit(client, apiURL, bearer, idemKey, body)
	if err != nil {
		return err
	}
	if !duplicate {
		return fmt.Errorf("expected duplicate=true on resubmit")
	}
	if dupID != deliveryID {
		return fmt.Errorf("duplicate delivery id %q != original %q", dupID, deliveryID)
	}
	logInfo("duplicate confirmed", nil)

	// 3. Wait for a terminal status (succeeded/failed).
	status, err := waitTerminal(client, apiURL, bearer, deliveryID, 120*time.Second)
	if err != nil {
		return err
	}
	if status != "succeeded" {
		logError("delivery failed", map[string]any{"status": status})
		return fmt.Errorf("delivery terminal status = %q, want succeeded", status)
	}

	// 4. Cross-check the test-sink receipt.
	if !sinkApplied(client, cfg.SinkURL, controlToken, deliveryID) {
		return fmt.Errorf("delivery %q not applied in test-sink", deliveryID)
	}

	logInfo("PASS", nil)
	return nil
}

func createDelivery(client *http.Client, apiURL, bearer, idemKey string, body map[string]interface{}) (string, error) {
	resp, err := postJSON(client, apiURL+"/v1/deliveries", bearer, idemKey, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("create delivery: status %d", resp.StatusCode)
	}
	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	id, _ := out["id"].(string)
	if id == "" {
		return "", fmt.Errorf("create delivery: no id in response")
	}
	return id, nil
}

func resubmit(client *http.Client, apiURL, bearer, idemKey string, body map[string]interface{}) (string, bool, error) {
	resp, err := postJSON(client, apiURL+"/v1/deliveries", bearer, idemKey, body)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", false, fmt.Errorf("resubmit: expected 200, got %d", resp.StatusCode)
	}
	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", false, err
	}
	id, _ := out["id"].(string)
	duplicate, _ := out["duplicate"].(bool)
	return id, duplicate, nil
}

func waitTerminal(client *http.Client, apiURL, bearer, deliveryID string, deadline time.Duration) (string, error) {
	until := time.Now().Add(deadline)
	for time.Now().Before(until) {
		req, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/deliveries/"+deliveryID, nil)
		req.Header.Set("Authorization", bearer)
		resp, err := client.Do(req)
		if err != nil {
			logError("status poll error", map[string]any{"error": err.Error()})
			time.Sleep(time.Second)
			continue
		}
		var out map[string]interface{}
		derr := json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || derr != nil {
			time.Sleep(time.Second)
			continue
		}
		status, _ := out["status"].(string)
		if status == "succeeded" || status == "failed" {
			return status, nil
		}
		time.Sleep(time.Second)
	}
	logError("timeout waiting for terminal status", nil)
	return "", fmt.Errorf("timeout waiting for terminal status")
}

func sinkApplied(client *http.Client, sinkURL string, controlToken []byte, deliveryID string) bool {
	req, _ := http.NewRequest(http.MethodGet, sinkURL+"/received/"+deliveryID, nil)
	req.Header.Set("Authorization", "Bearer "+string(controlToken))
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false
	}
	applied, _ := out["applied"].(bool)
	return applied
}

func postJSON(client *http.Client, url, bearer, idemKey string, body map[string]interface{}) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Idempotency-Key", idemKey)
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}
