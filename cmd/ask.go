package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
	"go-reins/internal/backend/llamacpp"
	"go-reins/internal/backend/ollama"
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
	b, err := newBackend()
	if err != nil {
		return err
	}

	model := viper.GetString("model")
	if model == "" {
		return fmt.Errorf("no model set: use --model, config file, or GO_REINS_MODEL")
	}

	a := agent.New(b, model, defaultSystemPrompt, nil)

	answer, err := a.Run(context.Background(), args[0])
	if err != nil {
		return err
	}

	fmt.Println(answer)
	return nil
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
