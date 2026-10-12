package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"

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
	ctx           context.Context
	cancel        context.CancelFunc
	agent         *agent.Agent
	gate          *ApprovalGate
	feed          *ToolFeed
	title         string                  // session line prefix; the live header appends backend/model
	switchBackend func(name string) error // wires /backend to session.NewBackend
	prompt        string                  // prompt of the running step, for the status line

	history  []backend.Message
	lines    []string
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model
	state    chatState
	approval *ApprovalRequest
	tool     ToolEvent // tool of the running step, for the status line
	step     *stepScope
	width    int
	height   int
}

// stepScope bounds the waiter Cmds armed for one agent step. When
// the step ends - answer delivered, error, or quit - cancel closes
// the scope's done channel and every waiter armed against it aborts
// without delivering. Without this, each prompt leaves a blocked
// gate and feed waiter behind for the lifetime of the session.
type stepScope struct {
	done chan struct{}
	once sync.Once
}

func newStepScope() *stepScope {
	return &stepScope{done: make(chan struct{})}
}

func (s *stepScope) cancel() {
	if s != nil {
		s.once.Do(func() { close(s.done) })
	}
}

func newChatModel(a *agent.Agent, gate *ApprovalGate, feed *ToolFeed, title string, switchBackend func(string) error) *chatModel {
	ctx, cancel := context.WithCancel(context.Background())

	ta := textarea.New()
	ta.Placeholder = "type a task or /models, /model, /backend · ctrl+j for a new line · ctrl+c quits"
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
		ctx:           ctx,
		cancel:        cancel,
		agent:         a,
		gate:          gate,
		feed:          feed,
		title:         title,
		switchBackend: switchBackend,
		viewport:      viewport.New(),
		textarea:      ta,
		spinner:       spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(spinnerStyle)),
		state:         chatReady,
	}
}

// Chat runs the interactive REPL until the operator quits. The alt
// screen is requested by the View itself (v2 models declare terminal
// state there); the transcript lives only as long as the session.
// The feed receives the agent's tool calls, so the status line can
// show which tool is in use. switchBackend (may be nil) connects
// the /backend command to the config-backed backend constructors.
func Chat(a *agent.Agent, gate *ApprovalGate, feed *ToolFeed, title string, switchBackend func(string) error) error {
	defer feed.Close()
	_, err := tea.NewProgram(newChatModel(a, gate, feed, title, switchBackend)).Run()
	return err
}

