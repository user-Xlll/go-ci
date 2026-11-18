package helm

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"text/template/parse"
	"unicode"

	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/chart"

	"github.com/user-Xlll/go-ci/pkg/helminspect"
)

func init() {
	RootCmd.AddCommand(&cobra.Command{
		Use:   "used-values",
		Short: "show all used values by code of helm chart",
		Long:  `analyze helm chard code, and show all used config values, and default values by code`,
		RunE:  runHelmValues,
	})
}

type SourcePlaces []SourcePlace

type ValueUsage struct {
	Value any
	Uses  SourcePlaces
}

type ValueUsages []ValueUsage

type Info struct {
	Required      bool
	KnownValues   ValueUsages  `json:",omitempty"`
	DefaultValues ValueUsages  `json:",omitempty"`
	Uses          SourcePlaces `json:",omitempty"`
}

type Place struct {
	ByteOffset int
	LineNum    int
	ColumnNum  int
}

type SourcePlace struct {
	FilePath string
	From     Place
	To       Place
}

func (s SourcePlaces) String() string {
	var sb strings.Builder

	for ix, sp := range s {
		if ix > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(sp.String())
	}

	return sb.String()
}

func (s SourcePlace) String() string {
	if s.From.LineNum == s.To.LineNum {
		return fmt.Sprintf("%s:%d,%d-%d", s.FilePath, s.From.LineNum, s.From.ColumnNum, s.To.ColumnNum)
	}

	return fmt.Sprintf("%s:%d,%d-%d,%d", s.FilePath, s.From.LineNum, s.From.ColumnNum, s.To.LineNum, s.To.ColumnNum)
}

func IndexToPlace(index int, data string) Place {
	return Place{
		ByteOffset: index,
		LineNum:    strings.Count(data[:index], "\n") + 1,
		ColumnNum:  index - strings.LastIndex(data[:index], "\n"),
	}
}

func isIdentChar(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '.' || ch == '_' || ch == '$'
}
func toSourcePlace(useplace *helminspect.UsePlace) SourcePlace {
	var (
		ix int
		ch rune
	)

	for ix, ch = range string(useplace.ChartFile.Data[useplace.Node.Position():]) {
		if isIdentChar(ch) {
			continue
		}

		ch = ' '
		break
	}

	if ch != ' ' {
		ix++
	}

	path := filepath.Join(useplace.Chart.ChartFullPath(), useplace.ChartFile.Name)
	rootChart := useplace.Chart

	for r := rootChart.Parent(); r != nil; r = r.Parent() {
		rootChart = r
	}

	path = strings.TrimPrefix(path, rootChart.Name()+"/")
	ixFrom := int(useplace.Node.Position())

	for ixFrom > 0 && isIdentChar(rune(useplace.ChartFile.Data[ixFrom-1])) {
		ixFrom--
	}

	return SourcePlace{
		FilePath: path,
		From:     IndexToPlace(ixFrom, string(useplace.ChartFile.Data)),
		To:       IndexToPlace(int(useplace.Node.Position())+ix-1, string(useplace.ChartFile.Data)), //last char included
	}
}

func (s *ValueUsages) Add(node parse.Node, chart *chart.Chart, chartFile *chart.File, values ...any) {
	sourcePlace := toSourcePlace(&helminspect.UsePlace{Chart: chart, ChartFile: chartFile, Node: node})

vl:
	for _, value := range values {
		for _, val := range *s {
			if val.Value == value {
				if !slices.Contains(val.Uses, sourcePlace) {
					val.Uses = append(val.Uses, sourcePlace)
					slices.SortFunc(val.Uses, func(a, b SourcePlace) int {
						m := strings.Compare(a.FilePath, b.FilePath)
						if m != 0 {
							return m
						}

						return cmp.Compare(a.From.ByteOffset, b.From.ByteOffset)
					})
				}

				continue vl
			}
		}

		*s = append(*s, ValueUsage{Value: value, Uses: []SourcePlace{sourcePlace}})
	}
}

