package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/user-Xlll/go-ci/pkg/util"
)

const statusSuccess = "SUCCESS"

type sonarAuth struct {
	Login    string `json:"sonar.login"`    //nolint:tagliatelle //ok
	Password string `json:"sonar.password"` //nolint:tagliatelle //ok
}

var sonarCmd = &cobra.Command{
	Use:   "sonar",
	Short: "Run sonar-scanner and wait for Quality Gate to complete",
	Long: `Executes the sonar-scanner tool to analyze the codebase
and waits until the Quality Gate (QG) has finished processing the results.`,
	RunE:               sonarCmdRun,
	DisableFlagParsing: true,
}

var (
	reURL                 = regexp.MustCompile(`report processing at\s+(.+)`)
	checkInterval         = 2 * time.Second
	waitTimeout           = 5 * time.Minute
	errTokenRequired      = errors.New("-Dsonar.login argument and env SONARQUBE_SCANNER_PARAMS.'sonar.login' is empty")
	errSonarBadTaskStatus = errors.New("bad sonar task status")

	statusInProgress = []string{"IN_PROGRESS", "PENDING"}
)

func init() {
	rootCmd.AddCommand(sonarCmd)
	sonarCmd.Flags().DurationVarP(&checkInterval, "check-interval", "i", checkInterval,
		"Interval between checks for the Quality Gate status")
	sonarCmd.Flags().DurationVarP(&waitTimeout, "wait-timeout", "t", waitTimeout,
		"Timeout for waiting for the Quality Gate to complete")
}

func getSonarParamValue(sonarParam string, sonarParams []string) (string, bool) {
	prefix := fmt.Sprintf("-Dsonar.%s=", sonarParam)

	if ix := slices.IndexFunc(sonarParams, func(param string) bool {
		return strings.HasPrefix(param, prefix)
	}); ix > -1 {
		return strings.TrimPrefix(sonarParams[ix], prefix), true
	}

	return "", false
}

func checkToken(sonarParams []string) (*sonarAuth, error) {
	if login, found := getSonarParamValue("login", sonarParams); found {
		res := &sonarAuth{Login: login}

		if password, found := getSonarParamValue("password", sonarParams); found {
			res.Password = password
		}

		return res, nil
	}

	val, found := os.LookupEnv("SONARQUBE_SCANNER_PARAMS")
	if !found {
		return nil, errTokenRequired
	}

	var res sonarAuth

	if err := json.Unmarshal([]byte(val), &res); err != nil {
		return nil, fmt.Errorf("failed to parse SONARQUBE_SCANNER_PARAMS: %w", err)
	}

	if res.Login == "" {
		return nil, errTokenRequired
	}

	return &res, nil
}

func parseSonarParams(cmd *cobra.Command, allParams []string) ([]string, error) {
	var noSonarParam []string
	var sonarParams []string

	for _, param := range allParams {
		if !strings.HasPrefix(param, "-D") {
			noSonarParam = append(noSonarParam, param)
		} else {
			sonarParams = append(sonarParams, param)
		}
	}

	if err := cmd.Flags().Parse(noSonarParam); err != nil {
		return nil, err
	}

	return sonarParams, nil
}

func sonarCmdRun(cm *cobra.Command, allParams []string) error {
	sonarParams, err := parseSonarParams(cm, allParams)
	if err != nil {
		return err
	}
	auth, err := checkToken(sonarParams)
	if err != nil {
		return err
	}

	args := make([]string, 0, len(sonarParams)+2)

	if debugMode {
		args = append(args, "-X", "-Dsonar.verbose=true")
	}

	args = append(args, sonarParams...)

	pr, pw := io.Pipe()
	cmd := exec.Command("sonar-scanner", args...)
	cmd.Stdout = pw
	cmd.Stderr = os.Stderr

	rURL := make(chan string, 1)
	rErr := make(chan error, 1)

	go func() {
		var (
			res    string
			resErr error
		)

		defer func() {
			if resErr = errors.Join(resErr, pw.Close()); resErr != nil {
				rErr <- resErr

				return
			}

			rURL <- res
		}()

		scanner := bufio.NewScanner(io.TeeReader(pr, os.Stdout))
		for scanner.Scan() {
			if res != "" {
				continue
			}

			if fnd := reURL.FindStringSubmatch(scanner.Text()); len(fnd) == 2 {
				res = fnd[1]
			}
		}

		if resErr = scanner.Err(); resErr != nil { //nolint:errorreturn //checked
			return
		}
	}()

	err = errors.Join(cmd.Run(), pw.Close())
	if err != nil {
		return fmt.Errorf("failed to run sonar-scanner: %w", err)
	}

	var taskURL string

	select {
	case err = <-rErr:
		return fmt.Errorf("read stdout failed: %w", err)
	case taskURL = <-rURL:
	}

	if err = waitTask(taskURL, auth); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timeout %d exceeded while waiting for task to complete: %w", waitTimeout, err)
		}

		return fmt.Errorf("failed to wait for task to complete: %w", err)
	}

	return nil
}

func waitTask(url string, auth *sonarAuth) error {
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()

	for {
		if err := ctx.Err(); err != nil {
			return err //nolint:wrapcheck //checked
		}

		if status, err := getTaskStatus(ctx, url, auth); err != nil {
			fmt.Fprintln(os.Stderr, "failed to get task status:", err)

			var statusCode util.BadResponseCodeError

			if errors.Is(err, &statusCode) && statusCode == http.StatusUnauthorized {
				return fmt.Errorf("authentication failed: %w", err)
			}
		} else {
			log.Println("Status: ", status)

			if !slices.Contains(statusInProgress, status) {
				if status != statusSuccess {
					return fmt.Errorf("%w: status %s", errSonarBadTaskStatus, status)
				}

				return nil
			}
		}

		log.Println("Sleep ", checkInterval, "...")

		select {
		case <-time.After(checkInterval):
		case <-ctx.Done():
		}
	}

}

func getTaskStatus(ctx context.Context, url string, auth *sonarAuth) (string, error) {
	res, err := util.HttpGetJSON[struct {
		Task struct {
			Status string `json:"status"`
		} `json:"task"`
	}](ctx, url, auth.Login, auth.Password)

	if err != nil {
		return "", err
	}

	return res.Task.Status, nil
}
