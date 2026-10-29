package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"go-reins/internal/backend"
	"go-reins/internal/buildinfo"
)

// compactDoneMsg carries the summarization result back to the
// REPL. It is its own message type - not stepDoneMsg - so the
// compaction can never be confused with a prompt's step.
type compactDoneMsg struct {
	summary string
	err     error
}

// compactPrompt directs the summarization request: the model sees
// the whole conversation plus this one instruction. The reply
// replaces the conversation, so it must keep what matters and
// drop the rest - and stay plain: a TOOLCALL line in a summary
// would just be text, nothing dispatches it.
const compactPrompt = `Summarize the conversation above concisely. Keep the key facts, decisions, open questions, and any file paths or names; drop greetings, filler, and repetition. Reply with the summary only.`

// maxCompact caps the summary that becomes the new context: a
// runaway reply must not bloat the history it was meant to shrink.
const maxCompact = 16 * 1024

// compact collapses the conversation into a summary the model
// writes itself. One direct backend round trip - no agent loop, no
// tools, no approvals: compaction is bookkeeping, not a task, and
// the reply must never dispatch anything. On success the history
// becomes the system message plus a single user message carrying
// the summary, so the next prompt continues from a small context.
func (m *chatModel) compact() tea.Cmd {
	if len(m.history) <= 1 {
		m.appendLine(dimStyle.Render("(nothing to compact yet)"))
		return nil
	}
	m.compacting = true
	m.state = chatBusy
	m.layout() // the input area pauses while the model summarizes
	m.appendLine(dimStyle.Render("compacting the conversation…"))

	// The backend and the history are captured here, in the Update
	// loop - never read inside the Cmd - so a compaction cannot
	// race the step that follows it.
	b := m.agent.Backend()
	model := m.agent.Model()
	history := make([]backend.Message, len(m.history))
	copy(history, m.history)

	compact := func() tea.Msg {
		resp, err := b.Chat(m.ctx, backend.ChatRequest{
			Model: model,
			Messages: append(history, backend.Message{
				Role:    backend.RoleUser,
				Content: compactPrompt,
			}),
		})
		return compactDoneMsg{summary: resp.Content, err: err}
	}
	return tea.Batch(compact, m.spinner.Tick)
}

// updateCompactDone settles the compaction: on success the history
// is replaced by the system message plus the summary, and the
// transcript notes the shrinkage; on failure - or an empty
// summary - the history is untouched and the operator keeps
// everything they had.
func (m *chatModel) updateCompactDone(msg compactDoneMsg) tea.Cmd {
	m.compacting = false
	m.state = chatReady
	m.layout() // the textarea returns
	if msg.err != nil {
		m.appendLine(errorStyle.Render(Sanitize("error: " + msg.err.Error())))
		return nil
	}
	summary := strings.TrimSpace(msg.summary)
	if summary == "" {
		m.appendLine(errorStyle.Render("the model returned an empty summary - history unchanged"))
		return nil
	}
	if len(summary) > maxCompact {
		// Keep the head on a rune boundary - a summary's opening
		// carries the frame of the conversation.
		cut := maxCompact
		for cut > 0 && !utf8.RuneStart(summary[cut]) {
			cut--
		}
		summary = summary[:cut] + "\n(summary truncated)"
	}

	beforeMsgs, beforeChars := len(m.history), 0
	for _, mm := range m.history {
		beforeChars += len(mm.Content)
	}
	compacted := make([]backend.Message, 0, 2)
	for _, mm := range m.history {
		if mm.Role == backend.RoleSystem {
			compacted = append(compacted, mm)
			break
		}
	}
	compacted = append(compacted, backend.Message{
		Role: backend.RoleUser,
		Content: "A summary of this conversation so far, for context:\n\n" +
			summary +
			"\n\nContinue from here; the conversation above is the full record.",
	})
	m.history = compacted

	m.appendLine(dimStyle.Render(fmt.Sprintf(
		"(history compacted: %d messages, %d chars -> %d messages, %d chars)",
		beforeMsgs, beforeChars, len(m.history), len(m.history[1].Content))))
	return nil
}

