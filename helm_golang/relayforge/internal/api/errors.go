package api

import (
	"encoding/json"
	"net/http"
)

// ApiError mirrors relayforge/api/errors.py: a structured HTTP error carrying a
// status code, an error code and a message, plus optional response headers.
type ApiError struct {
	Status  int
	Code    string
	Message string
	Headers map[string]string
}

func (e *ApiError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// NewApiError builds an ApiError with optional headers.
func NewApiError(status int, code, message string, headers map[string]string) *ApiError {
	return &ApiError{Status: status, Code: code, Message: message, Headers: headers}
}

// WriteApiError renders the ApiError as JSON: {"error": {"code": ..., "message": ...}}
// with the given headers, mirroring errors.api_error_handler.
func WriteApiError(w http.ResponseWriter, exc *ApiError) {
	body, _ := json.Marshal(map[string]interface{}{
		"error": map[string]string{"code": exc.Code, "message": exc.Message},
	})
	h := w.Header()
	h.Set("Content-Type", "application/json")
	for k, v := range exc.Headers {
		h.Set(k, v)
	}
	w.WriteHeader(exc.Status)
	w.Write(body)
}