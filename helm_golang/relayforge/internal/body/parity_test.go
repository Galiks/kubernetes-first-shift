package body

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"relayforge/internal/canonical"
)

// TestParityWithCanonical guards the silent-failure risk called out in body.go:
// the worker/testsink HMAC encoder must produce exactly the same bytes as the
// API-side canonical encoder (which is itself pinned to the Python orjson
// oracle by testdata/canonical.jsonl). Any divergence would break signatures.
func TestParityWithCanonical(t *testing.T) {
	p := filepath.Join("..", "..", "testdata", "canonical.jsonl")
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()

	rows := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var row struct {
			Input         string  `json:"input"`
			ExpectedError bool    `json:"expected_error"`
			Canonical     *string `json:"canonical"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatalf("bad corpus row: %v", err)
		}
		if row.ExpectedError || row.Canonical == nil {
			continue
		}
		want := *row.Canonical
		v, err := Unmarshal([]byte(want))
		if err != nil {
			t.Fatalf("body.Unmarshal(%q): %v", want, err)
		}
		got, err := Marshal(v)
		if err != nil {
			t.Fatalf("body.Marshal for %q: %v", want, err)
		}
		if string(got) != want {
			t.Fatalf("body/canonical parity break:\n  body      %q\n  canonical %q", got, want)
		}
		// Also assert the canonical package agrees byte-for-byte.
		c, err := canonical.Canonicalize([]byte(want))
		if err != nil {
			t.Fatalf("canonical.Canonicalize(%q): %v", want, err)
		}
		if string(c) != want {
			t.Fatalf("canonical not idempotent on %q -> %q", want, c)
		}
		rows++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rows == 0 {
		t.Fatal("no corpus rows exercised")
	}
	t.Logf("parity verified over %d canonical rows", rows)
}