package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "go-reins",
	Short: "A minimal AI agent harness with pluggable local backends",
	Long: `go-reins is a learning project: a small agent harness that talks to
local inference backends (Ollama, llama.cpp) through a swappable interface.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.go-reins.yaml)")
	rootCmd.PersistentFlags().String("backend", "ollama", "inference backend: ollama or llamacpp")
	rootCmd.PersistentFlags().String("url", "", "backend base URL (defaults: ollama http://localhost:11434, llamacpp http://localhost:8080)")
	rootCmd.PersistentFlags().String("model", "", "model name, e.g. llama3.2 or qwen2.5")
	rootCmd.PersistentFlags().Bool("yes", false, "auto-approve tool calls; without this flag every tool call is shown for confirmation")
	rootCmd.PersistentFlags().Bool("history", false, "print the full conversation history (with roles) after the answer")
	rootCmd.PersistentFlags().Bool("turns", false, "print how many turns the run took")

	_ = viper.BindPFlag("backend", rootCmd.PersistentFlags().Lookup("backend"))
	_ = viper.BindPFlag("url", rootCmd.PersistentFlags().Lookup("url"))
	_ = viper.BindPFlag("model", rootCmd.PersistentFlags().Lookup("model"))
	_ = viper.BindPFlag("yes", rootCmd.PersistentFlags().Lookup("yes"))
	_ = viper.BindPFlag("history", rootCmd.PersistentFlags().Lookup("history"))
	_ = viper.BindPFlag("turns", rootCmd.PersistentFlags().Lookup("turns"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot resolve home directory:", err)
			os.Exit(1)
		}
		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".go-reins")
	}

	// Environment overrides: GO_REINS_BACKEND, GO_REINS_URL, GO_REINS_MODEL.
	viper.SetEnvPrefix("GO_REINS")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "using config file:", viper.ConfigFileUsed())
	}
}
