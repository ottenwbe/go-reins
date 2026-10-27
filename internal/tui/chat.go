package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

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

// String names the state for the debug overlay.
func (s chatState) String() string {
	switch s {
	case chatReady:
		return "ready"
	case chatBusy:
		return "busy"
	case chatApproving:
		return "approving"
	}
	return "unknown"
}

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
	stream        *StreamFeed             // may be nil: no observer wired, nothing armed
	version       string                  // ldflags-stamped release version, shown by /info
	title         string                  // session line prefix; the live header appends backend/model
	switchBackend func(name string) error // wires /backend to session.NewBackend
	prompt        string                  // prompt of the running step, for the status line

	history    []backend.Message
	lines      []string
	live       string // streamed reply of the running step, shown as a forming line in the transcript
	viewport   viewport.Model
	textarea   textarea.Model
	spinner    spinner.Model
	state      chatState
	approval   *ApprovalRequest
	tool       ToolEvent // tool of the running step, for the status line
	step       *stepScope
	modelSeq   int  // generation of the newest model-deciding command (/model, /backend); older results are dropped
	appliedSeq int  // generation of the newest command that actually selected a model; only that suppresses the switch fallback
	compacting bool // /compact round trip in flight; shows in the status word and settles its own message
	debug      bool // /debug: show the state overlay in the status area; off unless asked for
	width      int
	height     int
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

