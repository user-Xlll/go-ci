package helm

import (
	"cmp"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/owenrumney/go-sarif/v3/pkg/report"
	"github.com/owenrumney/go-sarif/v3/pkg/report/v210/sarif"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"helm.sh/helm/v3/pkg/chart"

	"github.com/user-Xlll/go-ci/pkg/helminspect"
)

func init() {
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "lint helm chart",
		Long:  `analyze helm chart code, values and generafe sarif report`,
		RunE:  runLint,
	}

	cmd.Flags().StringVar(&SarifOut, "sarif", "", "path to write sarif report")
	cmd.Flags().IntVarP(&issueExitCode, "issues-exitcode", "e", issueExitCode, "exit code for linting errors")
	RootCmd.AddCommand(cmd)
}

type issue struct {
	Rule string
	SourcePlace
	Message    string
	IsCritical bool
}

var SarifOut string
var issueExitCode = 128

func otherValuesPath(value any, other string) any {
	if other == "" {
		return value
	}

	if mp, ok := value.(map[string]any); ok {
		return QueryValuesPath(mp, other)
	}

	return nil
}

type allIssues struct {
	data    map[string][]issue
	data_mu sync.Mutex
}

func (s *allIssues) Add(issue issue) {
	s.data_mu.Lock()
	defer s.data_mu.Unlock()

	s.data[issue.Rule] = append(s.data[issue.Rule], issue)
}

func (s *allIssues) All() iter.Seq2[string, []issue] {
	return func(yield func(string, []issue) bool) {
		s.data_mu.Lock()
		defer s.data_mu.Unlock()

		for k, v := range s.data {
			if !yield(k, v) {
				return
			}
		}
	}
}

func relCahrtToRelWD(rel string) string {
	abs := filepath.Join(workdir, rel)

	if wd, err := os.Getwd(); err == nil {
		if rel, err2 := filepath.Rel(wd, abs); err2 == nil {
			return rel
		}

	}

	return abs
}

func (s *allRules) ExportSARIF(filename string) error {
	run := sarif.NewRunWithInformationURI("goci-lint", "https://github.com/user-Xlll/go-ci")

	// for filePath := range s.allFiles {
	// 	run.AddDistinctArtifact(relCahrtToRelWD(filePath)) // add the location an artifact
	// }

	// for ruleKey, ruleInfo := range s.rules {
	// 	run.AddRule(ruleKey).WithDescription(ruleInfo.Description) // create a failed Rule
	// }

	for ruleKey, issues := range s.All() {
		for _, issue := range issues {
			level := "warning"

			if issue.IsCritical {
				level = "error"
			}

			run.CreateResultForRule(ruleKey).
				WithLevel(level).
				WithMessage(sarif.NewTextMessage(issue.Message)).
				AddLocation(
					sarif.NewLocationWithPhysicalLocation(
						sarif.NewPhysicalLocation().
							WithArtifactLocation(
								sarif.NewSimpleArtifactLocation(relCahrtToRelWD(issue.FilePath)),
							).WithRegion(

							sarif.NewRegion().
								WithStartLine(issue.From.LineNum).
								WithStartColumn(issue.From.ColumnNum).
								WithEndLine(issue.To.LineNum).
								WithEndColumn(issue.To.ColumnNum + 1),
							// WithByteOffset(issue.From.ByteOffset).
							// WithByteLength(issue.To.ByteOffset - issue.From.ByteOffset + 1),
						),
					),
				)
		}
	}

	rep := report.NewV210Report().AddRun(run)

	if err := rep.Validate(); err != nil { // validate the report
		return fmt.Errorf("failed to validate report: %w", err)
	}

	if err := rep.WriteFile(filename); err != nil {
		return fmt.Errorf(
			"failed to write SARIF report to %s: %w",
			filename,
			err,
		)
	}

	return nil
}

func newAllIssues() *allIssues {
	return &allIssues{
		data: make(map[string][]issue),
	}
}

