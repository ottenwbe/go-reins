package tui

import (
	"sync"

	tea "charm.land/bubbletea/v2"

	"go-reins/internal/agent"
)

// ToolEvent tells the UI which tool the agent is using right now.
type ToolEvent struct {
	Name string
	Args string
}

// ToolFeed connects the agent loop, which invokes the observer from
// its own goroutine, to a running bubbletea program: the observer
// pushes events, Wait delivers them as messages. It is the feed
// counterpart of the ApprovalGate; the events channel is buffered
// so the agent loop never blocks on a busy UI.
type ToolFeed struct {
	events chan ToolEvent
	done   chan struct{}
	once   sync.Once
}

// NewToolFeed builds an open feed.
func NewToolFeed() *ToolFeed {
	return &ToolFeed{
		events: make(chan ToolEvent, 16),
		done:   make(chan struct{}),
	}
}

// Observer returns the agent.ToolObserver backed by this feed.
func (f *ToolFeed) Observer() agent.ToolObserver {
	return func(name, args string) {
		select {
		case f.events <- ToolEvent{Name: name, Args: args}:
		case <-f.done:
		}
	}
}

// Wait is a tea.Cmd that blocks until the next tool event arrives
// (or the feed closes) and delivers it as a message. Keep one Wait
// pending for as long as a step is running.
func (f *ToolFeed) Wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-f.events:
			return ev
		case <-f.done:
			return nil
		}
	}
}

// Close unblocks the observer and any pending Wait. Safe to call
// more than once.
func (f *ToolFeed) Close() {
	f.once.Do(func() { close(f.done) })
}
