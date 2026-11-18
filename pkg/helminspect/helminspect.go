package helminspect

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"text/template"
	"text/template/parse"

	_ "unsafe"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/engine"
)

//go:linkname initFunMap helm.sh/helm/v3/pkg/engine.(*Engine).initFunMap
func initFunMap(e *engine.Engine, t *template.Template)

type DotContext struct {
	Expression     parse.Node
	CommandContext *DotContext
	ForEachResult  bool
}

func (s *DotContext) String() string {
	if s == nil || s.Expression == nil {
		return ""
	}

	var scope, pipe, mode string

	pipe = s.Expression.String() //todo remove BRACKETS

	if s.CommandContext != nil && strings.HasPrefix(pipe, ".") {
		scope = s.CommandContext.String()
	}

	if s.ForEachResult {
		mode = "[*]"
	}

	return scope + pipe + mode
}

type Variable struct {
	Name          string
	Scope         *parse.BranchNode
	Decl          *parse.VariableNode
	ValueVariants []*SetVariableLog
}

type Variables map[string]Variable

type Context struct {
	*analyzer
	nodeContext *VisitNodeContext
}

func (s *Context) Context() context.Context {
	return s.context
}

func (s *Context) NodeContext(node parse.Node) *VisitNodeContext {
	return s.nodesInfo[node].NodeContext
}

func (s *Context) ParentNode(node parse.Node) parse.Node {
	return s.nodesInfo[node].ParentNode
}

type VarInLogBranch struct {
	VarDeclaration *VarDeclaration
	LogBranch      *SetVariableLog
}

func VariablesValues(node parse.Node, nodesInfo map[parse.Node]NodeInfo) map[string]Variable {
	lastValue := make(map[VarInLogBranch]*SetVariableLog)
	nodeInfo := nodesInfo[node]

	for _, branch := range nodeInfo.IncomingVarLogs {
		branch.LastValues(branch, nodeInfo.NodeContext.Scopes, lastValue)
	}

	res := make(Variables)

	for k, v := range lastValue {
		old := res[k.VarDeclaration.Name]

		if old.ValueVariants == nil {
			old = Variable{
				Name:          k.VarDeclaration.Name,
				Scope:         k.VarDeclaration.Scope,
				Decl:          k.VarDeclaration.Node,
				ValueVariants: []*SetVariableLog{v}}
		} else {
			old.ValueVariants = append(old.ValueVariants, v)
		}

		res[k.VarDeclaration.Name] = old
	}

	return res
}

func (s *Context) DotContext() *DotContext {
	return s.nodeContext.DotContext
}

func (s *Context) Scope() *parse.BranchNode {
	if len(s.nodeContext.Scopes) == 0 {
		return nil
	}

	return s.nodeContext.Scopes[len(s.nodeContext.Scopes)-1]
}

type UsePlace struct {
	Chart     *chart.Chart
	ChartFile *chart.File
	Node      parse.Node
}

type NodeHandler func(ctx Context, place *UsePlace) error

type VarDeclaration struct {
	Name  string
	Scope *parse.BranchNode
	Node  *parse.VariableNode
}

type SetVariableLog struct {
	Variable     *VarDeclaration
	IsNew        bool
	IsKey        bool
	IsValue      bool
	Foreach      bool
	Value        *parse.CommandNode
	ValueContext *VisitNodeContext
	Parents      []*SetVariableLog
}

func (s *SetVariableLog) LastValues(branch *SetVariableLog, scopes []*parse.BranchNode, dst map[VarInLogBranch]*SetVariableLog) {
	if s.Variable.Scope == nil || slices.Contains(scopes, s.Variable.Scope) {
		key := VarInLogBranch{VarDeclaration: s.Variable, LogBranch: branch}
		if dst[key] == nil {
			dst[key] = s
		}
	}

	for _, parent := range s.Parents {
		parent.LastValues(branch, scopes, dst)
	}
}

type VisitNodeContext struct {
	DotContext *DotContext
	Parent     *VisitNodeContext
	Scopes     []*parse.BranchNode
	IsOptional bool
}

type NodeInfo struct {
	IncomingVarLogs []*SetVariableLog
	NodeContext     *VisitNodeContext
	ParentNode      parse.Node
	Template        *template.Template
}

type nodeChart struct {
	node      parse.Node
	chartFile *chart.File
}
type TemplateInfo struct {
	Chart     *chart.Chart
	ChartFile *chart.File
}

