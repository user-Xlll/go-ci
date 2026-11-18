package cmd

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/user-Xlll/go-ci/pkg/modules"
	"github.com/user-Xlll/go-ci/pkg/util"
)

type moduleInfo struct {
	ModulePath           string
	ReportDir            string
	JSONReportFilePath   string
	JUnitXML             string
	CoverageTXTFilePath  string
	CoverageHTMLFilePath string
	CoberturaXMLFilePath string
	StatusFilePath       string
	MainSubPath          []string
	StatusCode           int
}

const (
	defaultFilePerm = 0o640
	defaultDirPerm  = 0o750
)

var testCmd = &cobra.Command{
	Use:   "testreport",
	Short: "testreport",
	Long: `Generate in the specified directory reports:
			1) html code coverage
			2) txt code coverage
			3) xml cobertura
			4) xml junit
			for each module in a separate folder by the name of the module,
			 and also summary reports in the root directory specified`,
	RunE: runTestCmd,
}

// ErrTooManyArgs - слишком мнго аргументов.
var ErrTooManyArgs = errors.New("too many arguments")

// ErrNoGoModules - go модули не найдены.
var ErrNoGoModules = errors.New("no go modules")

// ErrTestsFailed - тесты не пройдены.
var ErrTestsFailed = errors.New("THERE IS FAILED TESTS")

var errRegExp = regexp.MustCompile(`(?i)(fail|error)`)

func init() {
	rootCmd.AddCommand(testCmd)
}

func (m *moduleInfo) isFailed() bool {
	return m.StatusCode != 0
}

func (m *moduleInfo) convertToJUNIT() error {
	filePath := filepath.Join(m.ReportDir, "junit.xml")

	cmd := exec.Command("go-junit-report", "-parser", //nolint:gosec // checked
		"gojson", "-in", m.JSONReportFilePath, "-out", filePath)

	var errBuf bytes.Buffer

	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("can't convert json tests report to xml(JUnit): %w, details:\n%s", err, errBuf.String())
	}

	m.JUnitXML = filePath

	return nil
}

func (m *moduleInfo) convertCoverageToHTML() error {
	if _, err := os.Stat(m.CoverageTXTFilePath); os.IsNotExist(err) {
		return nil
	}

	filePath := filepath.Join(m.ReportDir, "coverage.html")
	cmd := exec.Command("go", "tool", "cover", "-html", m.CoverageTXTFilePath, "-o", filePath) //nolint:gosec // checked
	cmd.Dir = m.ModulePath

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("can't convert coverage report to html: %w", err)
	}

	m.CoverageHTMLFilePath = filePath

	return nil
}

func (m *moduleInfo) convertToCobertura() (resErr error) {
	filePath := filepath.Join(m.ReportDir, "cobertura.xml")

	inFile, err := os.OpenFile(m.CoverageTXTFilePath, os.O_RDONLY, defaultFilePerm)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // skip convert if file not found
		}

		return fmt.Errorf("can't open coverage report file: %w", err)
	}

	defer func() {
		resErr = errors.Join(resErr, inFile.Close())
	}()

	outFile, err := os.OpenFile(filepath.Clean(filePath), os.O_CREATE|os.O_RDWR, defaultFilePerm)
	if err != nil {
		return fmt.Errorf("can't create cobertura report file: %w", err)
	}

	defer func() {
		resErr = errors.Join(resErr, outFile.Close())
	}()

	var errBuf bytes.Buffer

	cmd := exec.Command("gocover-cobertura")
	cmd.Stdin = inFile
	cmd.Stdout = outFile
	cmd.Stderr = &errBuf
	cmd.Dir = m.ModulePath

	if err = cmd.Run(); err != nil {
		return fmt.Errorf("can't convert coverage report to xml(cobertura): %w, detalis: %s", err, errBuf.String())
	}

	m.CoberturaXMLFilePath = filePath

	return nil
}

func (m *moduleInfo) runConverters() error {
	var eg errgroup.Group

	for _, fnc := range []func(*moduleInfo) error{
		(*moduleInfo).convertToJUNIT,
		(*moduleInfo).convertToCobertura,
		(*moduleInfo).convertCoverageToHTML} {
		eg.Go(func() error {
			return fnc(m)
		})
	}

	return eg.Wait()
}

