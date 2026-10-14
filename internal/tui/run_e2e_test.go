package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
	"go-reins/internal/tools/read"
)

// streamStubBackend emits scripted replies as stream deltas through
// ChatStream, the way the real adapters do.
type streamStubBackend struct {
	mu      sync.Mutex
	turn    int
	replies [][]string
}

func (b *streamStubBackend) Name() string { return "stub" }
func (b *streamStubBackend) Models(context.Context) ([]string, error) {
	return []string{"m"}, nil
}
func (b *streamStubBackend) Chat(ctx context.Context, req backend.ChatRequest) (backend.ChatResponse, error) {
	return b.ChatStream(ctx, req, nil)
}
func (b *streamStubBackend) ChatStream(ctx context.Context, req backend.ChatRequest, onDelta func(string)) (backend.ChatResponse, error) {
	b.mu.Lock()
	turn := b.turn
	b.turn++
	b.mu.Unlock()
	var full strings.Builder
	if turn < len(b.replies) {
		for _, d := range b.replies[turn] {
			full.WriteString(d)
			if onDelta != nil {
				onDelta(d)
			}
		}
	}
	return backend.ChatResponse{Content: full.String()}, nil
}

// TestRunTaskToolCallEndToEnd reproduces a live tool call end to end:
// streamed prose plus a TOOLCALL line through the gate, the real
// read tool executing, and the final answer as the run result -
// all under a real (headless) bubbletea program.
func TestRunTaskToolCallEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("the payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gate := NewApprovalGate()
	defer gate.Close()
	stream := NewStreamFeed()
	defer stream.Close()

	b := &streamStubBackend{replies: [][]string{
		{`Checking the file.`, "\n", "TOOLCALL read {\"path\":\"notes.txt\"}\n"},
		{`Done: the payload`},
	}}
	a := agent.New(b, "m", "sys", []agent.Tool{read.New()},
		agent.WithStreamObserver(stream.Observer()),
	)

	res, err := RunTask(a, gate, stream, "read notes.txt", tea.WithoutRenderer(), tea.WithInput(nil))
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	t.Logf("turns=%d answer=%q", res.Turns, res.Answer)
	if !strings.Contains(res.Answer, "Done") {
		t.Errorf("final answer missing; answer=%q turns=%d", res.Answer, res.Turns)
	}
	if res.Turns < 2 {
		t.Errorf("turns=%d, want the tool round trip to have happened", res.Turns)
	}
}