type analyzer struct {
	context           context.Context
	file              *chart.File
	nodesInfo         map[parse.Node]NodeInfo
	rangeBreackValues []*SetVariableLog
	allNodes          []*UsePlace
	chart             *chart.Chart
	defTemplates      map[string]*template.Template
	templateInfo      map[*template.Template]TemplateInfo
}

func joinVarLog(ctx *VisitNodeContext, pipe *parse.PipeNode, parents []*SetVariableLog) []*SetVariableLog {
	var (
		scope  *parse.BranchNode
		varlog *SetVariableLog
	)

	if len(ctx.Scopes) > 0 {
		scope = ctx.Scopes[len(ctx.Scopes)-1]
	}

	for i, decl := range pipe.Decl {
		if i == 0 {
			varlog = &SetVariableLog{
				Parents:      parents,
				ValueContext: ctx}
		} else {
			varlog = &SetVariableLog{
				Parents:      []*SetVariableLog{varlog},
				ValueContext: ctx}
		}

		varName := strings.TrimPrefix(strings.Join(decl.Ident, "."), "$")

		if scope != nil && scope.Pipe == pipe {
			varlog.Foreach = scope.NodeType == parse.NodeRange
			varlog.IsKey = len(scope.Pipe.Decl) == 2 && i == 0
			varlog.IsValue = len(scope.Pipe.Decl) == 2 && i == 1
			varlog.Value = pipe.Cmds[0]
		} else {
			varlog.Value = pipe.Cmds[i]
		}

		if !pipe.IsAssign {
			varlog.IsNew = true
			varlog.Variable = &VarDeclaration{Name: varName, Scope: scope, Node: decl}

			continue
		}

		if len(varlog.Parents) > 0 {
		search:
			for tmpLog := varlog.Parents[0]; tmpLog != nil; tmpLog = tmpLog.Parents[0] {
				if tmpLog.IsNew && len(tmpLog.ValueContext.Scopes) <= len(ctx.Scopes) && tmpLog.Variable.Name == varName {
					for i, sc := range tmpLog.ValueContext.Scopes {
						if sc != ctx.Scopes[i] { //other scope
							continue search
						}
					}

					varlog.Variable = tmpLog.Variable
					break
				}
			}
		}

		if varlog.Variable == nil {
			panic(fmt.Sprintf("unknown variable %s", varName))
		}
	}

	if varlog == nil {
		return parents
	}

	return []*SetVariableLog{varlog}
}

func (s *VisitNodeContext) Scope(scope *parse.BranchNode) *VisitNodeContext {
	res := *s
	res.Scopes = append(res.Scopes[0:len(res.Scopes):len(res.Scopes)], scope)
	return &res
}

func (s *VisitNodeContext) Optional() *VisitNodeContext {
	res := *s
	res.IsOptional = true
	return &res
}

func (s *VisitNodeContext) setDotByCurrentScope() *VisitNodeContext {
	scope := s.Scopes[len(s.Scopes)-1]

	if scope.NodeType == parse.NodeIf {
		return s
	}

	return s.SetDot(scope.Pipe.Cmds[0], scope.NodeType == parse.NodeRange)
}

func (s *VisitNodeContext) SetDot(cmd parse.Node, forEach bool) *VisitNodeContext {
	if pipe, ok := cmd.(*parse.PipeNode); ok {
		cmd = pipe.Cmds[0]
	}

	res := *s
	res.DotContext = &DotContext{CommandContext: res.DotContext,
		Expression:    cmd,
		ForEachResult: forEach}

	return &res
}

func (s *VisitNodeContext) Child() *VisitNodeContext {
	res := *s
	res.Parent = s
	return &res
}

func visitNodeList[T parse.Node](ctx *VisitNodeContext, s *analyzer, nodes []T, parent parse.Node, prevVarLogs []*SetVariableLog, t *template.Template,
	ctxPatch func(int, T) *VisitNodeContext) ([]*SetVariableLog, error) {
	for i, node := range nodes {
		if ctxPatch != nil {
			ctx = ctxPatch(i, node)
		}

		var err error

		if prevVarLogs, err = s.visitNode(ctx, node, parent, prevVarLogs, t); err != nil {
			return nil, err
		}
	}

	return prevVarLogs, nil
}

