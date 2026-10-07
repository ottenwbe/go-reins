package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
)

// chatState is what the REPL is waiting for.
type chatState int

const (
	chatReady     chatState = iota // waiting for the next prompt
	chatBusy                       // agent is working on the current prompt
	chatApproving                  // waiting for the operator's tool-call decision
)

// chatModel is the interactive REPL: a scrolling transcript, an input
// area, and an approval dialog. Conversation history is carried from
// prompt to prompt via agent.Step, so the model keeps its context
// within the session.
type chatModel struct {
	ctx    context.Context
	cancel context.CancelFunc
	agent  *agent.Agent
	gate   *ApprovalGate
	header string
	prompt string // prompt of the running step, for the status line

	history  []backend.Message
	lines    []string
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model
	state    chatState
	approval *ApprovalRequest
	width    int
	height   int
}

func newChatModel(a *agent.Agent, gate *ApprovalGate, header string) *chatModel {
	ctx, cancel := context.WithCancel(context.Background())

	ta := textarea.New()
	ta.Placeholder = "type a task, ctrl+j for a new line, enter to send, ctrl+c to quit"
	ta.ShowLineNumbers = true
	ta.Prompt = "› "
	ta.MaxHeight = 5

	// Enter sends, so the default newline binding (enter/ctrl+m) is
	// taken. Rebind newline to alt+enter and ctrl+j; ctrl+m cannot
	// be used because the terminal delivers it as enter.
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "ctrl+j"),
		key.WithHelp("ctrl+j", "new line"),
	)

	// Restyle the input area: the prompt sign picks up the accent
	// color while focused and goes dim when blurred, the cursor
	// line drops its full-line background (the block cursor is
	// enough), the placeholder stays muted, and the line-number
	// gutter keeps a low profile in both states.
	ta.FocusedStyle.Prompt = userStyle
	ta.FocusedStyle.Placeholder = dimStyle
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLineNumber = dimStyle
	ta.FocusedStyle.LineNumber = dimStyle
	ta.BlurredStyle.Prompt = dimStyle
	ta.BlurredStyle.Placeholder = dimStyle
	ta.BlurredStyle.Text = dimStyle
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLineNumber = dimStyle
	ta.BlurredStyle.LineNumber = dimStyle

	return &chatModel{
		ctx:      ctx,
		cancel:   cancel,
		agent:    a,
		gate:     gate,
		header:   header,
		viewport: viewport.New(0, 0),
		textarea: ta,
		spinner:  spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(spinnerStyle)),
		state:    chatReady,
	}
}

// Chat runs the interactive REPL until the operator quits. It uses
// the alt screen; the transcript lives only as long as the session.
func Chat(a *agent.Agent, gate *ApprovalGate, header string) error {
	_, err := tea.NewProgram(newChatModel(a, gate, header), tea.WithAltScreen()).Run()
	return err
}

func (m *chatModel) Init() tea.Cmd {
	return m.textarea.Focus()
}

func (m *chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = msg.Width
		m.textarea.SetWidth(msg.Width)
		m.layout()
		return m, nil

	case stepDoneMsg:
		if msg.err != nil {
			m.appendLine(errorStyle.Render("error: " + msg.err.Error()))
		} else {
			m.history = msg.res.History
			m.appendLine(answerStyle.Render(msg.res.Answer))
		}
		m.prompt, m.state = "", chatReady
		return m, nil

	case ApprovalRequest:
		m.approval, m.state = &msg, chatApproving
		m.appendLine(toolStyle.Render("tool call: " + msg.Tool + " " + msg.Args))
		return m, nil

	case spinner.TickMsg:
		if m.state == chatBusy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			m.gate.Close()
			return m, tea.Quit
		}
		switch m.state {
		case chatApproving:
			switch msg.String() {
			case "y", "Y", "enter":
				m.appendLine(dimStyle.Render("allowed"))
				m.approval.Reply <- true
				m.approval, m.state = nil, chatBusy
				return m, m.gate.Wait()
			case "n", "N", "esc":
				m.appendLine(dimStyle.Render("denied"))
				m.approval.Reply <- false
				m.approval, m.state = nil, chatBusy
				return m, m.gate.Wait()
			}
			return m, nil
		case chatBusy:
			return m, nil // swallow keys while the agent works
		}
		if msg.String() == "enter" {
			return m, m.submit()
		}
		// Scrolling keys go to the transcript, everything else to
		// the input area.
		switch msg.String() {
		case "pgup", "pgdown", "home", "end":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		m.layout() // the input may have grown or shrunk a line
		return m, cmd
	}

	// Non-key messages keep the textarea cursor blinking.
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// submit starts one agent step for the entered prompt.
func (m *chatModel) submit() tea.Cmd {
	prompt := strings.TrimSpace(m.textarea.Value())
	if prompt == "" {
		return nil
	}
	// Continuation lines are indented under the prompt sign.
	m.appendLine(userStyle.Render("› " + strings.ReplaceAll(prompt, "\n", "\n  ")))
	m.prompt = prompt
	m.textarea.Reset()
	m.layout() // the input shrank back to one line
	m.state = chatBusy

	step := func() tea.Msg {
		res, err := m.agent.Step(m.ctx, m.history, prompt)
		return stepDoneMsg{res: res, err: err}
	}
	return tea.Batch(step, m.gate.Wait(), m.spinner.Tick)
}

// appendLine adds a rendered line to the transcript and scrolls to
// the bottom.
func (m *chatModel) appendLine(line string) {
	m.lines = append(m.lines, line)
	m.viewport.SetContent(strings.Join(m.lines, "\n"))
	m.viewport.GotoBottom()
}

// layout recomputes the viewport height from the window size and the
// current input area height.
func (m *chatModel) layout() {
	input := lipgloss.Height(m.textarea.View())
	m.viewport.Height = m.height - lipgloss.Height(m.header) - input - 4
	if m.viewport.Height < 1 {
		m.viewport.Height = 1
	}
}

func (m *chatModel) View() string {
	var sb strings.Builder
	sb.WriteString(m.header)
	sb.WriteString("\n")
	sb.WriteString(m.viewport.View())
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")
	switch m.state {
	case chatBusy:
		sb.WriteString(m.spinner.View())
		sb.WriteString(" ")
		sb.WriteString(dimStyle.Render("thinking…"))
	case chatApproving:
		sb.WriteString(fmt.Sprintf("allow? %s",
			dimStyle.Render("[y] allow · [n] deny")))
	default:
		sb.WriteString(dimStyle.Render("enter to send · ctrl+j new line · pgup/pgdown scroll · ctrl+c quit"))
	}
	sb.WriteString("\n\n")
	sb.WriteString(m.textarea.View())
	return sb.String()
}
