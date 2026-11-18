package netrc

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jdx/go-netrc"
)

// RootCmd - root command for netrc.
var RootCmd = &cobra.Command{
	Use:   "netrc",
	Short: "Commands for modify .netrc file",
	Long:  `Add or remove rows in .netrc file`,
}

func GetAuthForURL(rawURL string) (string, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", err
	}

	path, err := netrcFilePath()
	if err != nil {
		return "", "", err
	}

	model, err1 := readNetrc(path)
	if err1 != nil {
		return "", "", err1
	}

	m := u.Hostname()
	mm := model.Machine(m)
	if mm == nil || mm.IsDefault {
		return "", "", fmt.Errorf("no credentials for %s in .netrc", m)
	}

	return mm.Get("login"), mm.Get("password"), nil
}

func netrcFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home dir: %w", err)
	}

	return filepath.Join(home, ".netrc"), nil
}

func readNetrc(path string) (*netrc.Netrc, error) {
	res, err1 := netrc.Parse(path)
	if err1 != nil {
		if os.IsNotExist(err1) {
			return netrc.New(path), nil
		}

		return nil, fmt.Errorf("failed to parse %s file: %w", path, err1)
	}

	return res, nil
}

func modifyNetRC(use func(*netrc.Netrc) error) error {
	path, err := netrcFilePath()
	if err != nil {
		return err
	}

	model, err1 := readNetrc(path)
	if err1 != nil {
		return err1
	}

	if err = use(model); err != nil {
		return err
	}

	return model.Save()
}