func (s *analyzer) visitNode(ctx *VisitNodeContext, node, parent parse.Node, prevVarLogs []*SetVariableLog, t *template.Template) ([]*SetVariableLog, error) {
	s.nodesInfo[node] = NodeInfo{IncomingVarLogs: prevVarLogs, NodeContext: ctx, ParentNode: parent, Template: t}
	ti := s.templateInfo[t]
	s.allNodes = append(s.allNodes, &UsePlace{Node: node, ChartFile: ti.ChartFile, Chart: ti.Chart})

	ctx = ctx.Child()

	branchNode := func(typedNode *parse.BranchNode) ([]*SetVariableLog, error) {
		ctx = ctx.Scope(typedNode)

		varLogs2, err := s.visitNode(ctx, typedNode.Pipe, typedNode, prevVarLogs, t)
		if err != nil {
			return nil, err
		}

		var varLogs3 []*SetVariableLog

		ctx2 := ctx.setDotByCurrentScope()
		if typedNode.NodeType == parse.NodeIf {
			ctx2 = ctx2.Optional()
		}

		if varLogs3, err = s.visitNode(ctx2, typedNode.List, typedNode, varLogs2, t); err != nil {
			return nil, err
		}

		if typedNode.ElseList != nil {
			var varLogs3e []*SetVariableLog
			if varLogs3e, err = s.visitNode(ctx.Optional(), typedNode.ElseList, typedNode, varLogs2, t); err != nil {
				return nil, err
			}

			res := make([]*SetVariableLog, 0, len(varLogs3)+len(varLogs3e))
			res = append(res, varLogs3...)

			for _, v := range varLogs3e {
				if !slices.Contains(varLogs3, v) {
					res = append(res, v)
				}
			}

			varLogs3 = res
		}

		return varLogs3, nil
	}

	switch typedNode := node.(type) {
	case *parse.PipeNode:
		if _, err := visitNodeList(ctx, s, typedNode.Decl, typedNode, prevVarLogs, t, nil); err != nil {
			return nil, err
		}

		if _, err := visitNodeList(ctx, s, typedNode.Cmds, typedNode, prevVarLogs, t,
			func(ix int, cmd *parse.CommandNode) *VisitNodeContext {
				if ix+1 < len(typedNode.Cmds) {
					if in, ok := typedNode.Cmds[ix+1].Args[0].(*parse.IdentifierNode); ok && in.Ident == "default" {
						return ctx.Optional()
					}
				}

				return ctx
			}); err != nil {
			return nil, err
		}

		return joinVarLog(ctx, typedNode, prevVarLogs), nil
	case *parse.TemplateNode:
		return s.visitTemplateCall(ctx, typedNode.Name, typedNode.Pipe, typedNode, prevVarLogs, t)
	case *parse.ChainNode:
		return s.visitNode(ctx, typedNode.Node, typedNode, prevVarLogs, t)
	case *parse.CommandNode:
		if len(typedNode.Args) > 1 {
			if in, ok := typedNode.Args[0].(*parse.IdentifierNode); ok {
				switch in.Ident {
				case "include":
					for _, name := range constantVariants(typedNode.Args[1], s) {
						var dotValue parse.Node

						if len(typedNode.Args) > 2 {
							dotValue = typedNode.Args[2]
						}

						if _, err := s.visitTemplateCall(ctx, fmt.Sprint(name), dotValue, typedNode, prevVarLogs, t); err != nil {
							return nil, err
						}
					}
					return nil, nil
				case "default":
					if _, err := s.visitNode(ctx, typedNode.Args[1], typedNode, prevVarLogs, t); err != nil {
						return nil, err
					}

					if len(typedNode.Args) > 2 {
						if _, err := s.visitNode(ctx.Optional(), typedNode.Args[2], typedNode, prevVarLogs, t); err != nil {
							return nil, err
						}
					}
					return nil, nil
				}
			}
		}
		return visitNodeList(ctx, s, typedNode.Args, typedNode, prevVarLogs, t, nil)
	case *parse.ListNode:
		return visitNodeList(ctx, s, typedNode.Nodes, typedNode, prevVarLogs, t, nil)
	case *parse.ActionNode:
		return s.visitNode(ctx, typedNode.Pipe, typedNode, prevVarLogs, t)
	case *parse.IfNode:
		return branchNode(&typedNode.BranchNode)
	case *parse.RangeNode:
		vals, err := branchNode(&typedNode.BranchNode)
		if err != nil {
			return nil, err
		}

		if s.rangeBreackValues != nil {
			res := vals[0:len(vals):len(vals)]

			for _, v := range s.rangeBreackValues {
				if !slices.Contains(vals, v) {
					res = append(res, v)
				}
			}

			vals = res
			s.rangeBreackValues = nil
		}

		return vals, nil

	case *parse.WithNode:
		return branchNode(&typedNode.BranchNode)
	case *parse.BreakNode, *parse.ContinueNode:
		s.rangeBreackValues = append(s.rangeBreackValues, prevVarLogs...)
		return prevVarLogs, nil
	default:
		return prevVarLogs, nil
	}
}

