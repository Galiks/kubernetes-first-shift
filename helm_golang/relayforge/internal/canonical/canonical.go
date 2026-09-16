// Package canonical implements strict JSON parsing and canonical serialization
// that is byte-for-byte compatible with the Python reference implementation
// (orjson with OPT_SORT_KEYS + strict parsing rejecting duplicate keys,
// NaN/Infinity and non-object roots).
//
// Golden regulator: testdata/canonical.jsonl, generated with tools/gen_corpus.py
// from the actual Python module. Any divergence from that fixture is a bug in
// this package, not a change to the fixture.
package canonical

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

// Node is the parsed canonical value.
type Node struct {
	Kind NodeKind
	Bool bool
	Str  string
	Num  json.Number
	Arr  []Node
	Obj  map[string]Node
}

type NodeKind int

const (
	KindNull NodeKind = iota
	KindBool
	KindString
	KindNumber
	KindArray
	KindObject
)

// StrictJSONLoads mirrors relayforge.canonical.strict_json_loads:
// duplicate keys and NaN/Infinity are rejected, root must be an object.
func StrictJSONLoads(raw []byte) (map[string]Node, error) {
	n, err := parseNode(raw)
	if err != nil {
		return nil, err
	}
	if n.Kind != KindObject {
		return nil, fmt.Errorf("root must be an object")
	}
	return n.Obj, nil
}

// Canonicalize mirrors relayforge.canonical.canonicalize: re-encodes the parsed
// value with sorted keys (UTF-8 byte order) and orjson-compatible string/number
// serialization.
func Canonicalize(raw []byte) ([]byte, error) {
	n, err := parseNode(raw)
	if err != nil {
		return nil, err
	}
	var b []byte
	return n.append(&b)
}

// AppendTo writes this node's canonical (orjson-compatible) encoding into dst
// and returns the extended slice. Exported so other packages can canonicalize
// already-parsed Node trees (e.g. the API's perform_create path) with the exact
// same serializer as Canonicalize.
func (n Node) AppendTo(dst *[]byte) ([]byte, error) {
	return n.append(dst)
}

// parseNode parses a single JSON value with duplicate-key detection and
// rejecting trailing content. Numbers stay verbatim via json.Number.
func parseNode(raw []byte) (Node, error) {
	if err := rejectLoneSurrogates(raw); err != nil {
		return Node{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	n, err := parseValue(dec)
	if err != nil {
		return Node{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Node{}, fmt.Errorf("trailing content after JSON value")
	}
	return n, nil
}

// rejectLoneSurrogates rejects JSON strings containing an unpaired \uD800-\uDFFF
// escape. Python's json accepts such strings but orjson.dumps then raises
// TypeError ("surrogates not allowed"), so the reference pipeline refuses them;
// Go's encoding/json would silently substitute U+FFFD and corrupt the payload.
// Valid surrogate pairs (e.g. \ud83d\ude00 -> an astral rune) are allowed.
func rejectLoneSurrogates(raw []byte) error {
	isHigh := func(h uint32) bool { return h >= 0xD800 && h <= 0xDBFF }
	isLow := func(h uint32) bool { return h >= 0xDC00 && h <= 0xDFFF }
	hex4 := func(b []byte) (uint32, bool) {
		var v uint32
		for _, c := range b {
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= uint32(c - '0')
			case c >= 'a' && c <= 'f':
				v |= uint32(c-'a') + 10
			case c >= 'A' && c <= 'F':
				v |= uint32(c-'A') + 10
			default:
				return 0, false
			}
		}
		return v, true
	}
	inString := false
	for i := 0; i < len(raw); {
		c := raw[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			i++
			continue
		}
		switch c {
		case '"':
			inString = false
			i++
		case '\\':
			if i+1 >= len(raw) {
				return nil // truncated escape: the decoder reports it
			}
			if raw[i+1] != 'u' {
				i += 2 // \n, \", \\, ... (escaped backslash handled here)
				continue
			}
			if i+6 > len(raw) {
				return nil
			}
			h, ok := hex4(raw[i+2 : i+6])
			if !ok {
				i += 6
				continue
			}
			if isLow(h) {
				return fmt.Errorf("invalid JSON: lone low surrogate \\u%04X", h)
			}
			if isHigh(h) {
				if i+12 <= len(raw) && raw[i+6] == '\\' && raw[i+7] == 'u' {
					if l, ok := hex4(raw[i+8 : i+12]); ok && isLow(l) {
						i += 12 // valid pair
						continue
					}
				}
				return fmt.Errorf("invalid JSON: lone high surrogate \\u%04X", h)
			}
			i += 6
		default:
			i++
		}
	}
	return nil
}

func parseValue(dec *json.Decoder) (Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return Node{}, fmt.Errorf("invalid JSON: %w", err)
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]Node{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return Node{}, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return Node{}, fmt.Errorf("invalid JSON: object key is not a string")
				}
				if _, dup := m[key]; dup {
					return Node{}, fmt.Errorf("duplicate key: %s", key)
				}
				v, err := parseValue(dec)
				if err != nil {
					return Node{}, err
				}
				m[key] = v
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return Node{}, err
			}
			return Node{Kind: KindObject, Obj: m}, nil
		case '[':
			var arr []Node
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return Node{}, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return Node{}, err
			}
			return Node{Kind: KindArray, Arr: arr}, nil
		default:
			return Node{}, fmt.Errorf("invalid JSON: unexpected delimiter %q", t)
		}
	case json.Number:
		return Node{Kind: KindNumber, Num: t}, nil
	case string:
		return Node{Kind: KindString, Str: t}, nil
	case bool:
		return Node{Kind: KindBool, Bool: t}, nil
	case nil:
		return Node{Kind: KindNull}, nil
	default:
		return Node{}, fmt.Errorf("invalid JSON value")
	}
}

