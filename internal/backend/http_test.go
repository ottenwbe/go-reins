package backend

import (
	"strings"
	"testing"
)

func TestParseBaseURL(t *testing.T) {
	cases := []struct {
		in, def, want string
	}{
		{"", "http://localhost:11434", "http://localhost:11434"},             // fallback
		{"http://host:8080/", "def", "http://host:8080"},                     // trailing slash trimmed
		{"https://example.com/prefix/", "def", "https://example.com/prefix"}, // path prefix kept
	}
	for _, c := range cases {
		got, err := ParseBaseURL(c.in, c.def)
		if err != nil {
			t.Errorf("ParseBaseURL(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseBaseURLRejects(t *testing.T) {
	for _, bad := range []string{"localhost:11434", "ftp://host", "http://", "://", "no scheme at all"} {
		if _, err := ParseBaseURL(bad, "http://fallback"); err == nil {
			t.Errorf("ParseBaseURL(%q): want error, got nil", bad)
		}
	}
}

func TestServerMessage(t *testing.T) {
	cases := map[string]string{
		`{"error":"model not found"}`: "model not found",   // ollama convention
		`{"detail":"boom"}`:           `{"detail":"boom"}`, // non-error JSON: snippet
		"plain text failure":          "plain text failure",
		"":                            "(no details)",
	}
	for in, want := range cases {
		if got := serverMessage(strings.NewReader(in)); got != want {
			t.Errorf("serverMessage(%q) = %q, want %q", in, got, want)
		}
	}

	// Long bodies are capped so one broken server cannot flood the
	// error line.
	long := strings.Repeat("x", 500)
	if got := serverMessage(strings.NewReader(long)); len(got) > 250 {
		t.Errorf("serverMessage(long) len = %d, want capped", len(got))
	}
}