func (s *analyzer) visitTemplateCall(ctx *VisitNodeContext, name string, dotValue parse.Node, parent parse.Node, prevVarLogs []*SetVariableLog, t *template.Template) ([]*SetVariableLog, error) {
	if dotValue != nil {
		if _, err := s.visitNode(ctx, dotValue, parent, prevVarLogs, t); err != nil {
			return nil, err
		}

		ctx = ctx.SetDot(dotValue, false)
	}

	tmp, found := s.defTemplates[name]
	if !found {
		return nil, fmt.Errorf("can't find defined template %s", name)
	}

	_, err := s.visitNode(ctx, tmp.Root, nil, nil, tmp)
	return nil, err
}

func (s *analyzer) visitTemplates(root *chart.Chart, use func(*chart.Chart, *chart.File) error) error {
	s.chart = root
	//root.Values

	for _, t := range root.Templates {
		if err := use(root, t); err != nil {
			return err
		}
	}

	for _, dep := range root.Dependencies() {
		s.chart = dep

		if err := s.visitTemplates(dep, use); err != nil {
			return err
		}
	}

	return nil
}

func mergeValues(dst, src map[string]any) {
	for k, v := range src {
		if old, ok := dst[k].(map[string]any); ok {
			if new, ok2 := v.(map[string]any); ok2 {
				mergeValues(old, new)
				continue
			}
		}

		dst[k] = v
	}

}

func appendValues(c *chart.Chart, dst map[string]any) {
	for _, sub := range c.Dependencies() {
		appendValues(sub, dst)
	}

	if subName := SubChartName(c); subName != "" {
		for name := range strings.SplitSeq(subName, ".") {
			sub := make(map[string]any)
			dst[name] = sub
			dst = sub
		}
	}

	mergeValues(dst, c.Values)
}

func LoadHelmChart(helmDir string) (*chart.Chart, error) {
	crt, err := loader.Load(helmDir)
	if err != nil {
		return nil, fmt.Errorf("can't load HelmChart: %w", err)
	}

	return crt, nil
}

func ChartValues(crt *chart.Chart) map[string]any {
	defaultValues := make(map[string]any)
	appendValues(crt, defaultValues)

	return defaultValues
}

func InspectHelmChart(ctx context.Context, crt *chart.Chart, nodeHandler NodeHandler) error {
	gotpl := template.New("gotpl")
	gotpl.Option("missingkey=zero")
	initFunMap(new(engine.Engine), gotpl)

	a := &analyzer{context: ctx,
		nodesInfo:    make(map[parse.Node]NodeInfo),
		templateInfo: make(map[*template.Template]TemplateInfo),
		defTemplates: make(map[string]*template.Template)}

	err := a.visitTemplates(crt, func(chrt *chart.Chart, f *chart.File) error {
		a.file = f

		oldTemplates := gotpl.Templates()
		rt, err2 := gotpl.Parse(string(f.Data))
		if err2 != nil {
			return fmt.Errorf("can't parse template %s: %w", f.Name, err2)
		}

		a.templateInfo[rt] = TemplateInfo{Chart: chrt, ChartFile: f} //file template

		defs := rt.Templates()

		for _, t := range defs {
			if slices.Contains(oldTemplates, t) || t.Name() == gotpl.Name() {
				continue //defined in other file
			}
			a.templateInfo[t] = TemplateInfo{Chart: chrt, ChartFile: f} //definated sub templates
			a.defTemplates[t.Name()] = t
		}

		if _, err := a.visitNode(&VisitNodeContext{}, rt.Root, nil, nil, rt); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return err
	}

	for _, n := range a.allNodes {
		if err := nodeHandler(Context{analyzer: a, nodeContext: a.nodesInfo[n.Node].NodeContext}, n); err != nil {
			return err
		}
	}

	a.allNodes = nil //free memory

	return nil
}
