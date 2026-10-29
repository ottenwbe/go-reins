package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"go-reins/internal/agent"
)

// ErrAborted reports a run the operator interrupted with ctrl+c.
var ErrAborted = errors.New("run aborted by operator")

// stepDoneMsg carries the outcome of one agent run to the UI.
type stepDoneMsg struct {
	res agent.RunResult
	err error
}

// runState is what the run view is currently showing.
type runState int

const (
	runThinking runState = iota
	runApproving
	runDone
	runError
	runAborted
)

// runModel drives a one-shot task: a spinner while the model works,
// an approval dialog on each tool call, and the answer as the final
// frame. The agent loop runs in a goroutine started by Init; when a
// stream feed is wired, the reply text appears under the spinner as
// it arrives.
type runModel struct {
	ctx      context.Context
	cancel   context.CancelFunc
	agent    *agent.Agent
	gate     *ApprovalGate
	stream   *StreamFeed // may be nil: no observer wired, nothing armed
	task     string
	live     string // streamed reply text, shown while thinking
	spinner  spinner.Model
	state    runState
	res      agent.RunResult
	err      error
	approval *ApprovalRequest
}

func newRunModel(a *agent.Agent, gate *ApprovalGate, stream *StreamFeed, task string) *runModel {
	ctx, cancel := context.WithCancel(context.Background())
	return &runModel{
		ctx:     ctx,
		cancel:  cancel,
		agent:   a,
		gate:    gate,
		stream:  stream,
		task:    task,
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(spinnerStyle)),
	}
}

// RunTask runs the agent under a bubbletea program and returns when
// the program exits. The final frame contains the answer (or the
// error), so nothing needs printing afterwards. The stream feed
// (may be nil) receives the reply text as it arrives, so the
// thinking frame shows the answer forming. Options are passed
// through to the program (used by tests to run headless).
func RunTask(a *agent.Agent, gate *ApprovalGate, stream *StreamFeed, task string, opts ...tea.ProgramOption) (agent.RunResult, error) {
	if stream != nil {
		defer stream.Close()
	}
	final, err := tea.NewProgram(newRunModel(a, gate, stream, task), opts...).Run()
	if err != nil {
		return agent.RunResult{}, err
	}
	m, ok := final.(*runModel)
	if !ok {
		return agent.RunResult{}, errors.New("tui: unexpected final model")
	}
	switch m.state {
	case runDone:
		return m.res, nil
	case runError:
		return agent.RunResult{}, m.err
	default:
		return agent.RunResult{}, ErrAborted
	}
}

// Init starts the agent goroutine, the spinner, the first approval
// wait, and the stream wait. The agent closes the gate when it
// finishes so a pending Wait cannot block forever.
func (m *runModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		func() tea.Msg {
			res, err := m.agent.Run(m.ctx, m.task)
			m.gate.Close()
			return stepDoneMsg{res: res, err: err}
		},
		m.spinner.Tick,
		m.gate.Wait(),
	}
	if m.stream != nil {
		cmds = append(cmds, m.stream.WaitOn(nil))
	}
	return tea.Batch(cmds...)
}

func (m *runModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case stepDoneMsg:
		m.gate.Close()
		m.cancel()
		m.live = ""
		if msg.err != nil {
			m.err, m.state = msg.err, runError
		} else {
			m.res, m.state = msg.res, runDone
		}
		return m, tea.Quit

	case StreamEvent:
		m.live = appendLive(m.live, msg.Delta)
		return m, m.stream.WaitOn(nil)

	case ApprovalRequest:
		m.approval, m.state = &msg, runApproving
		return m, nil

	case spinner.TickMsg:
		if m.state == runThinking {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			m.gate.Close()
			m.state = runAborted
			return m, tea.Quit
		}
		if m.state != runApproving {
			return m, nil
		}
		switch msg.String() {
		case "y", "Y", "enter":
			return m.decide(true)
		case "n", "N", "esc":
			return m.decide(false)
		}
	}
	return m, nil
}

// decide answers the pending approval request and goes back to
// thinking, with the next approval wait armed.
func (m *runModel) decide(allow bool) (tea.Model, tea.Cmd) {
	if m.approval != nil {
		m.approval.Reply <- allow
		m.approval = nil
	}
	m.state = runThinking
	return m, m.gate.Wait()
}

func (m *runModel) View() tea.View {
	var sb strings.Builder
	sb.WriteString(dimStyle.Render("task: "))
	sb.WriteString(m.task)
	sb.WriteString("\n\n")

	switch m.state {
	case runThinking:
		sb.WriteString(m.spinner.View())
		sb.WriteString(" thinking\n")
		if m.live != "" {
			// The answer forming, streamed; the final frame below
			// carries the complete text.
			sb.WriteString("\n")
			sb.WriteString(answerStyle.Render(Sanitize(m.live)))
			sb.WriteString("\n")
		}
	case runApproving:
		sb.WriteString(renderApproval(m.approval))
	case runDone:
		sb.WriteString("\n")
		sb.WriteString(answerStyle.Render(Sanitize(m.res.Answer)))
		sb.WriteString("\n\n")
		summary := fmt.Sprintf("%d turn(s)", m.res.Turns)
		if stats := usageLine(m.res); stats != "" {
			summary += " · " + stats
		}
		sb.WriteString(dimStyle.Render(summary))
		sb.WriteString("\n")
	case runError:
		sb.WriteString(errorStyle.Render(Sanitize("error: " + m.err.Error())))
		sb.WriteString("\n")
	case runAborted:
		sb.WriteString(dimStyle.Render("aborted"))
		sb.WriteString("\n")
	}
	return tea.NewView(sb.String())
}

// usageLine renders the reply statistics shown under a finished
// answer: the completion token count and the generation rate of the
// reply the answer came from, when the backend reported them. Empty
// when there is nothing to show - a backend that reports no usage
// leaves the transcript alone.
func usageLine(res agent.RunResult) string {
	last := res.FinalReply()
	if last.Usage.CompletionTokens <= 0 {
		return ""
	}
	if rate := last.TokensPerSecond(); rate > 0 {
		return fmt.Sprintf("%d tokens · %.1f tok/s", last.Usage.CompletionTokens, rate)
	}
	return fmt.Sprintf("%d tokens", last.Usage.CompletionTokens)
}

// renderApproval is shared by the run and chat views.
func renderApproval(req *ApprovalRequest) string {
	var sb strings.Builder
	sb.WriteString(toolStyle.Render("tool call: "))
	sb.WriteString(Sanitize(req.Tool))
	sb.WriteString(" ")
	sb.WriteString(Sanitize(req.Args))
	sb.WriteString("\n")
	sb.WriteString("allow? ")
	sb.WriteString(dimStyle.Render("[y] allow · [n] deny · ctrl+c abort"))
	sb.WriteString("\n")
	return sb.String()
}
