package helminspect

import (
	"fmt"
	"slices"
	"strings"
	"text/template/parse"

	"helm.sh/helm/v3/pkg/chart"
)

type PathFilter func(Path string) (string, bool)

func SubChartName(c *chart.Chart) string {
	var items []string

	for c1 := c; c1.Parent() != nil; c1 = c1.Parent() {
		items = append(items, c1.Name())
	}

	slices.Reverse(items)

	return strings.Join(items, ".")
}

func (s *Context) applyPathFilters(raw []string, PathFilters ...PathFilter) []string {
	res := make([]string, 0, len(raw))
	subChartName := SubChartName(s.chart)
items:
	for i, v := range raw {
		item := raw[i]

		if subChartName != "" &&
			strings.HasPrefix(v, "Values.") &&
			!strings.HasPrefix(v, "Values.global.") &&
			v != "Values.global" {
			item = "Values." + subChartName + "." + strings.TrimPrefix(v, "Values.")
		}

		for _, f := range PathFilters {
			var ok bool
			item, ok = f(item)

			if !ok {
				continue items
			}
		}

		res = append(res, item)
	}

	return res
}

func (s *Context) ArgOrPipeValue(argNumber int, cmd *parse.CommandNode, PathFilters ...PathFilter) []string {
	return s.applyPathFilters(s.analyzer.ArgOrPipeValue(argNumber, cmd), PathFilters...)
}

// UsedValues returns all possible config path values of the node
func (s *Context) UsedValues(node parse.Node, PathFilters ...PathFilter) []string {
	return s.applyPathFilters(usedValuesRaw(node, s.analyzer), PathFilters...)
}

func (anz *analyzer) PipeIncomingValue(cmd *parse.CommandNode) (parse.Node, bool) {
	pipe, ok := anz.nodesInfo[cmd].ParentNode.(*parse.PipeNode)
	if !ok {
		return nil, false
	}
	ix := slices.Index(pipe.Cmds, cmd)
	if ix == -1 {
		panic(fmt.Sprintf("Command %s not found in pipe", cmd))
	}
	if ix == 0 {
		return nil, false
	}

	return pipe.Cmds[ix-1], true
}

func (anz *analyzer) ArgOrPipeValue(argNumber int, cmd *parse.CommandNode) []string {
	if len(cmd.Args) > argNumber {
		return usedValuesRaw(cmd.Args[argNumber], anz)
	}

	if pipeArg, ok := anz.PipeIncomingValue(cmd); ok {
		return usedValuesRaw(pipeArg, anz)
	}

	return nil
}

func (anz *analyzer) ArgOrPipeConstants(argNumber int, cmd *parse.CommandNode) []any {
	if len(cmd.Args) > argNumber {
		return constantVariants(cmd.Args[argNumber], anz)
	}

	if pipeArg, ok := anz.PipeIncomingValue(cmd); ok {
		return constantVariants(pipeArg, anz)
	}

	return nil
}