func newChatModel(a *agent.Agent, gate *ApprovalGate, feed *ToolFeed, stream *StreamFeed, version, title string, switchBackend func(string) error) *chatModel {
	ctx, cancel := context.WithCancel(context.Background())

	ta := textarea.New()
	ta.Placeholder = "type a task or /models, /model, /backend, /compact, /info" + debugCommands + " · ctrl+j for a new line · ctrl+c quits"
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
		stream:        stream,
		version:       version,
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
// show which tool is in use; the stream feed (may be nil) receives
// the reply text as it arrives, so the answer forms in the
// transcript itself. switchBackend (may be nil) connects
// the /backend command to the config-backed backend constructors.
func Chat(a *agent.Agent, gate *ApprovalGate, feed *ToolFeed, stream *StreamFeed, version, title string, switchBackend func(string) error) error {
	defer feed.Close()
	if stream != nil {
		defer stream.Close()
	}
	_, err := tea.NewProgram(newChatModel(a, gate, feed, stream, version, title, switchBackend)).Run()
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

// Update is the single entry point bubbletea calls with every
// message; it traces the message for the debug overlay and hands it
// to the handler for its type. One handler per message type keeps
// each state transition readable on its own; the handlers mutate
// the model in place, so the returned model is always m.
func (m *chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// With the debug overlay on (debugui build only), every message
	// leaves a trace in the transcript before its effect - including
	// the ones the state guards below drop, which is the point.
	m.debugMsg(msg)
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, m.updateWindowSize(msg)
	case ToolEvent:
		return m, m.updateToolEvent(msg)
	case StreamEvent:
		return m, m.updateStreamEvent(msg)
	case stepDoneMsg:
		return m, m.updateStepDone(msg)
	case modelsDoneMsg:
		return m, m.updateModelsDone(msg)
	case ApprovalRequest:
		return m, m.updateApprovalRequest(msg)
	case compactDoneMsg:
		return m, m.updateCompactDone(msg)
	case spinner.TickMsg:
		return m, m.updateSpinnerTick(msg)
	case tea.KeyPressMsg:
		return m, m.updateKey(msg)
	}

	// Non-key messages keep the textarea cursor blinking.
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// updateWindowSize applies a new terminal size to the transcript
// and the input area.
func (m *chatModel) updateWindowSize(msg tea.WindowSizeMsg) tea.Cmd {
	m.width, m.height = msg.Width, msg.Height
	m.viewport.SetWidth(msg.Width)
	m.textarea.SetWidth(msg.Width)
	m.layout()
	return nil
}

// updateToolEvent shows the tool the running step is using and
// keeps the feed listening. A tool event from a step that already
// ended (racing the final answer) is noise: drop it instead of
// reviving the status line and arming a new waiter while idle.
func (m *chatModel) updateToolEvent(msg ToolEvent) tea.Cmd {
	if m.state != chatBusy {
		return nil
	}
	m.tool = msg
	return m.feed.WaitOn(m.step.done)
}

// updateStreamEvent grows the forming reply in the transcript. A
// delta from a step that already ended is noise: drop it instead
// of reviving the transcript and arming a new waiter while idle. A
// delta while the operator decides a tool call belongs to the
// running step - prose the model streamed before the call - so it
// keeps the forming line growing and the waiter armed; dropping it
// there would kill the wait chain and freeze the rest of the reply.
func (m *chatModel) updateStreamEvent(msg StreamEvent) tea.Cmd {
	if m.state != chatBusy && m.state != chatApproving {
		return nil
	}
	m.live = appendLive(m.live, msg.Delta)
	m.refreshTranscript()
	return m.stream.WaitOn(m.step.done)
}

// updateStepDone settles the finished step: cancel its waiters so
// they cannot linger into the next prompt, drop whatever the
// observer wrote in the race window around the step's end so it
// cannot be attributed to the next reply, then show the outcome.
// The state settles before the outcome is appended so the rerender
// drops the forming line - the final answer replaces it in the
// same spot.
func (m *chatModel) updateStepDone(msg stepDoneMsg) tea.Cmd {
	m.step.cancel()
	m.step = nil
	if m.stream != nil {
		m.stream.Discard()
	}
	m.prompt, m.tool, m.live, m.state = "", ToolEvent{}, "", chatReady
	m.layout() // the input area returns: the textarea replaces the pause line
	if msg.err != nil {
		m.appendLine(errorStyle.Render(Sanitize("error: " + msg.err.Error())))
	} else {
		m.history = msg.res.History
		m.appendLine(answerStyle.Render(Sanitize(msg.res.Answer)))
		if stats := usageLine(msg.res); stats != "" {
			m.appendLine(dimStyle.Render(stats))
		}
	}
	return nil
}

// updateModelsDone settles a listing from a /models or /backend
// command, or the validation behind a /model. One that raced a
// /backend switch describes a backend the session no longer uses:
// drop it, or a stale /model validation could set a name the new
// backend may not serve.
func (m *chatModel) updateModelsDone(msg modelsDoneMsg) tea.Cmd {
	if msg.backend != m.agent.BackendName() {
		return nil
	}
	if msg.err != nil {
		m.appendLine(errorStyle.Render(Sanitize("error: " + msg.err.Error())))
		return nil
	}
	if msg.want != "" {
		// Two /model commands against the same backend can have
		// their listings finish out of order: apply only the newest
		// generation, or an older listing landing last would
		// override the operator's later choice.
		if msg.seq != m.modelSeq {
			return nil
		}
		if m.applyModel(msg.want, msg.names) {
			// A real selection settles the model for this
			// generation; a refusal or a busy deferral does not,
			// and must leave the switch's fallback free to act.
			m.appliedSeq = msg.seq
		}
		return nil
	}
	if len(msg.names) == 0 {
		m.appendLine(dimStyle.Render("(the backend reported no models)"))
		return nil
	}
	m.appendLine(dimStyle.Render(Sanitize(strings.Join(msg.names, "\n"))))
	// The switch's follow-up reconciles only when no newer /model
	// actually selected a model: a command that was refused never
	// decided anything, and suppressing the fallback then would
	// leave the session on a model this backend does not serve.
	// The names still print above - they are information, not a
	// decision.
	if msg.switched && msg.seq > m.appliedSeq {
		m.reconcileModel(msg.names)
		m.appliedSeq = msg.seq
	}
	return nil
}

// updateApprovalRequest parks the running step for the operator's
// decision.
func (m *chatModel) updateApprovalRequest(msg ApprovalRequest) tea.Cmd {
	m.approval, m.state = &msg, chatApproving
	m.layout() // the input area pauses for the decision
	m.appendLine(toolStyle.Render(Sanitize("tool call: " + msg.Tool + " " + msg.Args)))
	return nil
}

// updateSpinnerTick animates the spinner while a step runs; in any
// other state the tick dies out and the spinner stops animating.
func (m *chatModel) updateSpinnerTick(msg spinner.TickMsg) tea.Cmd {
	if m.state == chatBusy {
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return cmd
	}
	return nil
}

// updateKey routes one keystroke by state: ctrl+c always quits; an
// approval consumes y/n; a running step swallows everything;
// otherwise enter submits, scroll keys reach the transcript, and
// typing goes to the input area.
func (m *chatModel) updateKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		m.cancel()
		m.gate.Close()
		m.feed.Close()
		if m.stream != nil {
			m.stream.Close()
		}
		m.step.cancel()
		return tea.Quit
	}
	switch m.state {
	case chatApproving:
		return m.updateApprovalKey(msg)
	case chatBusy:
		return nil // swallow keys while the agent works
	}
	if msg.String() == "enter" {
		return m.submit()
	}
	// Scrolling keys go to the transcript, everything else to the
	// input area.
	switch msg.String() {
	case "pgup", "pgdown", "home", "end":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return cmd
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.layout() // the input may have grown or shrunk a line
	return cmd
}

// updateApprovalKey answers the pending tool call - allow on
// y/enter, deny on n/esc - and hands the step back to busy, with
// the gate listening for the next request.
func (m *chatModel) updateApprovalKey(msg tea.KeyPressMsg) tea.Cmd {
	var allow bool
	switch msg.String() {
	case "y", "Y", "enter":
		allow = true
		m.appendLine(dimStyle.Render("allowed"))
	case "n", "N", "esc":
		m.appendLine(dimStyle.Render("denied"))
	default:
		return nil
	}
	m.approval.Reply <- allow
	m.approval, m.state = nil, chatBusy
	m.layout() // still paused, but the decision settled
	return m.gate.WaitOn(m.step.done)
}

// shorten caps the rendered args to a single status line; tool-call
// arguments are single-line JSON by protocol. The cut lands on a
// rune boundary, so truncation never splits a multibyte character.
func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}

