// Package main точка входа в приложение
package main

import (
	"fmt"
	"os"

	"github.com/user-Xlll/go-ci/cmd"
)

func main() {
	if err := cmd.ExecuteRootCommand(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
