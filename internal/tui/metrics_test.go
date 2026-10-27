package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
)

// measured builds a finished step whose backend reported the given
// completion tokens over the given round-trip wall time.
func measured(tokens int, elapsed time.Duration) agent.RunResult {
	return agent.RunResult{
		Answer: "an answer",
		Turns:  1,
		Replies: []agent.ReplyStat{{
			Usage:   backend.Usage{PromptTokens: 512, CompletionTokens: tokens},
			Elapsed: elapsed,
		}},
	}
}

// TestReplyMetricsAccumulates pins the rollup: replies, tokens, and
// the min/avg/max rates over several measured replies.
func TestReplyMetricsAccumulates(t *testing.T) {
	var m replyMetrics
	m.record(measured(42, 2*time.Second)) // 21.0 tok/s
	m.record(measured(84, 2*time.Second)) // 42.0 tok/s
	m.record(measured(10, 1*time.Second)) // 10.0 tok/s

	if m.replies != 3 {
		t.Errorf("replies = %d, want 3", m.replies)
	}
	if m.tokens != 136 {
		t.Errorf("tokens = %d, want 136", m.tokens)
	}
	if m.minRate != 10.0 {
		t.Errorf("minRate = %v, want 10.0", m.minRate)
	}
	if m.maxRate != 42.0 {
		t.Errorf("maxRate = %v, want 42.0", m.maxRate)
	}
	if got := m.average(); got != 73.0/3 {
		t.Errorf("average = %v, want %v", got, 73.0/3)
	}
}

// TestReplyMetricsSkipsUnmeasured pins that a reply the backend
// reported no usage for - or that measured no time - counts nowhere:
// it is not a data point, and it must not drag the rates down.
func TestReplyMetricsSkipsUnmeasured(t *testing.T) {
	var m replyMetrics
	m.record(agent.RunResult{Answer: "no usage"})
	m.record(agent.RunResult{
		Answer:  "no time",
		Replies: []agent.ReplyStat{{Usage: backend.Usage{CompletionTokens: 10}}},
	})
	m.record(measured(20, time.Second))

	if m.replies != 1 {
		t.Errorf("replies = %d, want 1 (only the measured reply counts)", m.replies)
	}
	if m.tokens != 20 {
		t.Errorf("tokens = %d, want 20", m.tokens)
	}
	if m.minRate != 20.0 || m.maxRate != 20.0 || m.average() != 20.0 {
		t.Errorf("rates = min %v avg %v max %v, want 20/20/20", m.minRate, m.average(), m.maxRate)
	}
}

// TestReplyMetricsZeroValue pins the empty accumulator: an average
// over no measurements is 0, not a division by zero.
func TestReplyMetricsZeroValue(t *testing.T) {
	var m replyMetrics
	if m.average() != 0 {
		t.Errorf("average = %v, want 0", m.average())
	}
	m.record(measured(5, time.Second))
	if m.replies != 1 || m.minRate != 5.0 {
		t.Errorf("after one record: %+v, want 1 reply at 5.0 tok/s", m)
	}
}

// TestReplyMetricsCountsEveryReplyOfAStep pins the throughput
// accounting: a step whose model answered with a tool call first
// contributes both round trips - the tool turn and the final answer -
// because the session's rate is the rate of every reply the model
// generated, not of the answers alone.
func TestReplyMetricsCountsEveryReplyOfAStep(t *testing.T) {
	var m replyMetrics
	m.record(agent.RunResult{
		Answer: "the answer",
		Turns:  2,
		Replies: []agent.ReplyStat{
			{Usage: backend.Usage{CompletionTokens: 7}, Elapsed: time.Second},      // tool turn: 7 tok/s
			{Usage: backend.Usage{CompletionTokens: 42}, Elapsed: 2 * time.Second}, // final: 21 tok/s
		},
	})
	if m.replies != 2 {
		t.Errorf("replies = %d, want 2 (tool turn + final answer)", m.replies)
	}
	if m.tokens != 49 {
		t.Errorf("tokens = %d, want 49", m.tokens)
	}
	if m.minRate != 7.0 || m.maxRate != 21.0 || m.average() != 14.0 {
		t.Errorf("rates = min %v avg %v max %v, want 7/14/21", m.minRate, m.average(), m.maxRate)
	}
}

// TestChatMetricsCommand pins /metrics: finished steps fold into the
// session rollup, the command prints counts and min/avg/max rates,
// and the unknown-command list names /metrics. Before any measured
// reply the command says there is nothing yet.
func TestChatMetricsCommand(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "the answer"}, "m", "sys", nil)
	m := startChat(a, gate, nil)

	// No measured replies yet: the empty state, not a zeroed report.
	m, _ = runCommand(m, "/metrics")
	if !strings.Contains(strings.Join(m.lines, "\n"), "no token metrics yet") {
		t.Errorf("empty-state line missing: %v", m.lines)
	}

	// Two measured replies land: 21.0 and 42.0 tok/s.
	next, _ := m.Update(stepDoneMsg{res: measured(42, 2*time.Second)})
	m = next.(*chatModel)
	next, _ = m.Update(stepDoneMsg{res: measured(84, 2*time.Second)})
	m = next.(*chatModel)
	// An unmeasured reply and a failed step change nothing.
	next, _ = m.Update(stepDoneMsg{res: agent.RunResult{Answer: "no usage", Turns: 1}})
	m = next.(*chatModel)
	next, _ = m.Update(stepDoneMsg{err: errors.New("backend down")})
	m = next.(*chatModel)

	m, _ = runCommand(m, "/metrics")
	transcript := strings.Join(m.lines, "\n")
	if !strings.Contains(transcript, "2 model replies · 126 completion tokens") {
		t.Errorf("counts line missing: %q", transcript)
	}
	if !strings.Contains(transcript, "tok/s: min 21.0 · avg 31.5 · max 42.0") {
		t.Errorf("rates line missing: %q", transcript)
	}

	// The unknown-command hint names /metrics.
	m, _ = runCommand(m, "/nope")
	if !strings.Contains(strings.Join(m.lines, "\n"), "/metrics") {
		t.Errorf("unknown-command list missing /metrics: %v", m.lines)
	}
}
