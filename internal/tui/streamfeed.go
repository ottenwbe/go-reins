package tui

import (
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"go-reins/internal/agent"
)

// StreamEvent carries a chunk of the model's reply text to the UI.
// Chunks are coalesced, not tokens: one event holds everything that
// arrived since the UI last looked.
type StreamEvent struct {
	Delta string
}

// StreamFeed connects the agent's stream observer, invoked from the
// step goroutine as tokens arrive, to a running bubbletea program.
// It is the streaming counterpart of the ToolFeed, with one
// addition the token rate forces: the observer never blocks.
// Deltas accumulate in a buffer and a single pending notification
// wakes the waiter, which drains whatever has piled up - a busy UI
// coalesces bursts instead of stalling generation.
type StreamFeed struct {
	mu      sync.Mutex
	pending strings.Builder
	notify  chan struct{}
	done    chan struct{}
	once    sync.Once
}

// NewStreamFeed builds an open feed.
func NewStreamFeed() *StreamFeed {
	return &StreamFeed{
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

// Observer returns the agent.StreamObserver backed by this feed. It
// appends to the buffer and pokes the waiter; it never blocks, so
// token generation outpaces the redraw loop freely.
func (f *StreamFeed) Observer() agent.StreamObserver {
	return func(delta string) {
		f.mu.Lock()
		f.pending.WriteString(delta)
		f.mu.Unlock()
		select {
		case f.notify <- struct{}{}:
		default: // a wakeup is already pending
		}
	}
}

// WaitOn is a tea.Cmd that blocks until new text has arrived (or
// the feed or the step ends) and delivers it as one StreamEvent.
// done cancels the wait, so it can be scoped to one agent step
// like the gate and tool feed waits.
func (f *StreamFeed) WaitOn(done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-f.notify:
		case <-f.done:
			return nil
		case <-done:
			return nil
		}
		f.mu.Lock()
		chunk := f.pending.String()
		f.pending.Reset()
		f.mu.Unlock()
		if chunk == "" {
			return nil // raced a drain: nothing new to show
		}
		return StreamEvent{Delta: chunk}
	}
}

// Close unblocks the observer and any pending wait. Safe to call
// more than once.
func (f *StreamFeed) Close() {
	f.once.Do(func() { close(f.done) })
}
