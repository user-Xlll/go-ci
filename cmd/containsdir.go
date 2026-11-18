package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var containsdirCmd = &cobra.Command{
	Use:   "containsdir",
	Short: "find directory contains all files with specified names",
	Long:  `find minial parent directory, contains all instances of file wuth specified names`,
	RunE:  runContainsdir,
}

var errNoFilesForSearch = errors.New("no files for search")
var firstOnly bool

func init() {
	containsdirCmd.Flags().BoolVarP(&firstOnly, "first-only", "f", firstOnly, "returns first match fast and stop search")
	rootCmd.AddCommand(containsdirCmd)
}

func runContainsdir(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w,need at least one file name for search their parent directory", errNoFilesForSearch)
	}

	var rootPath string

	err := filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		matchName := false

		for _, pattern := range args {
			if ok, err := filepath.Match(pattern, d.Name()); err == nil && ok {
				matchName = true
				break
			}
		}

		if !matchName {
			return nil
		}

		dir := filepath.Dir(strings.TrimPrefix(path, workDir+string(filepath.Separator)))

		switch {
		case rootPath == "":
			rootPath = dir
		case strings.HasPrefix(dir, rootPath+string(filepath.Separator)):
		default:
			dirParts := strings.Split(dir, string(filepath.Separator))

			for ix, name := range strings.Split(rootPath, string(filepath.Separator)) {
				if ix >= len(dirParts) || dirParts[ix] != name {
					if ix == 0 {
						rootPath = "."

						return filepath.SkipAll
					}

					rootPath = strings.Join(dirParts[:ix], string(filepath.Separator))

					break
				}
			}
		}

		if firstOnly {
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil {
		return err
	}

	_, _ = fmt.Fprint(os.Stdout, rootPath)

	return nil
}
