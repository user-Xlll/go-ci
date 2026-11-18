package cmd

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Version of goci",
	Long:  `Show git tag, used for compilation thits tool.`,
	RunE:  runVersionCmd,
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

func runVersionCmd(_ *cobra.Command, _ []string) error {
	version := "unknown"

	if info, available := debug.ReadBuildInfo(); available {
		version = info.Main.Version
	}

	_, _ = fmt.Fprintln(os.Stdout, "goci version:", version)

	return nil
}
