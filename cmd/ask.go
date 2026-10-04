package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
	"go-reins/internal/backend/llamacpp"
	"go-reins/internal/backend/ollama"
	"go-reins/internal/logging"
	"go-reins/internal/tools/shell"
)

const defaultSystemPrompt = `You are a helpful assistant running inside a small agent harness.
Answer concisely and accurately.`

var askCmd = &cobra.Command{
	Use:   "ask [prompt]",
	Short: "Send a single prompt to the agent and print the answer",
	Long: `ask sends the given prompt through the agent loop and prints the
final answer. Example:

	go-reins ask --model llama3.2 "explain the CAP theorem in 3 sentences"`,
	Args: cobra.ExactArgs(1),
	RunE: runAsk,
}

func init() {
	rootCmd.AddCommand(askCmd)
}

func runAsk(cmd *cobra.Command, args []string) error {
	logger, err := logging.New(viper.GetString("log-level"))
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	b, err := newBackend()
	if err != nil {
		return err
	}

	model := viper.GetString("model")
	if model == "" {
		return fmt.Errorf("no model set: use --model, config file, or GO_REINS_MODEL")
	}

	logger.Info("starting ask",
		zap.String("backend", b.Name()),
		zap.String("model", model))

	tools := []agent.Tool{shell.New()}
	a := agent.New(b, model, defaultSystemPrompt, tools,
		agent.WithApprover(confirmApprover()),
		agent.WithLogger(logger),
	)

	res, err := a.Run(context.Background(), args[0])
	if err != nil {
		return err
	}

	fmt.Println(res.Answer)
	if viper.GetBool("turns") {
		fmt.Fprintf(os.Stderr, "\nrun finished in %d turn(s)\n", res.Turns)
	}
	if viper.GetBool("history") {
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

// confirmApprover is the human in the loop: every tool call is shown
// on stderr and must be confirmed. It returns nil (allow everything)
// when --yes is set.
func confirmApprover() agent.Approver {
	if viper.GetBool("yes") {
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

// newBackend builds the backend selected by config, flag, or env.
func newBackend() (backend.Backend, error) {
	cfg := backend.Config{
		BaseURL: viper.GetString("url"),
		Model:   viper.GetString("model"),
	}
	switch viper.GetString("backend") {
	case "ollama":
		return ollama.New(cfg), nil
	case "llamacpp":
		return llamacpp.New(cfg), nil
	default:
		return nil, fmt.Errorf("unknown backend %q: expected ollama or llamacpp", viper.GetString("backend"))
	}
}
