package show

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/user-Xlll/go-ci/pkg/modules"
	"github.com/user-Xlll/go-ci/pkg/modules/gomod"
	"github.com/user-Xlll/go-ci/pkg/modules/npm"
)

var modulesCmd = &cobra.Command{
	Use:   "modules",
	Short: "Show all modules",
	Long:  `Show all modules for load dependies.`,
	RunE:  runModulesCmd,
}

func init() {
	RootCmd.AddCommand(modulesCmd)
}

func runModulesCmd(_ *cobra.Command, _ []string) error {
	modulesReport, err := modules.FindModules(workdir)
	if err != nil {
		return fmt.Errorf("find modules error: %w", err)
	}

	keys := slices.Collect(maps.Keys(modulesReport.Module2ShortName))
	slices.SortFunc(keys, func(a, b modules.IModuleInfo) int {
		return strings.Compare(modulesReport.ModuleShortName(a), modulesReport.ModuleShortName(b))
	})

	tbWriter := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	_, _ = fmt.Fprintln(tbWriter, "UNIQUE MODULE NAME", "\t|\t", "MODULE PATH", "\t|\t", "BUILD REL PATHS", "\t|\t", "TYPE")

	for _, k := range keys {
		shortName := modulesReport.ModuleShortName(k)

		var rels []string

		switch tp := k.(type) {
		case *npm.ModuleInfo:
			for _, outs := range tp.Scripts {
				for _, out := range outs {
					rel, err := filepath.Rel(k.ModulePath(), out.BuildDir)

					if err != nil {
						return fmt.Errorf("get relative path error: %w", err)
					}

					rels = append(rels, rel)
				}
			}

		case *gomod.ModuleInfo:
			rels = tp.MainSubPaths
		default:
			panic("unsupported module type " + k.ModuleType())
		}

		_, _ = fmt.Fprintln(tbWriter, shortName, "\t|\t", k.ModulePath(), "\t|\t", strings.Join(rels, ", "), "\t|\t", k.ModuleType())
	}

	if err = tbWriter.Flush(); err != nil {
		return fmt.Errorf("flush tab writer error: %w", err)
	}

	return nil
}
