package netrc

import (
	"github.com/jdx/go-netrc"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove rows in .netrc file",
	Long:  "Clear credantials for hosts in .netrc file",
	RunE:  logoutCmdRun,
}

func init() {
	RootCmd.AddCommand(logoutCmd)
}

func logoutCmdRun(_ *cobra.Command, hosts []string) error {
	return modifyNetRC(func(nrc *netrc.Netrc) error {
		if len(hosts) == 0 {
			list := nrc.Machines()
			hosts = make([]string, len(list))

			for index, machine := range list {
				hosts[index] = machine.Name
			}
		}

		for _, host := range hosts {
			nrc.RemoveMachine(host)
		}

		return nil
	})
}