func QueryValuesPath(values map[string]any, path string) any {
	field, other, _ := strings.Cut(path, ".")

	if ix := strings.Index(field, "["); ix > -1 {
		mode := field[ix+1 : len(field)-1]
		field = field[:ix]

		switch mode {
		case "*":
			switch tv := values[field].(type) {
			case []any:
				res := make([]any, 0, len(tv))
				for _, v := range tv {
					if ov := otherValuesPath(v, other); ov != nil {
						res = append(res, ov)
					}
				}

				if len(res) == 0 {
					return nil
				}

				return res
			case map[string]any:
				res := make([]any, 0, len(tv))
				for k, v := range tv {
					if ov := otherValuesPath(map[string]any{"key": k, "value": v}, other); ov != nil {
						res = append(res, ov)
					}
				}

				if len(res) == 0 {
					return nil
				}
				return res
			}
		case "*:key":
			if tv, ok := values[field].(map[string]any); ok {
				res := make([]any, 0, len(tv))

				for k := range tv {
					if ov := otherValuesPath(k, other); ov != nil {
						res = append(res, ov)
					}
				}

				if len(res) == 0 {
					return nil
				}

				return res
			}

			return nil
		case "*:value":
			if tv, ok := values[field].(map[string]any); ok {
				res := make([]any, 0, len(tv))

				for _, v := range tv {
					if ov := otherValuesPath(v, other); ov != nil {
						res = append(res, ov)
					}
				}

				if len(res) == 0 {
					return nil
				}

				return res
			}

			return nil
		default:
			index, err := strconv.Atoi(mode)
			if err != nil {
				panic(fmt.Errorf("unsupported mode %s: %w", mode, err))
			}

			if arr, ok := values[field].([]any); ok && index < len(arr) {
				return otherValuesPath(arr[index], other)
			}

			return nil
		}
	}

	return otherValuesPath(values[field], other)
}

type ruleInfo struct {
	Run         func(ar *allRules, ruleID string) error
	Description string
}

type allRules struct {
	*allIssues
	usedValues    UsedValuesStat
	defaultValues map[string]any
	allFiles      iter.Seq[string]
	rules         map[string]ruleInfo
}

func ruleDifferentDefaults(s *allRules, rule string) error {
	for k, v := range s.usedValues {
		if len(v.DefaultValues) < 2 {
			continue
		}

		for ix, defValue := range v.DefaultValues {
			var sb strings.Builder

			for ix2, v2 := range v.DefaultValues {
				if ix2 == ix {
					continue
				}

				if sb.Len() > 0 {
					sb.WriteString(", ")
				}

				sb.WriteString(fmt.Sprintf("%v (at %s:%d)", v2.Value, v2.Uses[0].FilePath, v2.Uses[0].From.LineNum))
			}

			for _, defPlace := range defValue.Uses {
				s.Add(issue{
					Rule:        rule,
					IsCritical:  true,
					SourcePlace: defPlace,
					Message:     fmt.Sprintf("%s already has a different default value %s", k, sb.String())})
			}
		}
	}

	return nil
}

func ruleDefaultAbsent(s *allRules, rule string) error {
	for k, v := range s.usedValues {
		if !v.Required {
			continue
		}

		if QueryValuesPath(s.defaultValues, k) == nil {
			for _, sp := range v.Uses {
				s.Add(issue{
					Rule:        rule,
					IsCritical:  true,
					SourcePlace: sp,
					Message:     fmt.Sprintf("%s absent in values.yaml, but used as required", k)})
			}
		}
	}

	return nil
}

func allFiles(c *chart.Chart, yield func(string) bool) bool {
	for _, f := range c.Raw {
		if !yield(f.Name) {
			return false
		}
	}

	for _, t := range c.Dependencies() {
		if !allFiles(t, yield) {
			return false
		}
	}

	return true
}

func runLint(cmd *cobra.Command, args []string) error {
	dst, err := analyzeHelmChart()

	if err != nil {
		return err
	}

	allRules := &allRules{
		allIssues:     newAllIssues(),
		usedValues:    dst,
		defaultValues: helminspect.ChartValues(crt),
		allFiles: func(yield func(string) bool) {
			_ = allFiles(crt, yield)
		},
		rules: map[string]ruleInfo{ //ALL DEFINED RULES
			"default-absent": {Run: ruleDefaultAbsent,
				Description: "shows the values used from the config for which there is " +
					"no meaning in default in the values.yml file inside the chart"},
			"different-defauls": {Run: ruleDifferentDefaults,
				Description: "the same value from the configuration " +
					"file has different default values in different parts of the chart"},
		},
	}

	var eg errgroup.Group

	for rule, v := range allRules.rules {
		eg.Go(func() error {
			return v.Run(allRules, rule)
		})
	}

	if err = eg.Wait(); err != nil {
		return err
	}

	var haseIssues bool

	for lint, issues := range allRules.All() {
		haseIssues = true

		slices.SortFunc(issues, func(a, b issue) int {
			if i := strings.Compare(a.FilePath, b.FilePath); i != 0 {
				return i
			}

			return cmp.Compare(a.From.ByteOffset, b.From.ByteOffset)
		})

		for _, issue := range issues {
			fmt.Printf("%s:%s (%s)\n", issue.Message, issue.SourcePlace, lint)
		}
	}

	if SarifOut != "" {
		if err = allRules.ExportSARIF(SarifOut); err != nil {
			return err
		}
	}

	if haseIssues {
		os.Exit(issueExitCode)
	}

	return nil
}
