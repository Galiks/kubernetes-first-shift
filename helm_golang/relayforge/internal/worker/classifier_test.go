package worker

import "testing"

// Table ported verbatim from tests/classifier-test.py.
func TestClassify(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{200, "success"}, {201, "success"}, {204, "success"},
		{408, "transient"}, {429, "transient"},
		{500, "transient"}, {502, "transient"}, {503, "transient"},
		{400, "permanent"}, {401, "permanent"}, {404, "permanent"}, {422, "permanent"},
		{300, "permanent"}, {301, "permanent"}, {302, "permanent"},
		{0, "transient"},
	}
	for _, c := range cases {
		if got := classify(c.status); got != c.want {
			t.Errorf("classify(%d) = %q, want %q", c.status, got, c.want)
		}
	}
}
