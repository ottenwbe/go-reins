package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"go-reins/internal/agent"
	"go-reins/internal/session"
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
	if !tui.Interactive() {
		return fmt.Errorf("chat requires an interactive terminal")
	}

	gate := tui.NewApprovalGate()
	feed := tui.NewToolFeed()
	a, err := session.NewAgent(conf, session.GateApprover(gate, conf),
		agent.WithToolObserver(feed.Observer()))
	if err != nil {
		return err
	}

	// /backend swaps the adapter mid-session: same constructor and
	// validation as startup, applied to the live agent. The
	// configured --url belongs to the startup backend and is not
	// carried to the new one, which applies its own default; the
	// conversation history stays valid.
	switchBackend := func(name string) error {
		b, err := session.NewBackend(conf, name)
		if err != nil {
			return err
		}
		a.SetBackend(b)
		return nil
	}

	return tui.Chat(a, gate, feed, "go-reins chat", switchBackend)
}
