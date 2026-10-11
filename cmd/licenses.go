package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"go-reins/internal/licenses"
)

var licensesCmd = &cobra.Command{
	Use:   "licenses",
	Short: "Print the open source licenses of the dependencies",
	Long: `licenses lists every module go-reins links against with its
version and SPDX license identifier. The data comes from a CycloneDX
SBOM embedded in this binary at build time (make build regenerates
it), so the command needs no Go toolchain and no module cache on
the machine it runs on.

EXPERIMENTAL: this command and the SBOM machinery behind it are a
learning ground for what Go and a CLI can do - go:embed, build
info, the module cache, the CycloneDX schema. Do not rely on it for
license compliance decisions.`,
	Args: cobra.NoArgs,
	RunE: printLicenses,
}

func init() {
	rootCmd.AddCommand(licensesCmd)
}

func printLicenses(cmd *cobra.Command, args []string) error {
	entries, err := licenses.List()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "MODULE\tVERSION\tLICENSE")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Module, e.Version, e.License)
	}
	return w.Flush()
}