// maxLive caps the streamed text the forming transcript line keeps.
// The final answer lands in the transcript from the step result
// regardless, so the live buffer only serves the in-progress view -
// long replies show their tail.
const maxLive = 8 * 1024

// appendLive grows the live text, capped to its tail on a rune
// boundary: one step cannot grow the view buffer without bound.
func appendLive(live, delta string) string {
	live += delta
	if len(live) <= maxLive {
		return live
	}
	cut := len(live) - maxLive
	for cut < len(live) && !utf8.RuneStart(live[cut]) {
		cut++
	}
	return live[cut:]
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
	m.layout() // the input area pauses for the step

	// One waiter per channel, scoped to this step: cancel any scope
	// left over from an earlier step, then arm fresh waits that die
	// with the step.
	m.step.cancel()
	m.step = newStepScope()

	step := func() tea.Msg {
		res, err := m.agent.Step(m.ctx, m.history, prompt)
		return stepDoneMsg{res: res, err: err}
	}
	cmds := []tea.Cmd{step, m.gate.WaitOn(m.step.done), m.feed.WaitOn(m.step.done), m.spinner.Tick}
	if m.stream != nil {
		cmds = append(cmds, m.stream.WaitOn(m.step.done))
	}
	return tea.Batch(cmds...)
}

// appendLine adds a rendered line to the transcript and scrolls to
// the bottom.
func (m *chatModel) appendLine(line string) {
	m.lines = append(m.lines, line)
	m.refreshTranscript()
}

