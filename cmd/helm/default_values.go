package helm

import (
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/user-Xlll/go-ci/pkg/helminspect"
)

func init() {
	RootCmd.AddCommand(&cobra.Command{
		Use:   "default-values",
		Short: "show merged values.yaml of chart and all subcharts",
		Long:  `merged values.yaml of chart with all values.yaml of subcharts`,
		RunE:  runHelmDefValues,
	})
}

func runHelmDefValues(cmd *cobra.Command, args []string) error {
	return yaml.NewEncoder(cmd.OutOrStdout()).Encode(helminspect.ChartValues(crt))
}
