package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"go-reins/internal/agent"
)

func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"multi\nline\ttabs\r\n", "multi\nline\ttabs\n"},   // CRLF is a line break,
		{"unicode: héllo → 世界 🎉", "unicode: héllo → 世界 🎉"}, // emoji are not control characters
		{"\x1b[31mred\x1b[0m text", "red text"},            // CSI color
		{"\x1b[2J\x1b[Hcleared", "cleared"},                // screen rewrite, cursor home
		{"\x1b]0;fake title\x07after", "after"},            // OSC title
		{"\x1bMreverse index", "reverse index"},            // two-character ESC sequence
		{"before\x00null\x08\x7f", "beforenull"},           // C0 and DEL
		{"yes\rno", "yesno"},                               // lone CR overwrites the display line
		{"c1: \u009b31m", "c1: 31m"},                       // 8-bit CSI
		{"carriage\x0bvertical\x0cform", "carriageverticalform"},
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("Sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The views legitimately carry lipgloss styling escapes of their
// own, so the display-level assertions target the forged sequences
// by name: a model-generated cursor-up or color change must not
// survive into any rendered view.

// TestApprovalPromptCannotBeForged pins the approval dialog: the
// forged cursor-up is gone, so the args cannot move the cursor
// onto the prompt line. Lookalike prompt text may survive as inert
// content - text can always look like a prompt, but only control
// sequences can forge one.
func TestApprovalPromptCannotBeForged(t *testing.T) {
	req := &ApprovalRequest{
		Tool: "read",
		Args: "{\"path\":\"ok\"}\n\x1b[2Aallow? [y] allow · [n] deny",
	}
	view := renderApproval(req)
	if strings.Contains(view, "\x1b[2A") {
		t.Errorf("forged cursor-up survived into the approval view:\n%q", view)
	}
	if !strings.Contains(view, `{"path":"ok"}`) {
		t.Errorf("sanitized args lost their readable text:\n%q", view)
	}
}

// TestRunViewSanitizesAnswer pins the run view's final frame: the
// smuggled sequences are gone, the answer's text stays.
func TestRunViewSanitizesAnswer(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "ok"}, "m", "sys", nil)
	m := newRunModel(a, gate, nil, "task")
	next, _ := m.Update(stepDoneMsg{res: agent.RunResult{Answer: "safe\x1b[2A\x1b[31m text"}})
	m = next.(*runModel)
	view := m.View().Content
	if strings.Contains(view, "\x1b[2A") || strings.Contains(view, "\x1b[31m") {
		t.Errorf("smuggled sequences survived into the run view:\n%q", view)
	}
	if !strings.Contains(view, "safe") || !strings.Contains(view, "text") {
		t.Errorf("sanitized answer lost readable text:\n%q", view)
	}
}

// TestChatTranscriptSanitizesAnswer pins the chat transcript: the
// forming line sanitizes at render time - so a sequence split
// across stream deltas is caught too - and the final answer lands
// clean.
func TestChatTranscriptSanitizesAnswer(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "ok"}, "m", "sys", nil)
	stream := NewStreamFeed()
	defer stream.Close()
	m := newChatModel(a, gate, NewToolFeed(), stream, "dev", "go-reins chat", nil)
	m.Init()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(*chatModel)
	m.state = chatBusy      // a step must be running for deltas to land
	m.step = newStepScope() // the re-arm needs a live scope

	// The escape sequence arrives split across three deltas; the
	// render joins and sanitizes, so the cursor-up never reaches
	// the screen.
	m.updateStreamEvent(StreamEvent{Delta: "safe"})
	m.updateStreamEvent(StreamEvent{Delta: "\x1b[2"})
	m.updateStreamEvent(StreamEvent{Delta: "Ajump"})
	if view := m.View().Content; !strings.Contains(view, "safejump") {
		t.Errorf("split-delta text lost its readable part:\n%q", view)
	} else if strings.Contains(view, "\x1b[2A") {
		t.Errorf("split-delta cursor-up survived into the chat view:\n%q", view)
	}

	// The final answer lands sanitized.
	m.updateStepDone(stepDoneMsg{res: agent.RunResult{Answer: "done\x1b[31m"}})
	view := m.View().Content
	if strings.Contains(view, "\x1b[31m") {
		t.Errorf("smuggled color survived into the transcript:\n%q", view)
	}
	if !strings.Contains(view, "done") {
		t.Errorf("answer lost its readable text:\n%q", view)
	}
}

// TestChatStatusLineSanitizesTool pins the last display path: the
// busy status line shows the tool name and a shortened argument
// snippet, both model-controlled, and a tool event carrying escape
// sequences must not reach the terminal through it.
func TestChatStatusLineSanitizesTool(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "ok"}, "m", "sys", nil)
	stream := NewStreamFeed()
	defer stream.Close()
	m := newChatModel(a, gate, NewToolFeed(), stream, "dev", "go-reins chat", nil)
	m.Init()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(*chatModel)
	m.state = chatBusy      // a step must be running for the tool line to show
	m.step = newStepScope() // the re-arm needs a live scope

	m.updateToolEvent(ToolEvent{Name: "read", Args: "\x1b[2Ahi"})
	if view := m.View().Content; strings.Contains(view, "\x1b[2A") {
		t.Errorf("forged cursor-up survived into the status line:\n%q", view)
	}
	if !strings.Contains(m.View().Content, "read") {
		t.Errorf("status line lost the tool name:\n%q", m.View().Content)
	}
}