// refreshTranscript renders the transcript into the viewport: the
// finished lines, plus - while a step is streaming - the forming
// reply as one more line at the bottom, approval dialog included.
// The step result replaces it wholesale, so the completed answer
// never doubles up; live is empty whenever no step is running.
func (m *chatModel) refreshTranscript() {
	content := strings.Join(m.lines, "\n")
	if m.live != "" {
		content += "\n" + answerStyle.Render(Sanitize(m.live))
	}
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

// modelsDoneMsg carries the outcome of a model listing back to the
// REPL; the listing runs as a Cmd so the UI never blocks on HTTP.
// want names the model a /model command asked to validate and
// switch to (empty for plain listings), seq is the generation of
// the model-deciding command that armed it (0 for plain listings,
// which only print), backend names the backend the listing ran
// against so a result that raced a /backend switch is dropped,
// and switched marks the listing a /backend switch armed to follow
// up on its model.
type modelsDoneMsg struct {
	names    []string
	want     string
	seq      int
	backend  string
	switched bool
	err      error
}

// listModels arms the listing Cmd against the current backend. The
// backend is captured here, in the Update loop - not read inside
// the Cmd - so a /backend switch racing the listing never writes
// a.backend under it; the name tag on the msg lets Update drop a
// listing that no longer describes the session's backend.
func (m *chatModel) listModels(want string, seq int, switched bool) tea.Cmd {
	b := m.agent.Backend()
	name := b.Name()
	return func() tea.Msg {
		names, err := b.Models(m.ctx)
		return modelsDoneMsg{names: names, want: want, seq: seq, backend: name, switched: switched, err: err}
	}
}

// modelAvailable reports the listed name matching want, if any: an
// exact match, or a tag variant of it - ollama accepts "llama3.2"
// as shorthand for "llama3.2:latest". Callers set the returned
// listed name, never the typed shorthand, so a request cannot fall
// back to a tag the backend may not serve. Empty means not
// available.
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
// of it, which is what gets set), otherwise say what is available.
// It reports whether a model was selected - only a selection
// suppresses a pending switch's fallback reconciliation. A step in
// flight owns the agent - setting a.model mid-step would race the
// running loop - so a listing that lands while busy is not applied.
func (m *chatModel) applyModel(want string, names []string) bool {
	if m.state != chatReady {
		m.appendLine(errorStyle.Render("a step is running - model not changed, retry /model when idle"))
		return false
	}
	match := modelAvailable(want, names)
	if match == "" {
		if len(names) == 0 {
			m.appendLine(errorStyle.Render(Sanitize("no model " + want + " · the backend reported no models")))
			return false
		}
		m.appendLine(errorStyle.Render(Sanitize("no model " + want + " · available: " + strings.Join(names, ", "))))
		return false
	}
	m.agent.SetModel(match)
	m.appendLine(dimStyle.Render(Sanitize("model set to " + match)))
	return true
}

// reconcileModel settles the model after the listing that follows
// a /backend switch: the old model survives only if the new
// backend serves it (or a tag variant of it, in which case the
// listed variant is what gets set); otherwise the first listed
// model takes over, so the next prompt works without a manual
// /model. Like applyModel, it defers to a running step - the step
// owns the agent's model.
func (m *chatModel) reconcileModel(names []string) {
	if m.state != chatReady {
		m.appendLine(dimStyle.Render("(model left as " + m.agent.Model() + " - a step is running, /model once idle)"))
		return
	}
	old := m.agent.Model()
	match := modelAvailable(old, names)
	if match != "" {
		// Served, possibly only as a specific tag: send the listed
		// name so the request does not fall back to a tag the
		// backend may not have.
		if match != old {
			m.agent.SetModel(match)
			m.appendLine(dimStyle.Render(Sanitize(old + " resolves to " + match + " here - model set to " + match)))
		}
		return
	}
	m.agent.SetModel(names[0])
	m.appendLine(dimStyle.Render(Sanitize(old + " is not served here - switched to " + names[0])))
}

// layout recomputes the viewport height from the window size and
// the current input area height: the textarea while the REPL is
// ready, the single pause line while it is not.
func (m *chatModel) layout() {
	input := 1 // the paused input is one line
	if m.state == chatReady {
		input = lipgloss.Height(m.textarea.View())
	}
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
			sb.WriteString(toolStyle.Render(Sanitize(m.tool.Name)))
			if m.tool.Args != "" {
				sb.WriteString(" ")
				sb.WriteString(dimStyle.Render(shorten(Sanitize(m.tool.Args), 60)))
			}
		} else if m.compacting {
			sb.WriteString(dimStyle.Render("compacting…"))
		} else if m.live == "" {
			sb.WriteString(dimStyle.Render("thinking…"))
		} else {
			sb.WriteString(dimStyle.Render("answering…"))
		}
	case chatApproving:
		sb.WriteString(fmt.Sprintf("allow? %s",
			dimStyle.Render("[y] allow · [n] deny")))
	default:
		sb.WriteString(dimStyle.Render("enter to send · ctrl+j new line · pgup/pgdown scroll · ctrl+c quit"))
	}
	if m.debug {
		sb.WriteString(m.debugOverlay())
	}
	sb.WriteString("\n\n")
	sb.WriteString(m.inputView())

	v := tea.NewView(sb.String())
	v.AltScreen = true
	return v
}

// inputView renders the input area: the textarea while the REPL
// accepts prompts, and a one-line pause note while it does not -
// the agent working or an approval pending means keystrokes are
// swallowed, and a focused-looking input that ignores typing
// reads as broken instead of busy.
func (m *chatModel) inputView() string {
	if m.state != chatReady {
		return dimStyle.Render("(input paused · ctrl+c aborts)")
	}
	return m.textarea.View()
}
