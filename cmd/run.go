package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
	"go-reins/internal/session"
	"go-reins/internal/tui"
)

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
	stream := tui.NewStreamFeed()
	opts := []agent.Option{}
	if !conf.NoStream {
		opts = append(opts, agent.WithStreamObserver(stream.Observer()))
	}
	a, err := session.NewAgent(conf, session.GateApprover(gate, conf), opts...)
	if err != nil {
		return err
	}

	res, err := tui.RunTask(a, gate, stream, task)
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
	a, err := session.NewAgent(conf, stdinApprover(os.Stdin, os.Stderr))
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
// nil (allow everything) when --yes is set. The in/out parameters
// carry the streams so tests can drive it; production passes
// os.Stdin and os.Stderr.
func stdinApprover(in io.Reader, out io.Writer) agent.Approver {
	if conf.Yes {
		return nil
	}
	// One buffered reader for the whole run: building a fresh
	// bufio.Reader per call would swallow piped answers into its
	// buffer and lose them (printf 'y\ny\n' | go-reins run ...).
	reader := bufio.NewReader(in)
	return func(name, args string) bool {
		fmt.Fprintf(out, "\ntool call: %s %s\n", name, args)
		fmt.Fprint(out, "allow? [y/N]: ")

		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			// Nobody answered on stdin: cron, CI, < /dev/null.
			// Deny, and tell the operator how to run unattended -
			// automation opts in with --yes; auto-approval is
			// never the default.
			fmt.Fprintln(out, "(no confirmation available, defaulting to deny; for unattended runs, pass --yes)")
			return false
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true
		default:
			fmt.Fprintln(out, "denied")
			return false
		}
	}
}
