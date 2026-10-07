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
version and SPDX license identifier. Modules and versions come from
the build info embedded in this binary; license texts are read from
the local Go module cache.`,
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
