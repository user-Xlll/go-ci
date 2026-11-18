package show

import (
	"github.com/spf13/cobra"
)

var workdir string

// RootCmd - root command for show.
var RootCmd = &cobra.Command{
	Use:   "show",
	Short: "Commands for getting info",
	Long:  `Commands for getting information about modules and build targets`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		workdir = cmd.Flag("workdir").Value.String()
	},
}