// append serializes the node to canonical bytes.
func (n Node) append(b *[]byte) ([]byte, error) {
	switch n.Kind {
	case KindNull:
		*b = append(*b, "null"...)
	case KindBool:
		if n.Bool {
			*b = append(*b, "true"...)
		} else {
			*b = append(*b, "false"...)
		}
	case KindString:
		*b = appendString(*b, n.Str)
	case KindNumber:
		var err error
		*b, err = appendNumber(*b, n.Num)
		if err != nil {
			return nil, err
		}
	case KindArray:
		*b = append(*b, '[')
		for i, e := range n.Arr {
			if i > 0 {
				*b = append(*b, ',')
			}
			var err error
			*b, err = e.append(b)
			if err != nil {
				return nil, err
			}
		}
		*b = append(*b, ']')
	case KindObject:
		keys := make([]string, 0, len(n.Obj))
		for k := range n.Obj {
			keys = append(keys, k)
		}
		sort.Strings(keys) // UTF-8 byte order, matches orjson OPT_SORT_KEYS
		*b = append(*b, '{')
		for i, k := range keys {
			if i > 0 {
				*b = append(*b, ',')
			}
			*b = appendString(*b, k)
			*b = append(*b, ':')
			var err error
			*b, err = n.Obj[k].append(b)
			if err != nil {
				return nil, err
			}
		}
		*b = append(*b, '}')
	default:
		return nil, fmt.Errorf("unknown node kind")
	}
	return *b, nil
}

// appendNumber emits a number exactly as orjson would: integer tokens via
// big.Int normalization (so "-0" -> "0", leading zeros stripped) restricted to
// the 64-bit range orjson supports, float/exponent tokens via short float
// formatting with Python-repr notation rules.
func appendNumber(b []byte, num json.Number) ([]byte, error) {
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
		// Python json.loads("1e309") -> inf, orjson.dumps(inf) -> null.
		if errors.Is(err, strconv.ErrRange) {
			return append(b, "null"...), nil
		}
		return nil, fmt.Errorf("invalid number: %s", s)
	}
	return appendFloat(b, f), nil
}

// appendFloat formats f like orjson: shortest round-trip digits, scientific
// notation when the decimal exponent is >= 16 or <= -6 (orjson's boundary,
// verified against the oracle: 1e-5 -> "0.00005", 1e-6 -> "1e-6"), always with a
// fractional part (e.g. "100.0"), lowercase 'e' exponent with no '+' and no
// leading zeros.
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