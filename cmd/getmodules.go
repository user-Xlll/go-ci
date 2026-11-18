package cmd

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/ini.v1"

	"github.com/user-Xlll/go-ci/cmd/netrc"
	"github.com/user-Xlll/go-ci/pkg/modules"
	"github.com/user-Xlll/go-ci/pkg/modules/gomod"
)

var getmodulesCmd = &cobra.Command{
	Use:   "getmodules",
	Short: "Run go get for all no vendor modules",
	Long:  `Download go packages for all no vendor modules.`,
	RunE:  getmodulesCmdRun,
}

func init() {
	rootCmd.AddCommand(getmodulesCmd)
}

func getmodulesCmdRun(_ *cobra.Command, _ []string) error {
	modulesReport, err := modules.FindModules(workDir)
	if err != nil {
		return fmt.Errorf("find modules error: %w", err)
	}

	slices.SortFunc(modulesReport.GOModules,
		func(a, b *gomod.ModuleInfo) int { return strings.Compare(a.GoModDir, b.GoModDir) })

	for _, mi := range modulesReport.NPMModules {
		shortName := modulesReport.ModuleShortName(mi)
		log.Println("Downloading NPM modules for", shortName, mi.Path)

		if err := runNPMGet(mi.Path); err != nil {
			return err
		}
	}

	for _, mi := range modulesReport.GOModules {
		shortName := modulesReport.ModuleShortName(mi)
		if mi.UseVendor {
			log.Println("GO Module", shortName, "is using vendor directory. Skipping...")

			continue
		}

		log.Println("Downloading GO modules for", shortName, mi.GoModDir)

		if err = runGoGet(mi.GoModDir, false); err != nil {
			return err
		}

		log.Println("Downloading GO test modules for", shortName)

		if err = runGoGet(mi.GoModDir, true); err != nil {
			return err
		}

	}

	return nil
}

func runGoGet(moduleDir string, forTest bool) error {
	errBuf := new(bytes.Buffer)
	args := []string{"get"}

	if forTest {
		args = append(args, "-t")
	}

	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = errBuf
	cmd.Dir = moduleDir

	if err := cmd.Run(); err != nil {
		details := errBuf.String()

		if strings.Contains(details, "no package to get in current directory") {
			log.Println("No packages to get in", moduleDir, "skipping...")

			return nil
		}

		return fmt.Errorf("go get in dir %s error: %w, details %s", moduleDir, err, details)
	}

	return nil
}

func writeRegistryAuth(registry, rcFile string) (rErr error) {
	login, password, err := netrc.GetAuthForURL(registry)
	if err != nil {
		return fmt.Errorf("can't get auth for %s: %w", registry, err)
	}

	header := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", login, password))

	fl, err := os.OpenFile(rcFile, os.O_WRONLY|os.O_APPEND, os.ModePerm)
	if err != nil {
		return fmt.Errorf("can't write registry %s auth in %s: %w", registry, rcFile, err)
	}
	defer func() {
		rErr = errors.Join(rErr, fl.Close())
	}()

	_, tail, found := strings.Cut(registry, ":")
	if !found {
		tail = fmt.Sprintf("//%s", registry)
	}
	_, err = fmt.Fprintf(fl, "\n%s:_auth=%s", tail, header)
	if err != nil {
		return fmt.Errorf("can't write to file %s: %w", rcFile, err)
	}

	return nil
}

func runNPMGet(moduleDir string) (rErr error) {
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("NodeJS not found: %w", err)
	}

	rcFile := filepath.Join(moduleDir, ".npmrc")
	rcData, err := os.ReadFile(rcFile)
	if err != nil {
		return fmt.Errorf("can't read .npmrc file at %s: %w", moduleDir, err)
	}

	defer func() { //restore .npmrc
		rErr = errors.Join(rErr, os.WriteFile(rcFile, rcData, os.ModePerm))
	}()

	f, err := ini.Load(rcData)
	if err != nil {
		return fmt.Errorf("can't parse %s as ini file: %w", rcFile, err)
	}

	ds, err := f.GetSection("")
	if err != nil {
		return fmt.Errorf("can't find default section in init file %s: %w", rcFile, err)
	}

	r, err := ds.GetKey("registry")
	if err != nil {
		return fmt.Errorf("key registry not found in %s: %w", rcFile, err)
	}

	if err = writeRegistryAuth(r.String(), rcFile); err != nil {
		return fmt.Errorf("cat't set registry auth in %s from .netrc: %w", rcFile, err)
	}

	errBuf := new(bytes.Buffer)
	cmd := exec.Command("npm", "clean-install", "--ignore-scripts", "--force", "--verbose")
	cmd.Stdout = os.Stdout
	cmd.Stderr = errBuf
	cmd.Dir = moduleDir

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm clean-install in dir %s error: %w, details: %s", moduleDir, err, errBuf.String())
	}

	return nil
}
