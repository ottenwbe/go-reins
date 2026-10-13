// Streaming plumbing shared by the backend adapters: the Streamer
// capability interface and the chunked-POST half of a streaming
// round trip, so each adapter owns only its per-line decode.
package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// maxStreamLine caps one line of a chunked response. Reply chunks
// are small JSON objects; a line that exceeds this means a broken
// or hostile server, and failing beats buffering without bound.
const maxStreamLine = 256 * 1024

// Streamer is the streaming capability a Backend can offer in
// addition to Chat: reply text is handed to onDelta as it arrives
// on the wire, and the full response is still returned - the agent
// loop needs the complete reply before it can parse a tool call or
// finish the turn. Callers that only know Backend discover the
// capability with a type assertion; adapters without streaming
// simply do not implement it.
type Streamer interface {
	// ChatStream is Chat with deltas: onDelta receives each piece
	// of the reply content as it arrives. A nil onDelta collects
	// the full response only, so Chat can delegate here.
	ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResponse, error)
}

// PostJSONLines performs the streaming half of the adapter round
// trip: marshal the payload, POST to base+path, and hand each
// non-empty line of the chunked response to onLine. Status and
// error handling mirror PostJSON, so a streaming chat fails the
// same way a buffered one does. Ollama answers in newline-delimited
// JSON and llama.cpp in SSE; both are line-oriented, so the line
// scanning lives here once.
func PostJSONLines(ctx context.Context, client *http.Client, base, path string, payload any, onLine func(line []byte) error) error {
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
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("status %s: %s", resp.Status, serverMessage(resp.Body))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), maxStreamLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := onLine(line); err != nil {
			return fmt.Errorf("stream %s: %w", url, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read stream %s: %w", url, err)
	}
	return nil
}
