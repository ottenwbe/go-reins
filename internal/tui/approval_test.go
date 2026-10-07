package tui

import (
	"testing"
	"time"
)

// callApprover runs the approver once and returns its decision (or
// fails the test if it does not return).
func callApprover(t *testing.T, approve func(name, args string) bool) bool {
	t.Helper()
	decision := make(chan bool, 1)
	go func() { decision <- approve("shell", "{\"command\":\"ls\"}") }()
	select {
	case got := <-decision:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("approver did not return")
		return false
	}
}

func TestApprovalGateRoundTrip(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()

	approve := gate.Approver()
	decision := make(chan bool, 1)
	go func() { decision <- approve("shell", "{}") }()

	msg := gate.Wait()()
	req, ok := msg.(ApprovalRequest)
	if !ok {
		t.Fatalf("Wait returned %T, want ApprovalRequest", msg)
	}
	if req.Tool != "shell" || req.Args != "{}" {
		t.Errorf("request = %+v, want shell {}", req)
	}

	req.Reply <- true
	select {
	case got := <-decision:
		if !got {
			t.Error("decision = deny, want allow")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approver did not return after reply")
	}
}

func TestApprovalGateDenyDecision(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()

	approve := gate.Approver()
	decision := make(chan bool, 1)
	go func() { decision <- approve("shell", "{}") }()

	req := gate.Wait()().(ApprovalRequest)
	req.Reply <- false
	select {
	case got := <-decision:
		if got {
			t.Error("decision = allow, want deny")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approver did not return after reply")
	}
}

func TestApprovalGateClosedDenies(t *testing.T) {
	gate := NewApprovalGate()
	gate.Close()

	if callApprover(t, gate.Approver()) {
		t.Error("approver allowed through a closed gate")
	}
	if msg := gate.Wait()(); msg != nil {
		t.Errorf("Wait after close returned %T, want nil", msg)
	}
}

func TestApprovalGateCloseDuringPendingCallDenies(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()

	approve := gate.Approver()
	decision := make(chan bool, 1)
	go func() { decision <- approve("shell", "{}") }()

	gate.Wait()() // deliver the pending request
	gate.Close()  // operator quits before deciding
	select {
	case got := <-decision:
		if got {
			t.Error("pending call allowed after close, want deny")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approver stuck after close")
	}
}
