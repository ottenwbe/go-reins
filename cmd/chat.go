package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"go-reins/internal/tui"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with the agent",
	Long: `chat runs an interactive REPL: type tasks, watch the agent work,
approve tool calls, and keep the conversation alive for the whole
session. Quit with ctrl+c.`,
	Args: cobra.NoArgs,
	RunE: chatSession,
}

func init() {
	rootCmd.AddCommand(chatCmd)
}

func chatSession(cmd *cobra.Command, args []string) error {
	if !interactive() {
		return fmt.Errorf("chat requires an interactive terminal")
	}

	gate := tui.NewApprovalGate()
	a, err := buildAgent(approverFor(gate))
	if err != nil {
		return err
	}

	header := fmt.Sprintf("go-reins chat · %s/%s", conf.Backend, conf.Model)
	return tui.Chat(a, gate, header)
}