// command runs one REPL slash command. Commands act on the live
// session only - they never reach the model, so they skip the step
// machinery entirely: no busy state, no approval gate, no history
// changes. They are accepted only while idle, which submit's caller
// guarantees (busy swallows keys).
func (m *chatModel) command(line string) tea.Cmd {
	m.appendLine(userStyle.Render("› " + strings.ReplaceAll(line, "\n", "\n  ")))
	m.textarea.Reset()
	m.layout()

	name, rest, _ := strings.Cut(line, " ")
	switch name {
	case "/models":
		m.appendLine(dimStyle.Render("asking " + m.agent.BackendName() + " for its models…"))
		return m.listModels("", 0, false)

	case "/model":
		model := strings.TrimSpace(rest)
		if model == "" {
			m.appendLine(errorStyle.Render("usage: /model <name> · current: " + m.agent.Model()))
			return nil
		}
		// Validate against the backend's listing before switching:
		// a typo would otherwise only surface as a failed next
		// step. The check runs as a Cmd so the UI never blocks,
		// and carries a generation so an older listing that
		// finishes after a newer /model cannot apply.
		m.modelSeq++
		m.appendLine(dimStyle.Render("checking " + model + " against " + m.agent.BackendName() + "'s models…"))
		return m.listModels(model, m.modelSeq, false)

	case "/backend":
		target := strings.TrimSpace(rest)
		if target == "" {
			m.appendLine(errorStyle.Render("usage: /backend <ollama|llamacpp> · current: " + m.agent.BackendName()))
			return nil
		}
		if m.switchBackend == nil {
			m.appendLine(errorStyle.Render("backend switching is not wired up in this session"))
			return nil
		}
		if err := m.switchBackend(target); err != nil {
			m.appendLine(errorStyle.Render("error: " + err.Error()))
			return nil
		}
		// The old model almost certainly does not exist on the
		// new backend; list what does, and let the listing's
		// follow-up (reconcileModel) keep or replace the model.
		// The switch is a model-deciding command like /model, so
		// it takes a generation: a /model issued while the
		// listing is in flight supersedes the reconciliation.
		m.modelSeq++
		m.appendLine(dimStyle.Render("backend switched to " + m.agent.BackendName() + " - listing its models…"))
		return m.listModels("", m.modelSeq, true)

	case "/info":
		m.appendLine(dimStyle.Render(m.infoLines()))
		return nil

	case "/metrics":
		m.appendLine(dimStyle.Render(m.metricsLines()))
		return nil

	case "/compact":
		return m.compact()

	case "/debug":
		// Compiled in only with -tags debugui; the off build's
		// handler says so instead of offering a toggle.
		return m.debugCommand()

	default:
		m.appendLine(errorStyle.Render("unknown command " + name + " · available: /models, /model <name>, /backend <name>, /compact, /info, /metrics" + debugCommands))
		return nil
	}
}

// infoLines renders the build story for the transcript, in the
// same fields as the info command but formatted for the chat view.
// A binary built without module metadata reports only its version.
func (m *chatModel) infoLines() string {
	bi := buildinfo.Collect(m.version)
	var sb strings.Builder
	sb.WriteString("go-reins " + bi.Version)
	if !bi.Available {
		return sb.String()
	}
	sb.WriteString("\nmodule " + bi.Module + " (" + bi.ModuleVersion + ")")
	if bi.Commit != "" {
		sb.WriteString("\ncommit " + bi.Commit)
	}
	if bi.Built != "" {
		sb.WriteString("\nbuilt " + bi.Built)
	}
	sb.WriteString("\ngo " + bi.Go)
	sb.WriteString("\nplatform " + bi.Platform)
	return sb.String()
}
