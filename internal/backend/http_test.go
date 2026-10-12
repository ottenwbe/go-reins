package backend

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestGetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var out struct{ Ok bool }
	if err := GetJSON(context.Background(), &http.Client{}, srv.URL, "/x", &out); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if !out.Ok {
		t.Error("GetJSON did not decode the response")
	}
}

func TestGetJSONTrimsTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := GetJSON(context.Background(), &http.Client{}, srv.URL+"/", "/x", &struct{}{}); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if gotPath != "/x" {
		t.Errorf("path = %q, want /x (trailing slash must not double it)", gotPath)
	}
}

func TestGetJSONCarriesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no models here"}`))
	}))
	defer srv.Close()

	err := GetJSON(context.Background(), &http.Client{}, srv.URL, "/x", &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "no models here") {
		t.Errorf("GetJSON error = %v, want server's message included", err)
	}
}

func TestGetJSONRejectsBadBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	err := GetJSON(context.Background(), &http.Client{}, srv.URL, "/x", &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Errorf("GetJSON error = %v, want decode error", err)
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
