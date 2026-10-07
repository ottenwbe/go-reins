package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"go-reins/internal/config"
)

var rootCmd = &cobra.Command{
	Use:   "go-reins",
	Short: "A minimal AI agent harness with pluggable local backends",
	Long: `go-reins is a learning project: a small agent harness that talks to
local inference backends (Ollama, llama.cpp) through a swappable interface.`,
	// Runtime errors are printed by Execute; the usage dump is noise.
	SilenceUsage: true,
	// Resolving configuration here means every subcommand sees the
	// same precedence chain: flags > env > config file > defaults.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cmd.Flags())
		if err != nil {
			return err
		}
		conf = cfg
		return nil
	},
}

// conf holds the resolved configuration for the running command.
var conf config.Config

// Execute runs the root command. A failure here is the program's
// final user-facing output, so it is printed plainly rather than
// logged: the logger is built per command from the resolved config,
// which does not exist when config loading itself failed.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "go-reins:", err)
		os.Exit(1)
	}
}

func init() {
	flags := rootCmd.PersistentFlags()
	flags.String("config", "", "config file (default is $HOME/.go-reins.yaml)")
	flags.String("backend", "ollama", "inference backend: ollama or llamacpp")
	flags.String("url", "", "backend base URL (defaults: ollama http://localhost:11434, llamacpp http://localhost:8080)")
	flags.String("model", "", "model name, e.g. llama3.2 or qwen2.5")
	flags.Bool("yes", false, "auto-approve tool calls; without this flag every tool call is shown for confirmation")
	flags.String("log-level", "error", "log level: debug, info, warn, error (logs go to stderr)")
}