func usedValuesRaw(node parse.Node, anz *analyzer) []string {
	switch typedNode := node.(type) {
	case *parse.DotNode:
		return []string{""}
	case *parse.PipeNode:
		if len(typedNode.Cmds) == 1 && len(typedNode.Decl) == 0 {
			return usedValuesRaw(typedNode.Cmds[0], anz)
		}
	case *parse.CommandNode:

		if id, ok := typedNode.Args[0].(*parse.IdentifierNode); ok {
			switch id.Ident {
			case "index":
				constVariants := anz.ArgOrPipeConstants(2, typedNode)

				var res []string

				for _, path := range usedValuesRaw(typedNode.Args[1], anz) {
					for _, val := range constVariants {
						if strval, ok := val.(string); ok {
							res = append(res, path+"."+strval)
						} else {
							res = append(res, fmt.Sprintf("%s[%v]", path, val))
						}
					}
				}

				return res
			case "keys":
				vals := anz.ArgOrPipeValue(1, typedNode)
				for i, v := range vals {
					vals[i] = v + "[*:key]"
				}
				return vals
			case "values":
				vals := anz.ArgOrPipeValue(1, typedNode)
				for i, v := range vals {
					vals[i] = v + "[*:value]"
				}
				return vals
			case "default":
				res := usedValuesRaw(typedNode.Args[1], anz)
				for _, v := range anz.ArgOrPipeValue(2, typedNode) {
					if !slices.Contains(res, v) {
						res = append(res, v)
					}
				}

				return res
			case "omit", "pick":
				return usedValuesRaw(typedNode.Args[1], anz)
			}
		}

		if len(typedNode.Args) == 1 {
			return usedValuesRaw(typedNode.Args[0], anz)
		}
	case *parse.VariableNode:
		nodeInfo := anz.nodesInfo[node]
		if _, ok := nodeInfo.ParentNode.(*parse.PipeNode); ok {
			return nil //declare or write variable
		}

		if typedNode.Ident[0] == "$" { //variable from config
			return []string{strings.Join(typedNode.Ident[1:], ".")}
		}

		name := strings.TrimPrefix(typedNode.Ident[0], "$")
		path := strings.Join(typedNode.Ident[1:], ".")

		allVariables := VariablesValues(node, anz.nodesInfo)
		if vv, found := allVariables[name]; found {
			var res []string

			for _, value := range vv.ValueVariants {
				for _, val := range usedValuesRaw(value.Value, anz) {
					if value.Foreach {
						switch {
						case value.IsKey:
							val += "[*:key]"
						case value.IsValue:
							val += "[*:value]"
						default:
							val += "[*]"
						}
					}

					if path != "" {
						val += "." + path
					}

					if !slices.Contains(res, val) {
						res = append(res, val)
					}
				}

			}

			return res
		}

		panic(fmt.Sprintf("Variable %s not found", name))
	case *parse.ChainNode:
		vals := usedValuesRaw(typedNode.Node, anz)

		for i, v := range vals {
			if v != "" {
				vals[i] = v + "." + strings.Join(typedNode.Field, ".")
			} else {
				vals[i] = strings.Join(typedNode.Field, ".")
			}
		}

		return vals
	case *parse.FieldNode:
		dotContext := anz.nodesInfo[node].NodeContext.DotContext
		if dotContext == nil {
			return []string{strings.Join(typedNode.Ident, ".")}
		}

		vals := usedValuesRaw(dotContext.Expression, anz)

		for i, v := range vals {
			var sep = "."

			if dotContext.ForEachResult {
				sep = "[*]."
			}

			if v != "" {
				vals[i] = v + sep + strings.Join(typedNode.Ident, ".")
			} else {
				vals[i] = strings.Join(typedNode.Ident, ".")
			}
		}

		return vals
	}

	return nil
}

// ConstantVariants returns all possible constant variants of the node
func (s *Context) ConstantVariants(node parse.Node) []any {
	return constantVariants(node, s.analyzer)
}

func constantVariants(node parse.Node, anz *analyzer) []any {
	switch typedNode := node.(type) {
	case *parse.PipeNode:
		if len(typedNode.Cmds) == 1 && len(typedNode.Decl) == 0 {
			return constantVariants(typedNode.Cmds[0], anz)
		}
	case *parse.CommandNode:
		if len(typedNode.Args) == 1 {
			return constantVariants(typedNode.Args[0], anz)
		}

	case *parse.BoolNode:
		return []any{typedNode.True}
	case *parse.NumberNode:
		switch {
		case typedNode.IsComplex:
			return []any{typedNode.Complex128}
		case typedNode.IsFloat:
			return []any{typedNode.Float64}
		case typedNode.IsInt:
			return []any{typedNode.Int64}
		case typedNode.IsUint:
			return []any{typedNode.Uint64}
		}
	case *parse.StringNode:
		return []any{typedNode.Text}
	case *parse.NilNode:
		return []any{nil}
	case *parse.VariableNode:
		nodeInfo := anz.nodesInfo[node]
		if _, ok := nodeInfo.ParentNode.(*parse.PipeNode); ok {
			return nil //declare or write variable
		}

		if typedNode.Ident[0] == "$" { //variable from config
			return nil
		}

		name := strings.TrimPrefix(typedNode.Ident[0], "$")
		path := strings.Join(typedNode.Ident[1:], ".")
		if path != "" {
			return nil
		}

		allVariables := VariablesValues(node, anz.nodesInfo)
		if vv, found := allVariables[name]; found {
			var raw []any

			for _, value := range vv.ValueVariants {
				for _, val := range constantVariants(value.Value, anz) {
					if value.Foreach {
						continue
					}

					if !slices.Contains(raw, val) {
						raw = append(raw, val)
					}
				}

			}

			return raw
		}

		panic(fmt.Sprintf("Variable %s not found", name))
	}
	return nil
}
