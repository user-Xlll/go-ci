package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
)

var giteventCmd = &cobra.Command{
	Use:   "gitprevious",
	Short: "Number of the previous minor version",
	Long: `
Retrieves number of the previous minor version.
Note that version must be in format v<MAJOR>.<MINOR>.<PATCH>
Versions in wrong format will be skipped with corresponding warning

Example
v1.7.3 -> passed as arg
v1.7.2 
v1.7.1
v1.6.8 -> returned as a result
v1.6.7
`,
	RunE: runGitprevious,
}

var shortOutput = true

func init() {
	giteventCmd.Flags().BoolVarP(&shortOutput, "short", "", shortOutput, "short version output format")
	rootCmd.AddCommand(giteventCmd)
}

var versionRe = regexp.MustCompile(`^(.*)0*(\d+)\.0*(\d+)\.0*(\d+).*$`)

func extractVersion(ref string) (string, *semver.Version, error) {
	parts := versionRe.FindStringSubmatch(ref)
	if len(parts) < 5 {
		return "", nil, fmt.Errorf("ref %s is not a valid semver", ref)
	}

	prefix := parts[1]
	version, err := semver.StrictNewVersion(strings.TrimPrefix(ref, prefix))
	if err != nil {
		return "", nil, fmt.Errorf("could not create version object from ref %s: %w", ref, err)
	}

	return prefix, version, err
}

func compareVersions(v1, v2 *semver.Version) int {
	if v1.Equal(v2) {
		return 0
	}
	if v1.GreaterThan(v2) {
		return 1
	}
	return -1
}

func runCmd(name string, arg ...string) (string, error) {
	sb := new(strings.Builder)

	cmd := exec.Command(name, arg...)
	cmd.Stdout = sb
	cmd.Stderr = os.Stderr
	cmd.Dir = workDir

	if err := cmd.Run(); err != nil {
		return "", err
	}

	return strings.TrimSpace(sb.String()), nil
}

func runGitprevious(cb *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no refName specified")
	}

	refName := args[0]

	revParseOutput, err := runCmd("git", "rev-parse", "--symbolic-full-name", refName)
	if err != nil {
		return fmt.Errorf("could not parse git rev %s: %w", refName, err)
	}

	fullRefName := strings.TrimSpace(revParseOutput)
	prefix, version, err := extractVersion(fullRefName)
	if err != nil {
		return fmt.Errorf("unable to parse semver from ref %s: %w", fullRefName, err)
	}

	remoteShowOutput, err := runCmd("git", "remote", "show")
	if err != nil {
		return fmt.Errorf("can't run git remote show: %w", err)
	}
	remotes := strings.Split(remoteShowOutput, "\n")
	if len(remotes) == 0 {
		return fmt.Errorf("current repo has no remotes")
	}
	if len(remotes) > 1 {
		log.Printf("there are more than one remotes: %s in this repo, the first one will be taken", remotes)
	}

	searchMask := fmt.Sprintf("%s*", prefix)
	lsRemoteOutput, err := runCmd("git", "ls-remote", remotes[0], searchMask)
	if err != nil {
		return fmt.Errorf("can't run git ls-remote: %w", err)
	}

	versions := []*semver.Version{}
	for line := range strings.SplitSeq(lsRemoteOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return fmt.Errorf("wrong git ls-remote output line format: expected 2 items, but got %d", len(fields))
		}
		ref := fields[1]
		_, version, err := extractVersion(ref)
		if err != nil {
			log.Printf("[WARNING] unable to parse semver from ref %s: %v this ref will be skipped", ref, err)
			continue
		}
		versions = append(versions, version)
	}

	slices.SortFunc(versions, compareVersions)
	idx, found := slices.BinarySearchFunc(versions, version, compareVersions)
	if !found {
		return fmt.Errorf("version %s doesn't exist in remote repo", version)
	}

	prev := version
	for i := idx; i > 0; i-- {
		if versions[i].Major() < version.Major() || versions[i].Minor() < version.Minor() {
			prev = versions[i]
			break
		}
	}

	prevRef := fmt.Sprintf("%s%s", prefix, prev)

	if shortOutput {
		if ix := strings.LastIndex(prevRef, "/"); ix > -1 {
			prevRef = prevRef[ix+1:]
		}
	}

	_, err = io.WriteString(cb.OutOrStdout(), prevRef)

	return err
}