type UsedValuesStat map[string]Info

func confPathFilter(s string) (string, bool) {
	after, found := strings.CutPrefix(s, "Values.")
	if !found {
		return "", false
	}

	if after == "global" {
		return "", false
	}

	return after, true
}

func newStatHandler(dst UsedValuesStat) func(helminspect.Context, *helminspect.UsePlace) error {
	updateStat := func(path string, node parse.Node, chart *chart.Chart, chartFile *chart.File, uses ...func(stat *Info)) {
		old := dst[path]

		if node != nil {
			sp := toSourcePlace(&helminspect.UsePlace{Chart: chart, ChartFile: chartFile, Node: node})

			if !slices.Contains(old.Uses, sp) {
				old.Uses = append(old.Uses, sp)
			}
		}

		for _, use := range uses {
			use(&old)
		}

		dst[path] = old
	}

	return func(ctx helminspect.Context, useplace *helminspect.UsePlace) error {
		switch tp := useplace.Node.(type) {
		case *parse.CommandNode:
			ident, ok := tp.Args[0].(*parse.IdentifierNode)
			if !ok {
				break
			}

			switch ident.Ident {
			case "default":
				defValues := ctx.ConstantVariants(tp.Args[1])

				if len(defValues) == 0 {
					break
				}

				for _, confPath := range ctx.ArgOrPipeValue(2, tp,confPathFilter) {
					updateStat(confPath, nil, nil, nil, func(stat *Info) {
						stat.DefaultValues.Add(tp.Args[1], useplace.Chart, useplace.ChartFile, defValues...)
					})
				}

			case "omit", "pick", "hasKey":
				for _, confPathBase := range ctx.UsedValues(tp.Args[1], confPathFilter) {
					for _, arg := range tp.Args[2:] {
						for _, cv := range ctx.ConstantVariants(arg) {
							confPath := fmt.Sprintf("%s.%v", confPathBase, cv)
							updateStat(confPath, arg, useplace.Chart, useplace.ChartFile)
						}

					}

				}
			case "eq", "ne":
				a, b := tp.Args[1], tp.Args[2]

				for range 2 {
					for _, confPath := range ctx.UsedValues(a, confPathFilter) {
						knownVals := ctx.ConstantVariants(b)

						updateStat(confPath, a, useplace.Chart, useplace.ChartFile, func(stat *Info) {
							stat.KnownValues.Add(b, useplace.Chart, useplace.ChartFile, knownVals...)
						})
					}

					a, b = b, a
				}
			}
		case *parse.ChainNode, *parse.FieldNode, *parse.VariableNode:
			confPaths := ctx.UsedValues(tp, confPathFilter)
			isOptional := ctx.NodeContext(tp).IsOptional

			for _, confPath := range confPaths {
				updateStat(confPath, tp, useplace.Chart, useplace.ChartFile, func(stat *Info) {
					if !stat.Required && !isOptional {
						stat.Required = true
					}
				})
			}
		}

		return nil
	}
}
func analyzeHelmChart() (UsedValuesStat, error) {
	dst := make(UsedValuesStat)
	if err := helminspect.InspectHelmChart(context.Background(), crt, newStatHandler(dst)); err != nil {
		return nil, err
	}

	return dst, nil
}

func runHelmValues(cmd *cobra.Command, args []string) error {
	dst, err := analyzeHelmChart()

	if err != nil {
		return err
	}

	keys := slices.Collect(maps.Keys(dst))
	slices.Sort(keys)

	type item struct {
		Path string
		Info `json:",inline"`
	}

	res := make([]item, len(keys))
	for i, k := range keys {
		res[i] = item{Path: k, Info: dst[k]}
	}

	ec := json.NewEncoder(cmd.OutOrStdout())
	ec.SetIndent("", "  ")

	return ec.Encode(res)
}
