package worker

// classify mirrors relayforge/worker/classifier.py: maps an HTTP status to a
// delivery outcome kind.
func classify(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "success"
	case status == 408 || status == 429 || (status >= 500 && status < 600):
		return "transient"
	case status >= 400 && status < 500:
		return "permanent"
	case status >= 300 && status < 400:
		return "permanent" // redirect treated as an error
	default:
		return "transient"
	}
}
