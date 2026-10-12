// Shared HTTP plumbing for the backend adapters: both talk JSON
// over HTTP, so the round trip, the error bodies, and the URL
// handling live here once instead of copy-pasted per adapter.
package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
)

// ParseBaseURL validates and normalizes a backend base URL: it must
// be absolute with an http(s) scheme and a host, and it loses its
// trailing slash so adapters can append paths without double slashes.
// An empty url falls back to def. Failures surface here, at the
// configuration boundary, instead of at the first request.
func ParseBaseURL(url, def string) (string, error) {
	if url == "" {
		url = def
	}
	u, err := neturl.Parse(url)
	if err != nil {
		return "", fmt.Errorf("invalid backend URL %q: %w", url, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid backend URL %q: want http or https", url)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid backend URL %q: missing host", url)
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

// PostJSON performs the JSON round trip every adapter needs: marshal
// payload, POST to base+path, decode the JSON response into out. A
// non-2xx status becomes an error that carries the server's own
// message when the body has one, so operators see "model not found"
// instead of "unexpected status 404".
func PostJSON(ctx context.Context, client *http.Client, base, path string, payload, out any) error {
	url := strings.TrimSuffix(base, "/") + path

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("status %s: %s", resp.Status, serverMessage(resp.Body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// GetJSON performs the read-only half of the adapter round trip: GET
// base+path and decode the JSON response into out. Status and error
// handling mirror PostJSON so listing models fails the same way
// chatting does.
func GetJSON(ctx context.Context, client *http.Client, base, path string, out any) error {
	url := strings.TrimSuffix(base, "/") + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("status %s: %s", resp.Status, serverMessage(resp.Body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// serverMessage extracts the server's own error text from a failed
// response: a JSON body shaped {"error":"..."} (the ollama
// convention) or {"error":{"message":"..."}} (the OpenAI convention
// llama.cpp follows) wins, anything else is capped to a short
// snippet.
func serverMessage(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, 2048))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return "(no details)"
	}
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &e); err == nil && len(e.Error) > 0 {
		var msg string
		if json.Unmarshal(e.Error, &msg) == nil && msg != "" {
			return msg
		}
		var obj struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(e.Error, &obj) == nil && obj.Message != "" {
			return obj.Message
		}
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
