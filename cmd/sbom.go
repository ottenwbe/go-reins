package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"go-reins/internal/licenses"
)

// genSbomCmd emits the CycloneDX SBOM of the running binary. It is
// the build-time half of the licenses story: `make sbom` runs it and
// bakes the output into the binary, where the licenses command reads
// it back. Hidden because it is a build tool, not user interface.
var genSbomCmd = &cobra.Command{
	Use:    "gen-sbom",
	Short:  "Emit the CycloneDX SBOM of this binary to stdout",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := licenses.Resolve()
		if err != nil {
			return err
		}
		out, err := licenses.WriteCycloneDX("go-reins", entries)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
	},
}

func init() {
	rootCmd.AddCommand(genSbomCmd)
}
