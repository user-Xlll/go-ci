package show

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/user-Xlll/go-ci/pkg/modules"
)

var servicesCmd = &cobra.Command{
	Use:   "services",
	Short: "Show all buildable targets",
	Long:  `Show table of services with unique short name.`,
	RunE:  runServicesCmd,
}

func init() {
	RootCmd.AddCommand(servicesCmd)
}

func runServicesCmd(_ *cobra.Command, _ []string) error {
	modulesReport, err := modules.FindModules(workdir)
	if err != nil {
		return fmt.Errorf("find modules error: %w", err)
	}

	keys := slices.Collect(maps.Keys(modulesReport.Services))
	slices.Sort(keys)

	tbWriter := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprintln(tbWriter, "UNIQUE SERVICE NAME", "\t|\t", "BUILD PATH", "\t|\t", "COMMAND", "\t|\t", "OUTPUT REL PATH", "\t|\t", "TYPE")

	for _, k := range keys {
		bi := modulesReport.Services[k]
		rel, err := filepath.Rel(bi.BuildDir, bi.BuildOutputDir)

		if err != nil {
			return fmt.Errorf("get relative path error: %w", err)
		}
		_, _ = fmt.Fprintln(tbWriter, k, "\t|\t", bi.BuildDir, "\t|\t", bi.BuildCommand, "\t|\t", rel, "\t|\t", bi.ModuleInfo.ModuleType())
	}

	if err = tbWriter.Flush(); err != nil {
		return fmt.Errorf("flush tab writer error: %w", err)
	}

	return nil
}
