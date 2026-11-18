package helm

import (
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/chart"

	"github.com/user-Xlll/go-ci/pkg/helminspect"
)

var workdir string

// RootCmd - root command for netrc.
var RootCmd = &cobra.Command{
	Use:   "helm",
	Short: "Commands for working with helm charts",
	Long:  `Contains various analyzers of helm chars`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		workdir = cmd.Flag("workdir").Value.String()

		var err error

		crt, err = helminspect.LoadHelmChart(workdir)

		return err
	},
}

var crt *chart.Chart
