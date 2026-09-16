package worker

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"time"
)

// NewClient builds an http.Client honoring the connect and read timeouts from
// config (mirroring httpx connect/read). The overall Job deadline is applied by
// the caller via the request context.
func NewClient(connectTimeout, readTimeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: readTimeout,
		// Do not follow redirects: Python uses httpx follow_redirects=False, so
		// a 3xx is returned to the classifier (-> permanent -> exit 12) instead
		// of being followed (which would convert POST->GET and leak the
		// X-Relay-* signature headers to the redirect target).
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: connectTimeout}).DialContext,
		},
	}
}

// Deliver POSTs the signed body to dstURL and returns the HTTP status code.
// Any transport-level error returns a non-nil error; redirects are NOT
// followed (follow_redirects=False in Python).
func Deliver(ctx context.Context, client *http.Client, dstURL string, body []byte, headers map[string]string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dstURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
