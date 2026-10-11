package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"go-reins/internal/buildinfo"
)

// version is the go-reins release identifier. The default is
// overridden at build time; the Makefile stamps in git describe
// output or an explicit version:
//
//	go build -ldflags "-X go-reins/cmd.version=1.2.3" .
var version = "dev"

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Print build information about this binary",
	Long: `info reports how this binary was built: the release version
(stamped in at build time via -ldflags), the git revision and build
time (embedded by the Go toolchain when building from a git
repository), the Go version, and the platform.`,
	Args: cobra.NoArgs,
	RunE: printBuildInfo,
}

func init() {
	rootCmd.AddCommand(infoCmd)
}

func printBuildInfo(cmd *cobra.Command, args []string) error {
	bi := buildinfo.Collect(version)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PROPERTY\tVALUE")
	fmt.Fprintf(w, "version\t%s\n", bi.Version)
	if !bi.Available {
		fmt.Fprintln(w, "build info\tnot available (built without modules)")
		return w.Flush()
	}

	fmt.Fprintf(w, "module\t%s (%s)\n", bi.Module, bi.ModuleVersion)
	if bi.Commit != "" {
		fmt.Fprintf(w, "commit\t%s\n", bi.Commit)
	}
	if bi.Built != "" {
		fmt.Fprintf(w, "built\t%s\n", bi.Built)
	}
	fmt.Fprintf(w, "go\t%s\n", bi.Go)
	fmt.Fprintf(w, "platform\t%s\n", bi.Platform)
	return w.Flush()
}
