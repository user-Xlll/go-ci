// Package netrc provides commands for modifying .netrc file.
package netrc

import (
	"fmt"

	"errors"

	"github.com/jdx/go-netrc"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Add or update rows in .netrc file",
	Long:  "Save credantials for hosts in .netrc file",
	RunE:  loginCmdRun,
}

var username, password string

var errRequiredFlag = errors.New("required flag")
var errNoHosts = errors.New("need at least one host")

func init() {
	RootCmd.AddCommand(loginCmd)
	loginCmd.Flags().StringVarP(&username, "user", "u", "", "user for hosts")
	loginCmd.Flags().StringVarP(&password, "password", "p", "", "password for hosts")
}

func loginCmdRun(_ *cobra.Command, hosts []string) error {
	if username == "" {
		return fmt.Errorf("%w: user", errRequiredFlag)
	}

	if len(hosts) == 0 {
		return errNoHosts
	}

	return modifyNetRC(func(n *netrc.Netrc) error {
		for _, host := range hosts {
			machine := n.Machine(host)
			if machine == nil || machine.IsDefault {
				n.AddMachine(host, username, password)

				continue
			}

			machine.Set("login", username)
			machine.Set("password", password)
		}

		return nil
	})
}