func newModuleInfo(reportDir, modulePath string) *moduleInfo {
	m := &moduleInfo{ReportDir: reportDir, ModulePath: modulePath, StatusCode: -1}
	m.JSONReportFilePath = filepath.Join(reportDir, "tests_report.json")
	m.CoverageTXTFilePath = filepath.Join(reportDir, "coverprofile.txt")
	m.StatusFilePath = filepath.Join(reportDir, "tests.status")

	return m
}

func (m *moduleInfo) writeStatus(failDetails string) error {
	var status bytes.Buffer

	if m.isFailed() {
		status.WriteString("FAILED\n")
		status.WriteString(failDetails)
	} else {
		status.WriteString("SUCCESS")
	}

	if err := os.WriteFile(m.StatusFilePath, status.Bytes(), defaultFilePerm); err != nil {
		return fmt.Errorf("can't write tests status file: %w", err)
	}

	return nil
}

func (m *moduleInfo) runTests() (resErr error) {
	fl, err := os.OpenFile(m.JSONReportFilePath, os.O_CREATE|os.O_WRONLY, defaultFilePerm)
	if err != nil {
		return fmt.Errorf("can't create tests_report.json in dir %s: %w", m.ReportDir, err)
	}

	defer func() {
		resErr = errors.Join(resErr, fl.Close(), m.writeStatus(m.ModulePath))
	}()

	cmd := exec.Command("go", "test", "-json", "-race", "-count=1", //nolint:gosec // checked
		"-timeout=1m", "-coverpkg=./...",
		"-coverprofile="+m.CoverageTXTFilePath, "./...") //nolint:strconcat // checked
	cmd.Dir = m.ModulePath
	cmd.Stdout = fl
	cmd.Stderr = fl

	if err = cmd.Run(); err != nil {
		var ec *exec.ExitError

		if errors.As(err, &ec) {
			m.StatusCode = ec.ExitCode()

			return nil
		}

		return fmt.Errorf("go test error: %w", err)
	}

	m.StatusCode = 0

	return nil
}

func appendJSONFile(dst io.Writer, src *moduleInfo) (resErr error) {
	sf, err := os.OpenFile(src.JSONReportFilePath, os.O_RDONLY, defaultFilePerm)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // skip merge if file not found
		}

		return fmt.Errorf("can't open tests_report.json from %s: %w", src.JSONReportFilePath, err)
	}

	defer func() {
		resErr = errors.Join(resErr, sf.Close())
	}()

	if _, err = io.Copy(dst, sf); err != nil {
		return fmt.Errorf("can't append tests_report.json %s: %w", src.JSONReportFilePath, err)
	}

	return nil
}

func mergeCoverage(from []*moduleInfo, to *moduleInfo) (resErr error) {
	fl, err := os.OpenFile(to.CoverageTXTFilePath, os.O_CREATE|os.O_WRONLY, defaultFilePerm)
	if err != nil {
		return fmt.Errorf("can't create coverprofile.txt in dir %s: %w", to.ReportDir, err)
	}

	defer func() {
		resErr = errors.Join(resErr, fl.Close())
	}()

	var errBuf bytes.Buffer

	args := make([]string, 0, len(from))

	var ok bool

	for _, k := range from {
		if ok, err = util.FileExists(k.CoverageTXTFilePath); err != nil {
			return fmt.Errorf("can't check file %s: %w", k.CoverageTXTFilePath, err)
		} else if !ok {
			continue
		}

		args = append(args, k.CoverageTXTFilePath)
	}

	cmd := exec.Command("gocovmerge", args...) //nolint:gosec // checked
	cmd.Stderr = &errBuf
	cmd.Stdout = fl

	if err = cmd.Run(); err != nil {
		return fmt.Errorf("can't merge coverage reports: %w, detalis: %s", err, errBuf.String())
	}

	return nil
}

func mergeReports(ml []*moduleInfo, m *moduleInfo) error {
	var eg errgroup.Group

	eg.Go(func() error {
		return mergeTestsJSON(ml, m)
	})

	eg.Go(func() error {
		return mergeCoverage(ml, m)
	})

	return eg.Wait()
}

func (m *moduleInfo) getTestsErrors() ([]string, error) {
	file, err := os.OpenFile(m.JSONReportFilePath, os.O_RDONLY, defaultFilePerm)
	if err != nil {
		return nil, fmt.Errorf("can't open tests_report.json from %s: %w", m.JSONReportFilePath, err)
	}

	var errs []string

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		txt := scanner.Text()

		if len(errRegExp.FindStringIndex(txt)) == 0 {
			continue
		}

		errs = append(errs, txt)
	}

	if err = scanner.Err(); err != nil {
		return nil, err
	}

	return errs, file.Close()
}

