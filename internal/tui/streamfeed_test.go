package tui

import "testing"

// TestStreamFeedDiscardDropsLeftovers pins the step-boundary behavior:
// text written before a Discard must never reach a later wait, so a
// cancelled step's tail cannot be shown as part of the next reply.
func TestStreamFeedDiscardDropsLeftovers(t *testing.T) {
	f := NewStreamFeed()
	defer f.Close()
	observe := f.Observer()

	observe("stale ")
	f.Discard()
	observe("fresh")

	msg := f.WaitOn(nil)()
	ev, ok := msg.(StreamEvent)
	if !ok {
		t.Fatalf("WaitOn msg = %T, want StreamEvent", msg)
	}
	if ev.Delta != "fresh" {
		t.Errorf("Delta = %q, want %q - the discarded text leaked", ev.Delta, "fresh")
	}
}

// TestStreamFeedWaitOnDropsAfterStepEnd pins that a wakeup racing the
// step's cancellation delivers nothing, even though the select may
// see the notification first.
func TestStreamFeedWaitOnDropsAfterStepEnd(t *testing.T) {
	f := NewStreamFeed()
	defer f.Close()
	done := make(chan struct{})

	f.Observer()("text written as the step ends")
	close(done)

	msg := f.WaitOn(done)()
	if msg != nil {
		t.Errorf("WaitOn msg = %v, want nil from a cancelled step", msg)
	}
}
