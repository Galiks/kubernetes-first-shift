// Package config mirrors relayforge/config.py: runtime configuration read from
// environment variables, plus the destinations loader.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type Destination struct {
	URL string `json:"url"`
}

type Config struct {
	Release      string
	Namespace    string
	APIPort      int
	TestSinkPort int
	SinkURL      string

	ControlTokenSinkPath string
	DestinationsPath     string

	ImageRepository    string
	ImageDigest        string
	SigningSecretName  string
	SigningSecretKey   string
	DestinationsConfig string
	WorkerSA           string

	WorkerBackoffLimit    int
	WorkerActiveDeadline  int
	WorkerTTL             int
	WorkerPermanentExit   int
	WorkerConnectTimeout  float64
	WorkerReadTimeout     float64
	SecretRevision        string
	APIBackpressureActive int
	APIBackpressureCreate int

	TestFaultCreateTimeout bool
}

func Load() (*Config, error) {
	c := &Config{}
	var err error
	get := func(k, def string) string {
		if v, ok := os.LookupEnv(k); ok {
			return v
		}
		return def
	}
	getInt := func(k string, def int) int {
		if v, ok := os.LookupEnv(k); ok {
			if n, e := strconv.Atoi(v); e == nil {
				return n
			}
			return def
		}
		return def
	}
	getFloat := func(k string, def float64) float64 {
		if v, ok := os.LookupEnv(k); ok {
			if f, e := strconv.ParseFloat(v, 64); e == nil {
				return f
			}
			return def
		}
		return def
	}
	c.Release, err = required("RELAYFORGE_RELEASE")
	if err != nil {
		return nil, err
	}
	c.Namespace, err = required("RELAYFORGE_NAMESPACE")
	if err != nil {
		return nil, err
	}
	c.APIPort = getInt("RELAYFORGE_API_PORT", 8080)
	c.TestSinkPort = getInt("RELAYFORGE_TEST_SINK_PORT", 8080)
	c.SinkURL = get("RELAYFORGE_SINK_URL", "http://"+c.Release+"-relayforge-test-sink:8080")
	c.ControlTokenSinkPath = get("RELAYFORGE_CONTROL_TOKEN_PATH", "/etc/relayforge/secrets-control/control-token")
	c.DestinationsPath = get("RELAYFORGE_DESTINATIONS_PATH", "/etc/relayforge/config/destinations.json")

	c.ImageRepository = get("RELAYFORGE_IMAGE_REPOSITORY", "localhost:5050/relayforge")
	c.ImageDigest = get("RELAYFORGE_IMAGE_DIGEST", "")
	c.SigningSecretName = get("RELAYFORGE_SIGNING_SECRET_NAME", "")
	c.SigningSecretKey = get("RELAYFORGE_SIGNING_SECRET_KEY", "signing-key")
	c.DestinationsConfig = get("RELAYFORGE_DESTINATIONS_CONFIGMAP", c.Release+"-relayforge-destinations")
	c.WorkerSA = get("RELAYFORGE_WORKER_SERVICE_ACCOUNT", c.Release+"-relayforge-worker")

	c.WorkerBackoffLimit = getInt("RELAYFORGE_WORKER_BACKOFF_LIMIT", 3)
	c.WorkerActiveDeadline = getInt("RELAYFORGE_WORKER_ACTIVE_DEADLINE", 300)
	c.WorkerTTL = getInt("RELAYFORGE_WORKER_TTL", 600)
	c.WorkerPermanentExit = getInt("RELAYFORGE_WORKER_PERMANENT_EXIT_CODE", 12)
	c.WorkerConnectTimeout = getFloat("RELAYFORGE_WORKER_CONNECT_TIMEOUT", 5)
	c.WorkerReadTimeout = getFloat("RELAYFORGE_WORKER_READ_TIMEOUT", 10)
	c.SecretRevision = get("RELAYFORGE_SECRET_REVISION", "1")
	c.APIBackpressureActive = getInt("RELAYFORGE_API_BACKPRESSURE_MAX_ACTIVE", 10)
	c.APIBackpressureCreate = getInt("RELAYFORGE_API_BACKPRESSURE_MAX_CONCURRENT", 2)
	c.TestFaultCreateTimeout = false
	if v, ok := os.LookupEnv("RELAYFORGE_TEST_FAULT_CREATE_TIMEOUT"); ok {
		switch v {
		case "1", "true", "True", "TRUE", "yes":
			c.TestFaultCreateTimeout = true
		}
	}
	return c, nil
}

func required(k string) (string, error) {
	v, ok := os.LookupEnv(k)
	if !ok {
		return "", fmt.Errorf("missing required environment variable %s", k)
	}
	return v, nil
}

// LoadDestinations reads the destinations ConfigMap (mounted read-only).
func LoadDestinations(path string) (map[string]Destination, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read destinations: %w", err)
	}
	var m map[string]Destination
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse destinations: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("destinations must be an object")
	}
	return m, nil
}

// GetDestinationURL resolves the external receiver URL for a destination name.
func GetDestinationURL(name string, dests map[string]Destination) (string, error) {
	d, ok := dests[name]
	if !ok {
		return "", fmt.Errorf("unknown destination: %s", name)
	}
	if d.URL == "" {
		return "", fmt.Errorf("destination %s has no url", name)
	}
	return d.URL, nil
}