// Package tui is the bubbletea front end: a run view for one-shot
// tasks and a chat REPL. Both share the approval gate, the bridge
// between the synchronous agent.Approver callback and the bubbletea
// event loop.
package tui

import (
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"go-reins/internal/agent"
)

// ApprovalRequest is one tool call presented to the operator. Reply
// carries the decision back to the agent goroutine.
type ApprovalRequest struct {
	Tool  string
	Args  string
	Reply chan bool
}

// ApprovalGate connects the agent loop, which invokes Approver from
// its own goroutine, to a running bubbletea program. The approver
// blocks until the UI delivers a decision or the gate is closed.
type ApprovalGate struct {
	requests chan ApprovalRequest
	done     chan struct{}
	once     sync.Once
}

// NewApprovalGate builds an open gate.
func NewApprovalGate() *ApprovalGate {
	return &ApprovalGate{
		requests: make(chan ApprovalRequest),
		done:     make(chan struct{}),
	}
}

// Approver returns the agent.Approver backed by this gate.
func (g *ApprovalGate) Approver() agent.Approver {
	return func(name, args string) bool {
		req := ApprovalRequest{
			Tool:  name,
			Args:  args,
			Reply: make(chan bool, 1),
		}
		select {
		case g.requests <- req:
		case <-g.done:
			return false
		}
		select {
		case ok := <-req.Reply:
			return ok
		case <-g.done:
			return false
		}
	}
}

// Wait is a tea.Cmd that blocks until the next approval request
// arrives (or the gate closes) and delivers it as a message. Keep one
// Wait pending for as long as the agent is running.
func (g *ApprovalGate) Wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case req := <-g.requests:
			return req
		case <-g.done:
			return nil
		}
	}
}

// Close unblocks the approver and any pending Wait; pending tool
// calls are denied. Safe to call more than once.
func (g *ApprovalGate) Close() {
	g.once.Do(func() { close(g.done) })
}
