package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
	"go-reins/internal/backend/llamacpp"
	"go-reins/internal/backend/ollama"
	"go-reins/internal/logging"
	"go-reins/internal/tools/shell"
	"go-reins/internal/tui"
)

const defaultSystemPrompt = `You are a helpful assistant running inside a small agent harness.
Answer concisely and accurately.`

var runCmd = &cobra.Command{
	Use:   "run [task]",
	Short: "Run a task through the agent loop and print the result",
	Long: `run sends the given task through the agent loop and prints the
final answer. The agent may call tools (subject to approval) along the
way. Example:

	go-reins run --model llama3.2 "figure out which system you run on"`,
	Args: cobra.ExactArgs(1),
	RunE: runTask,
}

func init() {
	// --history and --turns report on the one task this command
	// performs; chat has its own live transcript instead.
	runCmd.Flags().Bool("history", false, "print the full conversation history (with roles) after the answer")
	runCmd.Flags().Bool("turns", false, "print how many turns the run took")
	rootCmd.AddCommand(runCmd)
}

func runTask(cmd *cobra.Command, args []string) error {
	if tui.Interactive() {
		return runInteractive(args[0])
	}
	return runHeadless(args[0])
}

// runInteractive drives the task through the bubbletea run view: a
// spinner while the model works, an approval dialog on each tool
// call, and the answer as the final frame.
func runInteractive(task string) error {
	gate := tui.NewApprovalGate()
	a, err := buildAgent(approverFor(gate))
	if err != nil {
		return err
	}

	res, err := tui.RunTask(a, gate, task)
	if err != nil {
		return err
	}
	if conf.Turns {
		fmt.Fprintf(os.Stderr, "\nrun finished in %d turn(s)\n", res.Turns)
	}
	if conf.History {
		printHistory(res.History)
	}
	return nil
}

// runHeadless is the non-TTY path: plain output on stdout and the
// classic y/N prompt on stdin, so `go-reins run` stays scriptable.
func runHeadless(task string) error {
	a, err := buildAgent(stdinApprover())
	if err != nil {
		return err
	}

	res, err := a.Run(context.Background(), task)
	if err != nil {
		return err
	}
	fmt.Println(res.Answer)
	if conf.Turns {
		fmt.Fprintf(os.Stderr, "\nrun finished in %d turn(s)\n", res.Turns)
	}
	if conf.History {
		printHistory(res.History)
	}
	return nil
}

// printHistory dumps the conversation for review: one block per
// message, labeled with its role.
func printHistory(history []backend.Message) {
	fmt.Fprintln(os.Stderr, "\n--- conversation history ---")
	for i, m := range history {
		fmt.Fprintf(os.Stderr, "\n[%d] %s\n%s\n", i+1, m.Role, m.Content)
	}
}

// stdinApprover is the human in the loop without a TUI: every tool
// call is shown on stderr and must be confirmed on stdin. It returns
// nil (allow everything) when --yes is set.
func stdinApprover() agent.Approver {
	if conf.Yes {
		return nil
	}
	return func(name, args string) bool {
		fmt.Fprintf(os.Stderr, "\ntool call: %s %s\n", name, args)
		fmt.Fprint(os.Stderr, "allow? [y/N]: ")

		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(os.Stderr, "(could not read confirmation, defaulting to deny)")
			return false
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true
		default:
			fmt.Fprintln(os.Stderr, "denied")
			return false
		}
	}
}

// approverFor returns the gate-backed approver for the TUI views, or
// nil (allow everything) when --yes is set.
func approverFor(gate *tui.ApprovalGate) agent.Approver {
	if conf.Yes {
		return nil
	}
	return gate.Approver()
}

// buildAgent assembles the agent: backend and model from the
// resolved config, the shell tool, the logger, and the approver.
func buildAgent(approve agent.Approver) (*agent.Agent, error) {
	logger, err := logging.New(conf.LogLevel)
	if err != nil {
		return nil, err
	}

	b, err := newBackend()
	if err != nil {
		return nil, err
	}

	if conf.Model == "" {
		return nil, fmt.Errorf("no model set: use --model, config file, or GO_REINS_MODEL")
	}

	opts := []agent.Option{agent.WithLogger(logger)}
	if approve != nil {
		opts = append(opts, agent.WithApprover(approve))
	}

	logger.Info("starting session",
		zap.String("backend", b.Name()),
		zap.String("model", conf.Model))

	return agent.New(b, conf.Model, defaultSystemPrompt, []agent.Tool{shell.New()}, opts...), nil
}

// newBackend builds the backend selected by config.
func newBackend() (backend.Backend, error) {
	cfg := backend.Config{
		BaseURL: conf.URL,
		Model:   conf.Model,
	}
	switch conf.Backend {
	case "ollama":
		return ollama.New(cfg), nil
	case "llamacpp":
		return llamacpp.New(cfg), nil
	default:
		return nil, fmt.Errorf("unknown backend %q: expected ollama or llamacpp", conf.Backend)
	}
}
