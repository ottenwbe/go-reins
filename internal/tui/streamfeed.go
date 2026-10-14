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
// like the gate and tool feed waits. A notification whose text was
// already drained by the previous wait makes the loop go around
// again instead of ending the chain - a dead chain would leave a
// stale notification and undelivered text behind for the next step
// to misattribute. A wakeup that raced the step's end is dropped
// too: select may pick the notification even though done is
// closed, and delivering would attribute an ended step's text to
// whatever step is running by the time the message lands.
func (f *StreamFeed) WaitOn(done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		for {
			select {
			case <-f.notify:
				select {
				case <-done:
					return nil // the step this wait served is over
				default:
				}
			case <-f.done:
				return nil
			case <-done:
				return nil
			}
			f.mu.Lock()
			chunk := f.pending.String()
			f.pending.Reset()
			f.mu.Unlock()
			if chunk != "" {
				return StreamEvent{Delta: chunk}
			}
		}
	}
}

// Discard drops any text left in the feed and consumes a pending
// wakeup. A step's leftovers must not reach the next step's waiter:
// text the observer wrote in the race window around the step's end
// would otherwise be drained and shown as part of the next reply.
// Call it when a step ends, before the next waiter is armed.
func (f *StreamFeed) Discard() {
	f.mu.Lock()
	f.pending.Reset()
	f.mu.Unlock()
	select {
	case <-f.notify:
	default: // no wakeup pending
	}
}

// Close unblocks the observer and any pending wait. Safe to call
// more than once.
func (f *StreamFeed) Close() {
	f.once.Do(func() { close(f.done) })
}
