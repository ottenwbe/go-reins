package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	feed   *ToolFeed
	header string
	prompt string // prompt of the running step, for the status line

	history  []backend.Message
	lines    []string
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model
	state    chatState
	approval *ApprovalRequest
	tool     ToolEvent // tool of the running step, for the status line
	width    int
	height   int
}

func newChatModel(a *agent.Agent, gate *ApprovalGate, feed *ToolFeed, header string) *chatModel {
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
	// gutter keeps a low profile in both states. v2 hands styles
	// out by value, so they are edited and set back.
	styles := ta.Styles()
	styles.Focused.Prompt = userStyle
	styles.Focused.Placeholder = dimStyle
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Focused.CursorLineNumber = dimStyle
	styles.Focused.LineNumber = dimStyle
	styles.Blurred.Prompt = dimStyle
	styles.Blurred.Placeholder = dimStyle
	styles.Blurred.Text = dimStyle
	styles.Blurred.CursorLine = lipgloss.NewStyle()
	styles.Blurred.CursorLineNumber = dimStyle
	styles.Blurred.LineNumber = dimStyle
	ta.SetStyles(styles)

	return &chatModel{
		ctx:      ctx,
		cancel:   cancel,
		agent:    a,
		gate:     gate,
		feed:     feed,
		header:   header,
		viewport: viewport.New(),
		textarea: ta,
		spinner:  spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(spinnerStyle)),
		state:    chatReady,
	}
}

// Chat runs the interactive REPL until the operator quits. The alt
// screen is requested by the View itself (v2 models declare terminal
// state there); the transcript lives only as long as the session.
// The feed receives the agent's tool calls, so the status line can
// show which tool is in use.
func Chat(a *agent.Agent, gate *ApprovalGate, feed *ToolFeed, header string) error {
	defer feed.Close()
	_, err := tea.NewProgram(newChatModel(a, gate, feed, header)).Run()
	return err
}

func (m *chatModel) Init() tea.Cmd {
	return m.textarea.Focus()
}

func (m *chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.SetWidth(msg.Width)
		m.textarea.SetWidth(msg.Width)
		m.layout()
		return m, nil

	case ToolEvent:
		// The agent switched tools; show it and keep listening.
		m.tool = msg
		return m, m.feed.Wait()

	case stepDoneMsg:
		if msg.err != nil {
			m.appendLine(errorStyle.Render("error: " + msg.err.Error()))
		} else {
			m.history = msg.res.History
			m.appendLine(answerStyle.Render(msg.res.Answer))
		}
		m.prompt, m.tool, m.state = "", ToolEvent{}, chatReady
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

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			m.gate.Close()
			m.feed.Close()
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

// shorten caps the rendered args to a single status line; tool-call
// arguments are single-line JSON by protocol.
func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
	m.tool = ToolEvent{}
	m.state = chatBusy

	step := func() tea.Msg {
		res, err := m.agent.Step(m.ctx, m.history, prompt)
		return stepDoneMsg{res: res, err: err}
	}
	return tea.Batch(step, m.gate.Wait(), m.feed.Wait(), m.spinner.Tick)
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
	h := m.height - lipgloss.Height(m.header) - input - 4
	if h < 1 {
		h = 1
	}
	m.viewport.SetHeight(h)
}

func (m *chatModel) View() tea.View {
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
		if m.tool.Name != "" {
			sb.WriteString(toolStyle.Render(m.tool.Name))
			if m.tool.Args != "" {
				sb.WriteString(" ")
				sb.WriteString(dimStyle.Render(shorten(m.tool.Args, 60)))
			}
		} else {
			sb.WriteString(dimStyle.Render("thinking…"))
		}
	case chatApproving:
		sb.WriteString(fmt.Sprintf("allow? %s",
			dimStyle.Render("[y] allow · [n] deny")))
	default:
		sb.WriteString(dimStyle.Render("enter to send · ctrl+j new line · pgup/pgdown scroll · ctrl+c quit"))
	}
	sb.WriteString("\n\n")
	sb.WriteString(m.textarea.View())

	v := tea.NewView(sb.String())
	v.AltScreen = true
	return v
}
