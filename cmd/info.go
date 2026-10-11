package cmd

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"text/tabwriter"

	"github.com/spf13/cobra"
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
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PROPERTY\tVALUE")
	fmt.Fprintf(w, "version\t%s\n", version)

	info, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Fprintln(w, "build info\tnot available (built without modules)")
		return w.Flush()
	}

	fmt.Fprintf(w, "module\t%s (%s)\n", info.Main.Path, info.Main.Version)

	rev, built, modified := vcsSettings(info)
	if rev != "" {
		fmt.Fprintf(w, "commit\t%s\n", commitLabel(rev, modified))
	}
	if built != "" {
		fmt.Fprintf(w, "built\t%s\n", built)
	}

	fmt.Fprintf(w, "go\t%s\n", info.GoVersion)
	fmt.Fprintf(w, "platform\t%s/%s\n", runtime.GOOS, runtime.GOARCH)
	return w.Flush()
}

// vcsSettings pulls the git revision, commit time, and dirty flag
// the Go toolchain embeds when building from a git repository
// (-buildvcs defaults to on). All three are empty for source
// tarball builds.
func vcsSettings(info *debug.BuildInfo) (revision, built, modified string) {
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			built = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	return revision, built, modified
}

// commitLabel shortens the full SHA to something readable and
// marks a build from a dirty working tree.
func commitLabel(rev, modified string) string {
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if modified == "true" {
		return rev + " (dirty)"
	}
	return rev
}
