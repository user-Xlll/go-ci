// Package cmd содержит реализацию команд утилиты.
package cmd

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/user-Xlll/go-ci/cmd/helm"
	"github.com/user-Xlll/go-ci/cmd/netrc"
	"github.com/user-Xlll/go-ci/cmd/nexus"
	"github.com/user-Xlll/go-ci/cmd/show"
)

var (
	workDir      string
	errNoCommand = errors.New("please specify a command or use --help for more information")
	debugMode    = false
	rootCmd      = &cobra.Command{
		Use:   "goci",
		Short: "Утилита для реализации различных CI проектов на GO",
		Long:  `Утилита является частью InnerSource и открыта для доработок.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return errNoCommand
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}
)

func init() {
	workDirDefault, err := os.Getwd()

	if err != nil { //nolint:errorreturn //checked
		workDirDefault = filepath.Dir(os.Args[0])
	}

	rootCmd.PersistentFlags().StringVarP(&workDir, "workdir", "w", workDirDefault, "working directory")
	rootCmd.PersistentFlags().BoolVarP(&debugMode, "debug", "", false, "debug mode")

	rootCmd.AddCommand(netrc.RootCmd)
	rootCmd.AddCommand(helm.RootCmd)
	rootCmd.AddCommand(nexus.RootCmd)
	rootCmd.AddCommand(show.RootCmd)

	nexus.IsDebug = func() bool {
		return debugMode
	}
}

// ExecuteRootCommand запускает корневую команду.
func ExecuteRootCommand() error {
	return rootCmd.Execute() //nolint:wrapcheck //not need
}
