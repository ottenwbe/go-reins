package cmd

import (
	"bufio"
	"context"
	"fmt"
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
	a, err := session.NewAgent(conf, session.GateApprover(gate, conf))
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
	a, err := session.NewAgent(conf, stdinApprover())
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
