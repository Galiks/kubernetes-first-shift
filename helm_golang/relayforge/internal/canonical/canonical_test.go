package canonical

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type corpusRow struct {
	Input         string `json:"input"`
	ExpectedError bool   `json:"expected_error"`
	Canonical     *string `json:"canonical"`
	SHA256        *string `json:"sha256"`
}

func loadCorpus(t *testing.T) []corpusRow {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "canonical.jsonl")
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()
	var rows []corpusRow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var r corpusRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("bad corpus row: %v", err)
		}
		rows = append(rows, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan corpus: %v", err)
	}
	return rows
}

// TestGoldenDifferential asserts byte-for-byte parity with the Python oracle
// (orjson) across the corpus. This is the hard compatibility gate.
func TestGoldenDifferential(t *testing.T) {
	for _, r := range loadCorpus(t) {
		if r.ExpectedError {
			// The Python pipeline rejects at either stage (strict parse or
			// canonicalization). Mirror that: at least one stage must fail.
			_, err1 := Canonicalize([]byte(r.Input))
			_, err2 := StrictJSONLoads([]byte(r.Input))
			if err1 == nil && err2 == nil {
				t.Fatalf("input %q: expected a pipeline error", r.Input)
			}
			continue
		}
		got, err := Canonicalize([]byte(r.Input))
		if err != nil {
			t.Fatalf("input %q: unexpected error: %v", r.Input, err)
		}
		want := []byte(*r.Canonical)
		if string(got) != string(want) {
			t.Fatalf("input %q:\n  got  %q\n  want %q", r.Input, got, want)
		}
		sum := sha256.Sum256(got)
		if hex.EncodeToString(sum[:]) != *r.SHA256 {
			t.Fatalf("input %q: sha256 mismatch", r.Input)
		}
	}
}

// TestSelfConsistency enforces encode->decode->encode idempotence, the
// runtime stability guarantee for request-hash annotations.
func TestSelfConsistency(t *testing.T) {
	for _, r := range loadCorpus(t) {
		if r.ExpectedError || r.Canonical == nil {
			continue
		}
		out, err := Canonicalize([]byte(*r.Canonical))
		if err != nil {
			t.Fatalf("re-canonicalize %q: %v", *r.Canonical, err)
		}
		if string(out) != *r.Canonical {
			t.Fatalf("not idempotent:\n  got  %q\n  want %q", out, *r.Canonical)
		}
		if _, err := StrictJSONLoads([]byte(*r.Canonical)); err != nil {
			t.Fatalf("canonical form not strictly parseable: %v", err)
		}
	}
}

func TestStrictRejects(t *testing.T) {
	cases := []string{
		"[]", "[1,2]", `"str"`, "null", "true", "42", "1.5",
		`{"a":NaN}`, `{"a":Infinity}`, `{"a":-Infinity}`,
		`{"a":1,"a":2}`,
		"{\"a\":1}junk",
	}
	for _, c := range cases {
		if _, err := StrictJSONLoads([]byte(c)); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
	ok := `{"a":{"b":1}}`
	m, err := StrictJSONLoads([]byte(ok))
	if err != nil {
		t.Fatalf("valid object rejected: %v", err)
	}
	if _, ok := m["a"]; !ok {
		t.Fatalf("missing key a")
	}
}

// TestFloatRoundTrip ensures appendFloat always round-trips the exact float64
// and formatting is idempotent — the runtime stability guarantee for hashes
// derived from arbitrary float payloads. Deterministic pseudo-random corpus.
func TestFloatRoundTrip(t *testing.T) {
	rng := uint64(0x9E3779B97F4A7C15)
	next := func() uint64 {
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return rng
	}
	vals := []float64{
		0, -0.0, 1, -1, 1e300, -5e-324, 1e21, 1e-7, 1e16, 1e15,
		100.0, 0.0001, 0.00001, 123.456, -123.456e30, 3.141592653589793,
	}
	for i := 0; i < 20000; i++ {
		bits := next()
		if (bits>>52)&0x7ff != 0x7ff { // exclude NaN/Inf (not from valid JSON)
			vals = append(vals, math.Float64frombits(bits))
		}
	}
	for _, f := range vals {
		s1 := string(appendFloat(nil, f))
		g, err := strconv.ParseFloat(s1, 64)
		if err != nil {
			t.Fatalf("formatted %v -> %q unparseable: %v", f, s1, err)
		}
		if math.Float64bits(g) != math.Float64bits(f) {
			t.Fatalf("float round-trip mismatch: %v -> %q -> %v", f, s1, g)
		}
		s2 := string(appendFloat(nil, g))
		if s1 != s2 {
			t.Fatalf("float format not idempotent: %v -> %q then %q", f, s1, s2)
		}
	}
}