func mergeTestsJSON(list []*moduleInfo, dst *moduleInfo) (resErr error) {
	jsonDst, err := os.OpenFile(dst.JSONReportFilePath, os.O_CREATE|os.O_WRONLY, defaultFilePerm)
	if err != nil {
		return fmt.Errorf("can't create tests_report.json in dir %s: %w", dst.ReportDir, err)
	}

	defer func() {
		resErr = errors.Join(resErr, jsonDst.Close())
	}()

	var failModules []string

	for _, scrModule := range list {
		if scrModule.isFailed() {
			module := strings.TrimPrefix(strings.TrimPrefix(scrModule.ModulePath, workDir), string(filepath.Separator))
			failModules = append(failModules, module)
			dst.StatusCode = max(scrModule.StatusCode, dst.StatusCode)
		}

		if err = appendJSONFile(jsonDst, scrModule); err != nil {
			return err
		}
	}

	if len(failModules) == 0 {
		dst.StatusCode = 0
	}

	slices.Sort(failModules)

	return dst.writeStatus(strings.Join(failModules, "\n"))
}

func getDestinationDir(args []string, rndPattern string) (string, error) {
	switch len(args) {
	case 0:
		rd, err := os.MkdirTemp(os.TempDir(), rndPattern)

		if err != nil {
			return "", fmt.Errorf("can't create temp dir for reports: %w", err)
		}

		return rd, nil
	case 1:
		path, err := filepath.Abs(args[0])
		if err != nil {
			return "", fmt.Errorf("can't resolve destination path: %w", err)
		}

		return path, nil
	default:
		return "", ErrTooManyArgs
	}
}

func runTestCmd(_ *cobra.Command, args []string) error {
	reportDir, err := getDestinationDir(args, "test-report-*")
	if err != nil {
		return err
	}

	log.Println("writing reports to:", reportDir)

	modulesReport, err := modules.FindModules(workDir)
	if err != nil {
		return fmt.Errorf("can't find go modules: %w", err)
	}

	modList := make([]*moduleInfo, 0, len(modulesReport.GOModules))

	var summaryModule *moduleInfo

	switch len(modulesReport.GOModules) {
	case 0:
		return ErrNoGoModules
	case 1:
		for _, m := range modulesReport.GOModules {
			summaryModule = newModuleInfo(reportDir, m.GoModDir)
			modList = append(modList, summaryModule)
		}
	default:
		summaryModule = newModuleInfo(reportDir, modulesReport.GoWorkDir)

		for _, v := range modulesReport.GOModules {
			modList = append(modList, newModuleInfo(filepath.Join(reportDir, modulesReport.ModuleShortName(v)), v.GoModDir))
		}
	}

	log.Println("running tests for", len(modList), "modules...")

	if err = makeTestReports(modList); err != nil {
		return err
	}

	if len(modList) > 1 {
		log.Println("merge reports...")

		if err = mergeReports(modList, summaryModule); err != nil {
			return err
		}

		if err = summaryModule.runConverters(); err != nil {
			return err
		}
	}

	if summaryModule.StatusCode != 0 {
		lst, err2 := summaryModule.getTestsErrors()

		if err2 != nil {
			fmt.Fprintln(os.Stderr, "can't extract errors from json test report:", err2.Error())
		}

		for _, msg := range lst {
			fmt.Fprintln(os.Stderr, msg)
		}

		return ErrTestsFailed
	}

	log.Println("SUCCESS!")

	return nil
}

func makeTestReports(modList []*moduleInfo) error {
	var eg errgroup.Group

	for _, mod := range modList {
		eg.Go(func() error {
			var err error

			if err = os.Mkdir(mod.ReportDir, defaultDirPerm); err != nil && !os.IsExist(err) {
				return fmt.Errorf("can't create dir %s: %w", mod.ReportDir, err)
			}

			if err = mod.runTests(); err != nil {
				return fmt.Errorf("can't run tests for module %s: %w", mod.ModulePath, err)
			}

			if err = mod.runConverters(); err != nil {
				return fmt.Errorf("can't convert test report for module %s: %w", mod.ModulePath, err)
			}

			return nil
		})
	}

	return eg.Wait()
}
