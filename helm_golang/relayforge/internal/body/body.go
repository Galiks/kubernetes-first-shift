// Package body provides the canonical request-body encoder shared by the worker
// (producer) and the test-sink (verifier).
//
// The bytes it emits must be identical to the Python reference
//
//	orjson.dumps(value, option=orjson.OPT_SORT_KEYS)
//
// used by relayforge/worker/run.py. Object keys are sorted in UTF-8 byte order
// (Go's sort.Strings), numeric literals that arrived as json.Number are
// re-emitted with orjson/Python-repr semantics (integer normalisation plus
// shortest round-trip float formatting), and strings use the same escape set as
// orjson (short escapes plus lowercase \u00xx for control bytes, everything
// else raw UTF-8).
//
// Both sides of the HMAC must use this package; a divergence here silently
// breaks every delivery signature.
//
// The serializer is intentionally self-contained (it does not import
// relayforge/internal/canonical, which is owned by another workstream) but the
// number/string rules are the same orjson-compatible rules.
package body

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// Marshal renders v as canonical JSON with sorted object keys, matching
// orjson.dumps(v, option=orjson.OPT_SORT_KEYS).
func Marshal(v interface{}) ([]byte, error) {
	out := make([]byte, 0, 128)
	return appendValue(out, v)
}

// MarshalMap is the common entry point for the worker body and for the
// test-sink canonicalization: render the map with its keys sorted.
func MarshalMap(m map[string]interface{}) ([]byte, error) {
	return Marshal(m)
}

// Unmarshal parses JSON into the exact Go shape Marshal expects, preserving
// integral literals as json.Number so ints are re-emitted byte-for-byte.
func Unmarshal(raw []byte) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonical body: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("canonical body: trailing content after JSON value")
	}
	return v, nil
}

// UnmarshalObject parses a JSON object into the map shape used for the worker
// body.
func UnmarshalObject(raw []byte) (map[string]interface{}, error) {
	v, err := Unmarshal(raw)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("payload must be a JSON object")
	}
	return obj, nil
}

func appendValue(b []byte, v interface{}) ([]byte, error) {
	switch t := v.(type) {
	case nil:
		return append(b, "null"...), nil
	case bool:
		if t {
			return append(b, "true"...), nil
		}
		return append(b, "false"...), nil
	case string:
		return appendString(b, t), nil
	case json.Number:
		return appendNumberBytes(b, t)
	case float64:
		return appendFloat(b, t), nil
	case float32:
		return appendFloat(b, float64(t)), nil
	case int:
		return strconv.AppendInt(b, int64(t), 10), nil
	case int8:
		return strconv.AppendInt(b, int64(t), 10), nil
	case int16:
		return strconv.AppendInt(b, int64(t), 10), nil
	case int32:
		return strconv.AppendInt(b, int64(t), 10), nil
	case int64:
		return strconv.AppendInt(b, t, 10), nil
	case uint:
		return strconv.AppendUint(b, uint64(t), 10), nil
	case uint8:
		return strconv.AppendUint(b, uint64(t), 10), nil
	case uint16:
		return strconv.AppendUint(b, uint64(t), 10), nil
	case uint32:
		return strconv.AppendUint(b, uint64(t), 10), nil
	case uint64:
		return strconv.AppendUint(b, t, 10), nil
	case []interface{}:
		b = append(b, '[')
		for i, e := range t {
			if i > 0 {
				b = append(b, ',')
			}
			var err error
			b, err = appendValue(b, e)
			if err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys) // UTF-8 byte order == orjson OPT_SORT_KEYS
		b = append(b, '{')
		for i, k := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendString(b, k)
			b = append(b, ':')
			var err error
			b, err = appendValue(b, t[k])
			if err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	default:
		return nil, fmt.Errorf("canonical body: unsupported type %T", v)
	}
}

// appendNumberBytes emits a json.Number exactly as orjson would: integer tokens
// via big.Int normalization (so "-0" -> "0", leading zeros stripped) restricted
// to the 64-bit range orjson supports, float/exponent tokens via shortest
// round-trip formatting with Python-repr notation rules.
func appendNumberBytes(b []byte, num json.Number) ([]byte, error) {
	s := string(num)
	if strings.IndexByte(s, '.') == -1 && strings.IndexByte(s, 'e') == -1 && strings.IndexByte(s, 'E') == -1 {
		bi, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("invalid number: %s", s)
		}
		if bi.IsInt64() {
			return strconv.AppendInt(b, bi.Int64(), 10), nil
		}
		if bi.Sign() >= 0 && bi.BitLen() <= 64 {
			return strconv.AppendUint(b, bi.Uint64(), 10), nil
		}
		return nil, fmt.Errorf("integer out of 64-bit range (orjson-compatible): %s", s)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// orjson parses overflow to inf and emits null, matching Python.
		if errors.Is(err, strconv.ErrRange) {
			return append(b, "null"...), nil
		}
		return nil, fmt.Errorf("invalid number: %s", s)
	}
	return appendFloat(b, f), nil
}

// appendFloat formats f like orjson: shortest round-trip digits, scientific
// notation when the decimal exponent is >= 16 or <= -6 (verified against the
// oracle: 1e-5 -> "0.00005", 1e-6 -> "1e-6"), always with a fractional part
// (e.g. "100.0"), lowercase 'e' exponent with no '+' and no leading zeros.
func appendFloat(b []byte, f float64) []byte {
	if f == 0 {
		if math.Signbit(f) {
			return append(b, "-0.0"...)
		}
		return append(b, "0.0"...)
	}
	if f < 0 {
		b = append(b, '-')
		f = -f
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	e := strings.IndexByte(s, 'e')
	mant := s[:e]
	exp, _ := strconv.Atoi(s[e+1:])
	digits := strings.ReplaceAll(mant, ".", "")
	if exp >= 16 || exp <= -6 {
		b = append(b, digits[0])
		if len(digits) > 1 {
			b = append(b, '.')
			b = append(b, digits[1:]...)
		}
		b = append(b, 'e')
		if exp < 0 {
			b = append(b, '-')
			exp = -exp
		}
		return strconv.AppendInt(b, int64(exp), 10)
	}
	pre := exp + 1
	if pre <= 0 {
		b = append(b, "0."...)
		b = append(b, bytes.Repeat([]byte{'0'}, -pre)...)
		b = append(b, digits...)
	} else if pre >= len(digits) {
		b = append(b, digits...)
		b = append(b, bytes.Repeat([]byte{'0'}, pre-len(digits))...)
		b = append(b, ".0"...)
	} else {
		b = append(b, digits[:pre]...)
		b = append(b, '.')
		b = append(b, digits[pre:]...)
	}
	return b
}

// appendString writes an orjson-compatible JSON string: escapes only '"', '\',
// and control characters < 0x20 (short escapes plus lowercase \u00xx), leaves
// all other bytes (including non-ASCII and U+2028/U+2029) as raw UTF-8.
func appendString(b []byte, s string) []byte {
	const hex = "0123456789abcdef"
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\b':
			b = append(b, '\\', 'b')
		case '\t':
			b = append(b, '\\', 't')
		case '\n':
			b = append(b, '\\', 'n')
		case '\f':
			b = append(b, '\\', 'f')
		case '\r':
			b = append(b, '\\', 'r')
		default:
			if c < 0x20 {
				b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
			} else {
				b = append(b, c)
			}
		}
	}
	return append(b, '"')
}