// headerText is the live session line: title plus the backend and
// model the next request will use, so a mid-session switch shows.
func (m *chatModel) headerText() string {
	return fmt.Sprintf("%s · %s/%s", m.title, m.agent.BackendName(), m.agent.Model())
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
		// A tool event from a step that already ended (racing the
		// final answer) is noise: drop it instead of reviving the
		// status line and arming a new waiter while idle.
		if m.state != chatBusy {
			return m, nil
		}
		// The agent switched tools; show it and keep listening.
		m.tool = msg
		return m, m.feed.WaitOn(m.step.done)

	case stepDoneMsg:
		// The step is over: cancel its waiters so they cannot
		// linger into the next prompt, then show the outcome.
		m.step.cancel()
		m.step = nil
		if msg.err != nil {
			m.appendLine(errorStyle.Render("error: " + msg.err.Error()))
		} else {
			m.history = msg.res.History
			m.appendLine(answerStyle.Render(msg.res.Answer))
		}
		m.prompt, m.tool, m.state = "", ToolEvent{}, chatReady
		return m, nil

	case modelsDoneMsg:
		// The listing from a /models or /backend command, or the
		// validation behind a /model, arrived.
		if msg.err != nil {
			m.appendLine(errorStyle.Render("error: " + msg.err.Error()))
			return m, nil
		}
		if msg.want != "" {
			m.applyModel(msg.want, msg.names)
			return m, nil
		}
		if len(msg.names) == 0 {
			m.appendLine(dimStyle.Render("(the backend reported no models)"))
			return m, nil
		}
		m.appendLine(dimStyle.Render(strings.Join(msg.names, "\n")))
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
			m.step.cancel()
			return m, tea.Quit
		}
		switch m.state {
		case chatApproving:
			switch msg.String() {
			case "y", "Y", "enter":
				m.appendLine(dimStyle.Render("allowed"))
				m.approval.Reply <- true
				m.approval, m.state = nil, chatBusy
				return m, m.gate.WaitOn(m.step.done)
			case "n", "N", "esc":
				m.appendLine(dimStyle.Render("denied"))
				m.approval.Reply <- false
				m.approval, m.state = nil, chatBusy
				return m, m.gate.WaitOn(m.step.done)
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

// submit starts one agent step for the entered prompt. A line
// starting with / is a REPL command instead and never reaches the
// model.
func (m *chatModel) submit() tea.Cmd {
	prompt := strings.TrimSpace(m.textarea.Value())
	if prompt == "" {
		return nil
	}
	if strings.HasPrefix(prompt, "/") {
		return m.command(prompt)
	}
	// Continuation lines are indented under the prompt sign.
	m.appendLine(userStyle.Render("› " + strings.ReplaceAll(prompt, "\n", "\n  ")))
	m.prompt = prompt
	m.textarea.Reset()
	m.layout() // the input shrank back to one line
	m.tool = ToolEvent{}
	m.state = chatBusy

	// One waiter per channel, scoped to this step: cancel any scope
	// left over from an earlier step, then arm fresh waits that die
	// with the step.
	m.step.cancel()
	m.step = newStepScope()

	step := func() tea.Msg {
		res, err := m.agent.Step(m.ctx, m.history, prompt)
		return stepDoneMsg{res: res, err: err}
	}
	return tea.Batch(step, m.gate.WaitOn(m.step.done), m.feed.WaitOn(m.step.done), m.spinner.Tick)
}

// appendLine adds a rendered line to the transcript and scrolls to
// the bottom.
func (m *chatModel) appendLine(line string) {
	m.lines = append(m.lines, line)
	m.viewport.SetContent(strings.Join(m.lines, "\n"))
	m.viewport.GotoBottom()
}

// modelsDoneMsg carries the outcome of a model listing back to the
// REPL; the listing runs as a Cmd so the UI never blocks on HTTP.
// want names the model a /model command asked to validate and
// switch to; it is empty for plain /models and /backend listings.
type modelsDoneMsg struct {
	names []string
	want  string
	err   error
}

// listModels arms the listing Cmd against the current backend.
func (m *chatModel) listModels(want string) tea.Cmd {
	return func() tea.Msg {
		names, err := m.agent.Models(m.ctx)
		return modelsDoneMsg{names: names, want: want, err: err}
	}
}

// modelAvailable reports the listed name matching want, if any: an
// exact match, or a tag variant of it - ollama accepts "llama3.2"
// as shorthand for "llama3.2:latest". Empty means not available.
func modelAvailable(want string, names []string) string {
	for _, n := range names {
		if n == want || strings.HasPrefix(n, want+":") {
			return n
		}
	}
	return ""
}

// applyModel settles a /model command whose validation listing came
// back: switch when the backend offers the name (or a tag variant
// of it), otherwise say what is available. A step in flight owns
// the agent - setting a.model mid-step would race the running
// loop - so a listing that lands while busy is not applied.
func (m *chatModel) applyModel(want string, names []string) {
	if m.state != chatReady {
		m.appendLine(errorStyle.Render("a step is running - model not changed, retry /model when idle"))
		return
	}
	if modelAvailable(want, names) == "" {
		if len(names) == 0 {
			m.appendLine(errorStyle.Render("no model " + want + " · the backend reported no models"))
			return
		}
		m.appendLine(errorStyle.Render("no model " + want + " · available: " + strings.Join(names, ", ")))
		return
	}
	m.agent.SetModel(want)
	m.appendLine(dimStyle.Render("model set to " + want))
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
		return m.listModels("")

	case "/model":
		model := strings.TrimSpace(rest)
		if model == "" {
			m.appendLine(errorStyle.Render("usage: /model <name> · current: " + m.agent.Model()))
			return nil
		}
		// Validate against the backend's listing before switching:
		// a typo would otherwise only surface as a failed next
		// step. The check runs as a Cmd so the UI never blocks.
		m.appendLine(dimStyle.Render("checking " + model + " against " + m.agent.BackendName() + "'s models…"))
		return m.listModels(model)

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
		// new backend; list what does, so the next step is one
		// /model away.
		m.appendLine(dimStyle.Render("backend switched to " + m.agent.BackendName() + " - listing its models…"))
		return m.listModels("")

	default:
		m.appendLine(errorStyle.Render("unknown command " + name + " · available: /models, /model <name>, /backend <name>"))
		return nil
	}
}

// layout recomputes the viewport height from the window size and the
// current input area height.
func (m *chatModel) layout() {
	input := lipgloss.Height(m.textarea.View())
	h := m.height - lipgloss.Height(m.headerText()) - input - 4
	if h < 1 {
		h = 1
	}
	m.viewport.SetHeight(h)
}

func (m *chatModel) View() tea.View {
	var sb strings.Builder
	sb.WriteString(m.headerText())
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
