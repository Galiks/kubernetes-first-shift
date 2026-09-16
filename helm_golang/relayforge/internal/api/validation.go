package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"unicode/utf8"

	"relayforge/internal/canonical"
)

const maxBody = 16 * 1024

var (
	idempotencyKeyRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)
	eventTypeRe      = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,63}$`)
	allowedFields    = map[string]bool{"destination": true, "event_type": true, "payload": true}
)

type validatedRequest struct {
	idemKey string
	data    map[string]canonical.Node
	raw     []byte
	dest    string
	event   string
	payload map[string]canonical.Node
}

// validateRequest mirrors relayforge/api/validation.py step by step.
func validateRequest(w http.ResponseWriter, r *http.Request, st *State) (*validatedRequest, error) {
	// 1. Size from Content-Length header.
	if length := r.Header.Get("Content-Length"); length != "" {
		var n int
		if _, err := fmt.Sscanf(length, "%d", &n); err == nil && n > maxBody {
			return nil, NewApiError(413, "BODY_TOO_LARGE", "body exceeds 16 KiB", nil)
		}
	}

	// 2. Read + UTF-8.
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, NewApiError(400, "INVALID_JSON", err.Error(), nil)
	}
	if len(raw) > maxBody {
		return nil, NewApiError(413, "BODY_TOO_LARGE", "body exceeds 16 KiB", nil)
	}
	if !utf8.Valid(raw) {
		return nil, NewApiError(400, "INVALID_UTF8", "invalid utf-8 encoding", nil)
	}

	// 3. Strict JSON (duplicate keys / NaN / Infinity rejected, root must be an object).
	data, err := canonical.StrictJSONLoads(raw)
	if err != nil {
		return nil, NewApiError(400, "INVALID_JSON", err.Error(), nil)
	}

	// 4. Unknown fields.
	var unknown []string
	for k := range data {
		if !allowedFields[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, NewApiError(422, "UNKNOWN_FIELDS", fmt.Sprintf("unknown fields: %v", unknown), nil)
	}

	// 5. Required fields.
	for _, f := range []string{"destination", "event_type", "payload"} {
		if _, ok := data[f]; !ok {
			return nil, NewApiError(422, "MISSING_FIELD", fmt.Sprintf("missing field: %s", f), nil)
		}
	}

	// 6. Types.
	destNode := data["destination"]
	if destNode.Kind != canonical.KindString {
		return nil, NewApiError(422, "INVALID_FIELD", "destination must be string", nil)
	}
	evNode := data["event_type"]
	if evNode.Kind != canonical.KindString {
		return nil, NewApiError(422, "INVALID_FIELD", "event_type must be string", nil)
	}
	payloadNode := data["payload"]
	if payloadNode.Kind != canonical.KindObject {
		return nil, NewApiError(422, "INVALID_FIELD", "payload must be object", nil)
	}

	// 7. Destination from config.
	destinations := st.destinationsCopy()
	if _, ok := destinations[destNode.Str]; !ok {
		return nil, NewApiError(422, "UNKNOWN_DESTINATION", fmt.Sprintf("unknown destination: %s", destNode.Str), nil)
	}

	// 8. Event type regex.
	if !eventTypeRe.MatchString(evNode.Str) {
		return nil, NewApiError(422, "INVALID_EVENT_TYPE", "event_type does not match pattern", nil)
	}

	// 9. Idempotency-Key.
	idemKey := r.Header.Get("Idempotency-Key")
	if !idempotencyKeyRe.MatchString(idemKey) {
		return nil, NewApiError(422, "INVALID_IDEMPOTENCY_KEY", "key does not match pattern", nil)
	}

	return &validatedRequest{
		idemKey: idemKey,
		data:    data,
		raw:     raw,
		dest:    destNode.Str,
		event:   evNode.Str,
		payload: payloadNode.Obj,
	}, nil
}

// payloadToJSON re-encodes the payload node canonically so that the worker
// receives the same byte-exact JSON that orjson produced for the hash.
// Numbers travel verbatim (json.Number), strings are emitted with orjson-style
// escaping (Go json.Marshal with HTML escaping disabled matches this for all
// inputs: same short escapes \b \t \n \f \r \\ \", \u00xx for control chars,
// all other bytes raw UTF-8 — identical to canonical.appendString).
func payloadToJSON(payload map[string]canonical.Node) (string, error) {
	v := nodeToGoValue(canonical.Node{Kind: canonical.KindObject, Obj: payload})
	buf := bytes.NewBuffer(nil)
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	// Encoder appends a trailing newline; orjson.dumps does not.
	s := buf.String()
	s = s[:len(s)-1]
	return s, nil
}

// nodeToGoValue converts a canonical Node tree into plain JSON-marshalable Go
// values. json.Number passes through verbatim so Marshal keeps the original
// token bytes (matching orjson number serialization of parsed tokens).
func nodeToGoValue(n canonical.Node) interface{} {
	switch n.Kind {
	case canonical.KindNull:
		return nil
	case canonical.KindBool:
		return n.Bool
	case canonical.KindString:
		return n.Str
	case canonical.KindNumber:
		return n.Num
	case canonical.KindArray:
		out := make([]interface{}, len(n.Arr))
		for i, e := range n.Arr {
			out[i] = nodeToGoValue(e)
		}
		return out
	case canonical.KindObject:
		out := make(map[string]interface{}, len(n.Obj))
		for k, e := range n.Obj {
			out[k] = nodeToGoValue(e)
		}
		return out
	default:
		return nil
	}
}