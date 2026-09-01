// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package flowviz

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

const goSDKPackage = "github.com/superdurable/dex/sdk-go/dex"

type goAnalyzer struct {
	graph        *Graph
	file         *ast.File
	fileSet      *token.FileSet
	typeInfo     *types.Info
	dexAliases   map[string]bool
	methods      map[string]map[string]*ast.FuncDecl
	steps        map[string]string
	resources    map[types.Object]string
	resourceVars map[string]string
}

type goTransition struct {
	kind         string
	target       string
	label        string
	condition    string
	multiplicity string
	span         *Span
	metadata     map[string]any
}

type goDecisionOutcome struct {
	decisionType    string
	condition       string
	transitions     []goTransition
	checkedChannels []string
	span            *Span
}

func analyzeGo(ctx context.Context, sourcePath string, source []byte) (*Graph, error) {
	graph := NewGraph("go", sourcePath)
	config := &packages.Config{
		Context: ctx,
		Dir:     filepath.Dir(sourcePath),
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps,
		Tests: false,
	}
	loaded, loadErr := packages.Load(config, "file="+sourcePath)
	if loadErr != nil {
		graph.AddDiagnostic("error", "go_load_failed", loadErr.Error(), nil)
	}
	var selectedPackage *packages.Package
	var selectedFile *ast.File
	for _, candidate := range loaded {
		for _, syntax := range candidate.Syntax {
			filename := candidate.Fset.Position(syntax.Pos()).Filename
			absoluteFilename, absoluteErr := filepath.Abs(filename)
			if absoluteErr == nil && absoluteFilename == sourcePath {
				selectedPackage = candidate
				selectedFile = syntax
				break
			}
		}
	}
	if selectedFile == nil {
		fileSet := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fileSet, sourcePath, source, parser.AllErrors)
		if parseErr != nil {
			graph.AddDiagnostic("error", "go_parse_failed", parseErr.Error(), nil)
			return graph, nil
		}
		selectedFile = parsed
		selectedPackage = &packages.Package{Fset: fileSet, TypesInfo: &types.Info{}}
	}
	for _, packageError := range selectedPackage.Errors {
		graph.AddDiagnostic("error", "go_type_check_failed", packageError.Msg, nil)
	}
	analyzer := newGoAnalyzer(graph, selectedFile, selectedPackage.Fset, selectedPackage.TypesInfo)
	analyzer.Analyze()
	return graph, nil
}

func newGoAnalyzer(graph *Graph, file *ast.File, fileSet *token.FileSet, typeInfo *types.Info) *goAnalyzer {
	if typeInfo == nil {
		typeInfo = &types.Info{}
	}
	return &goAnalyzer{
		graph:        graph,
		file:         file,
		fileSet:      fileSet,
		typeInfo:     typeInfo,
		dexAliases:   make(map[string]bool),
		methods:      make(map[string]map[string]*ast.FuncDecl),
		steps:        make(map[string]string),
		resources:    make(map[types.Object]string),
		resourceVars: make(map[string]string),
	}
}

func (analyzer *goAnalyzer) Analyze() {
	analyzer.indexImportsAndMethods()
	analyzer.analyzeResources()
	flowMethods := analyzer.findFlowMethods()
	if len(flowMethods) == 0 {
		analyzer.graph.AddDiagnostic("error", "flow_not_found", "source must define exactly one Flow with GetSteps", nil)
		return
	}
	if len(flowMethods) > 1 {
		analyzer.graph.AddDiagnostic("error", "multiple_flows", "source must define exactly one Flow", nil)
		for _, method := range flowMethods {
			analyzer.graph.AddNode(Node{ID: "unknown:flow:" + receiverTypeName(method), Kind: "unknown", Name: receiverTypeName(method), Span: analyzer.span(method)})
		}
		return
	}
	getSteps := flowMethods[0]
	flowName := receiverTypeName(getSteps)
	analyzer.graph.Flow = Flow{Name: analyzer.customTypeName(flowName, "GetFlowType"), Span: analyzer.span(getSteps)}
	analyzer.analyzeStepRegistration(getSteps)
	for stepType, nodeID := range analyzer.steps {
		analyzer.analyzeStep(stepType, nodeID)
	}
	analyzer.analyzeFlowHandlers(flowName)
}

func (analyzer *goAnalyzer) indexImportsAndMethods() {
	for _, importSpec := range analyzer.file.Imports {
		path, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil || path != goSDKPackage {
			continue
		}
		alias := "dex"
		if importSpec.Name != nil {
			alias = importSpec.Name.Name
		}
		analyzer.dexAliases[alias] = true
	}
	for _, declaration := range analyzer.file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil {
			continue
		}
		receiver := receiverTypeName(function)
		if receiver == "" {
			continue
		}
		if analyzer.methods[receiver] == nil {
			analyzer.methods[receiver] = make(map[string]*ast.FuncDecl)
		}
		analyzer.methods[receiver][function.Name.Name] = function
	}
}

func (analyzer *goAnalyzer) analyzeResources() {
	for _, declaration := range analyzer.file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, specification := range general.Specs {
			valueSpec, valueOK := specification.(*ast.ValueSpec)
			if !valueOK {
				continue
			}
			for index, name := range valueSpec.Names {
				if index >= len(valueSpec.Values) {
					continue
				}
				call, callOK := valueSpec.Values[index].(*ast.CallExpr)
				if !callOK {
					continue
				}
				kind := goResourceKind(analyzer.callName(call))
				if kind == "" {
					continue
				}
				resourceName := name.Name
				if len(call.Args) > 0 {
					if staticName, ok := analyzer.staticString(call.Args[0]); ok {
						resourceName = staticName
					} else {
						analyzer.graph.AddDiagnostic("error", "dynamic_resource_name", fmt.Sprintf("resource %s must use a static name", name.Name), analyzer.span(call.Args[0]))
					}
				}
				nodeID := "resource:" + kind + ":" + name.Name
				resource := analyzer.resourceDetails(call)
				if resource.ValueType == "unknown" {
					analyzer.graph.AddDiagnostic("warning", "unknown_resource_type", fmt.Sprintf("%s %s has no statically readable value type", kind, resourceName), analyzer.span(call))
				}
				analyzer.graph.AddNode(Node{ID: nodeID, Kind: kind, Name: resourceName, Span: analyzer.span(valueSpec), Resource: &resource})
				analyzer.resourceVars[name.Name] = nodeID
				if object := analyzer.typeInfo.Defs[name]; object != nil {
					analyzer.resources[object] = nodeID
				}
			}
		}
	}
}

func (analyzer *goAnalyzer) findFlowMethods() []*ast.FuncDecl {
	methods := make([]*ast.FuncDecl, 0)
	for _, declaration := range analyzer.file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv != nil && function.Name.Name == "GetSteps" && analyzer.isFlowGetSteps(function) {
			methods = append(methods, function)
		}
	}
	return methods
}

func (analyzer *goAnalyzer) isFlowGetSteps(method *ast.FuncDecl) bool {
	if method.Type.Results == nil || len(method.Type.Results.List) != 1 {
		return false
	}
	result := method.Type.Results.List[0].Type
	if typeAndValue, ok := analyzer.typeInfo.Types[result]; ok && typeAndValue.Type != nil {
		return strings.Contains(typeAndValue.Type.String(), goSDKPackage+".StepDef")
	}
	return strings.Contains(analyzer.expressionString(result), "StepDef")
}

func (analyzer *goAnalyzer) analyzeStepRegistration(getSteps *ast.FuncDecl) {
	if getSteps.Body == nil {
		analyzer.graph.AddDiagnostic("error", "dynamic_step_registration", "GetSteps must have a directly visible body", analyzer.span(getSteps))
		return
	}
	registrationCount := 0
	ast.Inspect(getSteps.Body, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok {
			return true
		}
		callName := analyzer.callName(call)
		if callName != "DefineStep" && callName != "DefineStartStep" {
			return true
		}
		registrationCount++
		if len(call.Args) == 0 {
			analyzer.addDynamicTargetDiagnostic("step registration has no static target", call)
			return false
		}
		stepType := analyzer.expressionTypeName(call.Args[0])
		if stepType == "" {
			analyzer.addDynamicTargetDiagnostic("registered Step type must be static", call.Args[0])
			return false
		}
		stepName := analyzer.customTypeName(stepType, "GetStepType")
		nodeID := "step:" + stepType
		isStart := callName == "DefineStartStep"
		analyzer.steps[stepType] = nodeID
		analyzer.graph.AddNode(Node{ID: nodeID, Kind: "step", Name: stepName, Start: isStart, Span: analyzer.span(call)})
		if isStart {
			if analyzer.graph.Flow.StartStepID != "" {
				analyzer.graph.AddDiagnostic("error", "multiple_start_steps", "Flow defines more than one start Step", analyzer.span(call))
			} else {
				analyzer.graph.Flow.StartStepID = nodeID
			}
		}
		return false
	})
	if registrationCount == 0 {
		analyzer.graph.AddDiagnostic("error", "dynamic_step_registration", "GetSteps must directly call DefineStep or DefineStartStep", analyzer.span(getSteps))
	}
}

func (analyzer *goAnalyzer) analyzeStep(stepType string, nodeID string) {
	stepMethods := analyzer.methods[stepType]
	if stepMethods == nil || stepMethods["Execute"] == nil {
		analyzer.graph.AddDiagnostic("error", "step_handler_not_in_file", fmt.Sprintf("Step %s Execute must be defined in the source file", stepType), nil)
		return
	}
	phase := "execute"
	if stepMethods["WaitFor"] != nil {
		phase = "wait_for+execute"
	}
	analyzer.graph.SetNodePhase(nodeID, phase)
	execute := stepMethods["Execute"]
	analyzer.analyzeDecisionHandler(nodeID, execute, "execute")
	analyzer.analyzeResourceAccess(nodeID, execute, "execute")
	if waitFor := stepMethods["WaitFor"]; waitFor != nil {
		analyzer.analyzeWaitFor(nodeID, stepType, waitFor)
	}
	if options := stepMethods["GetStepOptions"]; options != nil {
		analyzer.analyzeFailurePolicy(nodeID, options)
	}
}

func (analyzer *goAnalyzer) analyzeFlowHandlers(flowType string) {
	for name, method := range analyzer.methods[flowType] {
		switch name {
		case "GetSteps", "GetPersistenceSchema", "GetFlowType", "GetFlowOptions", "GetFlowConfig":
			continue
		case "HandleTimeout":
			nodeID := "timeout_handler:" + flowType
			analyzer.graph.AddNode(Node{ID: nodeID, Kind: "timeout_handler", Name: "handleTimeout", Span: analyzer.span(method)})
			analyzer.analyzeDecisionHandler(nodeID, method, "timeout")
			analyzer.analyzeResourceAccess(nodeID, method, "timeout")
		default:
			if !ast.IsExported(name) || !analyzer.isRPCMethod(method) {
				continue
			}
			nodeID := "rpc:" + name
			analyzer.graph.AddNode(Node{ID: nodeID, Kind: "rpc", Name: name, Span: analyzer.span(method)})
			analyzer.analyzeDecisionHandler(nodeID, method, "rpc")
			analyzer.analyzeResourceAccess(nodeID, method, "rpc")
		}
	}
}

func (analyzer *goAnalyzer) analyzeDecisionHandler(ownerID string, method *ast.FuncDecl, phase string) {
	if method.Body == nil {
		return
	}
	localMovements := analyzer.collectLocalMovements(method.Body)
	outcomes := make([]goDecisionOutcome, 0)
	analyzer.walkStatements(method.Body.List, "", func(expression ast.Expr, condition string) bool {
		decisionType := analyzer.decisionType(expression, phase == "rpc")
		if decisionType == "" {
			return false
		}
		outcomes = append(outcomes, goDecisionOutcome{
			decisionType:    decisionType,
			condition:       condition,
			transitions:     analyzer.transitionsFromExpression(expression, condition, phase == "rpc", localMovements),
			checkedChannels: analyzer.checkedChannels(expression),
			span:            analyzer.span(expression),
		})
		return true
	})
	hasCondition := false
	for _, outcome := range outcomes {
		if outcome.condition != "" {
			hasCondition = true
			break
		}
	}
	if hasCondition && len(outcomes) > 1 {
		for index := range outcomes {
			if outcomes[index].condition == "" {
				outcomes[index].condition = "otherwise"
			}
		}
	}
	if len(outcomes) == 0 && analyzer.returnsStepDecisionCall(method) {
		unknownID := fmt.Sprintf("unknown:%s:%d", ownerID, analyzer.fileSet.Position(method.Pos()).Line)
		analyzer.graph.AddNode(Node{ID: unknownID, Kind: "unknown", Name: "Dynamic Dex decision", Span: analyzer.span(method)})
		analyzer.graph.AddEdge(Edge{Kind: "transition", From: ownerID, To: unknownID, Label: "dynamic", Span: analyzer.span(method)})
		analyzer.graph.AddDiagnostic("error", "hidden_dex_decision", fmt.Sprintf("%s hides its Dex decision in a helper", method.Name.Name), analyzer.span(method))
		return
	}
	if len(outcomes) > 1 {
		dispatchID := "decision-dispatch:" + ownerID
		analyzer.graph.AddNode(Node{ID: dispatchID, Kind: "decision_dispatch", Name: "Decision", ParentID: ownerID, Phase: phase, Span: analyzer.span(method)})
	}
	for _, outcome := range outcomes {
		position := analyzer.fileSet.Position(method.Pos())
		if outcome.span != nil {
			position.Line = outcome.span.StartLine
			position.Column = outcome.span.StartColumn
		}
		decisionID := fmt.Sprintf("decision:%s:%d:%d", ownerID, position.Line, position.Column)
		details := DecisionDetails{Type: outcome.decisionType, CheckedChannels: outcome.checkedChannels}
		for _, transition := range outcome.transitions {
			if transition.kind != "cancel" {
				continue
			}
			targetID := analyzer.resolveTransitionTarget(transition.target, transition.span)
			details.Cancellations = append(details.Cancellations, Cancellation{StepID: targetID, Scope: cancellationScope(transition.label)})
		}
		analyzer.graph.AddNode(Node{
			ID: decisionID, Kind: "decision", Name: outcome.decisionType, ParentID: ownerID,
			Condition: outcome.condition, Phase: phase, Span: outcome.span, Decision: &details,
		})
		for _, transition := range outcome.transitions {
			if transition.kind == "terminal" {
				continue
			}
			targetID := analyzer.resolveTransitionTarget(transition.target, transition.span)
			analyzer.graph.AddEdge(Edge{
				Kind:         transition.kind,
				From:         decisionID,
				To:           targetID,
				Label:        transition.label,
				Multiplicity: transition.multiplicity,
				Span:         transition.span,
				Metadata:     transition.metadata,
			})
		}
	}
}

func (analyzer *goAnalyzer) analyzeWaitFor(ownerID string, stepType string, method *ast.FuncDecl) {
	type waitReturn struct {
		call      *ast.CallExpr
		condition string
	}
	returns := make([]waitReturn, 0)
	analyzer.walkStatements(method.Body.List, "", func(expression ast.Expr, condition string) bool {
		call, ok := expression.(*ast.CallExpr)
		if !ok || goWaitType(analyzer.callName(call)) == "" {
			return false
		}
		returns = append(returns, waitReturn{call: call, condition: condition})
		return true
	})
	if len(returns) == 0 {
		unknownID := fmt.Sprintf("unknown:wait:%s:%d", stepType, analyzer.fileSet.Position(method.Pos()).Line)
		analyzer.graph.AddNode(Node{ID: unknownID, Kind: "unknown", Name: "Dynamic WaitFor", ParentID: ownerID, Span: analyzer.span(method)})
		analyzer.graph.AddDiagnostic("error", "hidden_dex_wait", fmt.Sprintf("%s hides its Dex Wait in a helper", method.Name.Name), analyzer.span(method))
		return
	}
	if len(returns) > 1 {
		analyzer.graph.AddNode(Node{ID: "wait-dispatch:" + stepType, Kind: "wait_dispatch", Name: "WaitFor", ParentID: ownerID, Phase: "wait_for", Span: analyzer.span(method)})
	}
	for _, result := range returns {
		position := analyzer.fileSet.Position(result.call.Pos())
		waitID := fmt.Sprintf("wait:%s:%d:%d", stepType, position.Line, position.Column)
		waitType := goWaitType(analyzer.callName(result.call))
		conditions := analyzer.goWaitConditions(waitID, result.call)
		analyzer.graph.AddNode(Node{
			ID: waitID, Kind: "wait", Name: waitType, ParentID: ownerID, Condition: result.condition,
			Phase: "wait_for", Span: analyzer.span(result.call), Wait: &WaitDetails{Type: waitType, Conditions: conditions},
		})
	}
	analyzer.analyzeResourceAccess(ownerID, method, "wait_for")
}

func (analyzer *goAnalyzer) analyzeFailurePolicy(ownerID string, method *ast.FuncDecl) {
	ast.Inspect(method.Body, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok || analyzer.callName(call) != "ProceedToOnExecuteFailure" {
			return true
		}
		if len(call.Args) == 0 {
			analyzer.addDynamicTargetDiagnostic("execute failure recovery target must be static", call)
			return false
		}
		target := analyzer.expressionTypeName(call.Args[0])
		metadata := map[string]any{}
		if target != "" {
			metadata["skipWaitFor"] = analyzer.methods[target]["WaitFor"] == nil
		}
		targetID := analyzer.resolveTransitionTarget(target, analyzer.span(call.Args[0]))
		analyzer.graph.AddEdge(Edge{Kind: "failure_transition", From: ownerID, To: targetID, Label: "Execute failure", Span: analyzer.span(call), Metadata: metadata})
		return false
	})
}

func (analyzer *goAnalyzer) analyzeResourceAccess(ownerID string, method *ast.FuncDecl, phase string) {
	seen := make(map[string]bool)
	ast.Inspect(method.Body, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, selectorOK := unwrapCallFun(call.Fun).(*ast.SelectorExpr)
		if !selectorOK {
			return true
		}
		resourceID := analyzer.resourceForExpression(selector.X)
		if resourceID == "" {
			return true
		}
		kind := goResourceEdgeKind(selector.Sel.Name, phase)
		if kind == "" || kind == "wait_condition" {
			return true
		}
		key := kind + ":" + resourceID + ":" + selector.Sel.Name
		if seen[key] {
			return true
		}
		seen[key] = true
		metadata := map[string]any{"phase": phase}
		if strings.HasPrefix(resourceID, "resource:stream:") && selector.Sel.Name == "Write" {
			metadata["bestEffort"] = true
			metadata["repeatable"] = true
			metadata["role"] = "progress"
			if phase == "rpc" || phase == "timeout" {
				analyzer.graph.AddDiagnostic("error", "step_progress_outside_step", "Stream.Write is only available in WaitFor and Execute", analyzer.span(call))
			}
		}
		from := ownerID
		to := resourceID
		if kind == "resource_read" {
			from, to = resourceID, ownerID
		}
		analyzer.graph.AddEdge(Edge{Kind: kind, From: from, To: to, Label: selector.Sel.Name, Span: analyzer.span(call), Metadata: metadata})
		return true
	})
}

func (analyzer *goAnalyzer) decisionType(expression ast.Expr, allowRPC bool) string {
	if allowRPC {
		if typeAndValue, ok := analyzer.typeInfo.Types[expression]; ok && typeAndValue.Type != nil && strings.Contains(typeAndValue.Type.String(), goSDKPackage+".RPCResult") {
			return "rpcResult"
		}
	}
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return ""
	}
	if selector, selectorOK := unwrapCallFun(call.Fun).(*ast.SelectorExpr); selectorOK && (selector.Sel.Name == "CancelSteps" || selector.Sel.Name == "CancelSiblingSteps") {
		return analyzer.decisionType(selector.X, allowRPC)
	}
	switch analyzer.callName(call) {
	case "GoTo":
		return "goTo"
	case "GoToMany":
		return "goToMany"
	case "GracefulComplete":
		return "gracefulComplete"
	case "ForceComplete":
		return "forceComplete"
	case "ForceFail":
		return "forceFail"
	case "DeadEnd":
		return "deadEnd"
	case "ForceCompleteIfChannelsEmpty":
		return "forceCompleteIfChannelsEmpty"
	case "RPCResult":
		if allowRPC {
			return "rpcResult"
		}
	}
	return ""
}

func (analyzer *goAnalyzer) checkedChannels(expression ast.Expr) []string {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return nil
	}
	if selector, selectorOK := unwrapCallFun(call.Fun).(*ast.SelectorExpr); selectorOK && (selector.Sel.Name == "CancelSteps" || selector.Sel.Name == "CancelSiblingSteps") {
		return analyzer.checkedChannels(selector.X)
	}
	if analyzer.callName(call) != "ForceCompleteIfChannelsEmpty" || len(call.Args) < 2 {
		return nil
	}
	channels := make([]string, 0)
	ast.Inspect(call.Args[1], func(current ast.Node) bool {
		expression, ok := current.(ast.Expr)
		if !ok {
			return true
		}
		if resourceID := analyzer.resourceForExpression(expression); strings.HasPrefix(resourceID, "resource:channel:") {
			channels = appendUnique(channels, resourceID)
			return false
		}
		return true
	})
	return channels
}

func (analyzer *goAnalyzer) goWaitConditions(waitID string, waitCall *ast.CallExpr) []WaitCondition {
	conditions := make([]WaitCondition, 0)
	ast.Inspect(waitCall, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok || call == waitCall {
			return true
		}
		name := analyzer.callName(call)
		switch name {
		case "Timer":
			expression := "duration"
			if len(call.Args) > 0 {
				expression = analyzer.expressionString(call.Args[0])
			}
			conditions = append(conditions, WaitCondition{
				Kind: "timer", Label: humanizeGoDuration(expression) + " timer", Expression: expression, Span: analyzer.span(call),
			})
			return false
		case "SubFlow":
			name := "SubFlow"
			if len(call.Args) > 0 {
				name = valueOr(analyzer.expressionTypeName(call.Args[0]), name)
			}
			subFlowID := fmt.Sprintf("subflow:%s:%d", name, analyzer.fileSet.Position(call.Pos()).Line)
			analyzer.graph.AddNode(Node{ID: subFlowID, Kind: "subflow", Name: name, External: true, Span: analyzer.span(call)})
			analyzer.graph.AddEdge(Edge{Kind: "subflow", From: waitID, To: subFlowID, Label: "start", Span: analyzer.span(call)})
			conditions = append(conditions, WaitCondition{Kind: "subflow", Label: name, SubFlowID: subFlowID, Span: analyzer.span(call)})
			return false
		}
		selector, selectorOK := unwrapCallFun(call.Fun).(*ast.SelectorExpr)
		if !selectorOK || !isGoChannelCondition(name) {
			return true
		}
		resourceID := analyzer.resourceForExpression(selector.X)
		if !strings.HasPrefix(resourceID, "resource:channel:") {
			return true
		}
		label, expression := analyzer.goChannelConditionLabel(resourceID, name, call.Args)
		conditions = append(conditions, WaitCondition{
			Kind: "channel", Label: label, ResourceID: resourceID, Expression: expression, Span: analyzer.span(call),
		})
		analyzer.graph.AddEdge(Edge{Kind: "wait_condition", From: resourceID, To: waitID, Label: label, Span: analyzer.span(call)})
		return false
	})
	return conditions
}

func (analyzer *goAnalyzer) goChannelConditionLabel(resourceID string, method string, arguments []ast.Expr) (string, string) {
	resource := analyzer.node(resourceID)
	name := resource.Name
	isMap := resource.Resource != nil && resource.Resource.Map
	argumentStrings := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		argumentStrings = append(argumentStrings, analyzer.expressionString(argument))
	}
	instance := ""
	counts := argumentStrings
	if isMap && len(argumentStrings) > 0 {
		instance = "[" + argumentStrings[0] + "]"
		counts = argumentStrings[1:]
	}
	count := func(index int, fallback string) string {
		if index < len(counts) {
			return counts[index]
		}
		return fallback
	}
	label := name + instance
	switch method {
	case "ForOne":
		label += ".for 1"
	case "ForN":
		label += ".for " + count(0, "N")
	case "AtLeast":
		label += ".at least " + count(0, "N")
	case "AtMost":
		label += ".at most " + count(0, "N")
	case "AtLeastAtMost":
		label += ".for " + count(0, "N") + "…" + count(1, "N")
	}
	return label, strings.Join(argumentStrings, ", ")
}

func (analyzer *goAnalyzer) node(nodeID string) Node {
	for _, node := range analyzer.graph.Nodes {
		if node.ID == nodeID {
			return node
		}
	}
	return Node{}
}

func (analyzer *goAnalyzer) transitionsFromExpression(expression ast.Expr, condition string, allowMovement bool, locals map[string][]goTransition) []goTransition {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		if allowMovement {
			result := make([]goTransition, 0)
			ast.Inspect(expression, func(current ast.Node) bool {
				movement, movementOK := current.(*ast.CallExpr)
				if movementOK && analyzer.callName(movement) == "MovementOf" {
					result = append(result, analyzer.movementTransition(movement, condition))
					return false
				}
				return true
			})
			return result
		}
		return nil
	}
	if selector, selectorOK := unwrapCallFun(call.Fun).(*ast.SelectorExpr); selectorOK && (selector.Sel.Name == "CancelSteps" || selector.Sel.Name == "CancelSiblingSteps") {
		result := analyzer.transitionsFromExpression(selector.X, condition, allowMovement, locals)
		for _, argument := range call.Args {
			target := analyzer.expressionTypeName(argument)
			result = append(result, goTransition{kind: "cancel", target: target, label: selector.Sel.Name, condition: condition, span: analyzer.span(argument)})
		}
		return result
	}
	name := analyzer.callName(call)
	switch name {
	case "GoTo":
		if len(call.Args) == 0 {
			return []goTransition{{kind: "transition", target: "", label: "GoTo", condition: condition, span: analyzer.span(call)}}
		}
		return []goTransition{{kind: "transition", target: analyzer.expressionTypeName(call.Args[0]), label: "GoTo", condition: condition, span: analyzer.span(call)}}
	case "GoToMany":
		result := make([]goTransition, 0)
		for _, argument := range call.Args {
			if movementCall, movementOK := argument.(*ast.CallExpr); movementOK && analyzer.callName(movementCall) == "MovementOf" {
				result = append(result, analyzer.movementTransition(movementCall, condition))
				continue
			}
			if identifier, identifierOK := argument.(*ast.Ident); identifierOK {
				for _, movement := range locals[identifier.Name] {
					movement.condition = condition
					if call.Ellipsis.IsValid() {
						movement.multiplicity = "×N"
					}
					result = append(result, movement)
				}
			}
		}
		return result
	case "ForceCompleteIfChannelsEmpty":
		result := make([]goTransition, 0)
		for _, argument := range call.Args[2:] {
			if movementCall, movementOK := argument.(*ast.CallExpr); movementOK && analyzer.callName(movementCall) == "MovementOf" {
				result = append(result, analyzer.movementTransition(movementCall, condition))
			}
		}
		if len(result) == 0 {
			result = append(result, goTransition{kind: "terminal", target: name, label: terminalLabel(name), condition: condition, span: analyzer.span(call)})
		}
		return result
	case "MovementOf":
		if allowMovement {
			return []goTransition{analyzer.movementTransition(call, condition)}
		}
	case "GracefulComplete", "ForceComplete", "ForceFail", "DeadEnd":
		return []goTransition{{kind: "terminal", target: name, label: terminalLabel(name), condition: condition, span: analyzer.span(call)}}
	}
	return nil
}

func (analyzer *goAnalyzer) movementTransition(call *ast.CallExpr, condition string) goTransition {
	target := ""
	if len(call.Args) > 0 {
		target = analyzer.expressionTypeName(call.Args[0])
	}
	return goTransition{kind: "transition", target: target, label: "fan-out", condition: condition, span: analyzer.span(call)}
}

func (analyzer *goAnalyzer) collectLocalMovements(body *ast.BlockStmt) map[string][]goTransition {
	locals := make(map[string][]goTransition)
	ast.Inspect(body, func(current ast.Node) bool {
		switch node := current.(type) {
		case *ast.AssignStmt:
			for index, left := range node.Lhs {
				identifier, ok := left.(*ast.Ident)
				if !ok || index >= len(node.Rhs) {
					continue
				}
				literal, literalOK := node.Rhs[index].(*ast.CompositeLit)
				if !literalOK {
					continue
				}
				for _, element := range literal.Elts {
					if call, callOK := element.(*ast.CallExpr); callOK && analyzer.callName(call) == "MovementOf" {
						locals[identifier.Name] = append(locals[identifier.Name], analyzer.movementTransition(call, ""))
					}
				}
			}
		case *ast.CallExpr:
			if analyzer.callName(node) != "append" || len(node.Args) < 2 {
				return true
			}
			identifier, ok := node.Args[0].(*ast.Ident)
			movement, movementOK := node.Args[1].(*ast.CallExpr)
			if !ok || !movementOK || analyzer.callName(movement) != "MovementOf" {
				return true
			}
			transition := analyzer.movementTransition(movement, "")
			transition.multiplicity = "×N"
			locals[identifier.Name] = append(locals[identifier.Name], transition)
		}
		return true
	})
	return locals
}

func (analyzer *goAnalyzer) walkStatements(statements []ast.Stmt, condition string, visit func(ast.Expr, string) bool) bool {
	hasOutcome := false
	for _, statement := range statements {
		switch current := statement.(type) {
		case *ast.ReturnStmt:
			for _, result := range current.Results {
				hasOutcome = visit(result, condition) || hasOutcome
			}
		case *ast.IfStmt:
			branch := analyzer.expressionString(current.Cond)
			bodyHasOutcome := analyzer.walkStatements(current.Body.List, combineCondition(condition, branch), visit)
			hasOutcome = bodyHasOutcome || hasOutcome
			if current.Else != nil {
				fallbackCondition := combineCondition(condition, negateGoCondition(branch))
				switch otherwise := current.Else.(type) {
				case *ast.BlockStmt:
					hasOutcome = analyzer.walkStatements(otherwise.List, fallbackCondition, visit) || hasOutcome
				case *ast.IfStmt:
					hasOutcome = analyzer.walkStatements([]ast.Stmt{otherwise}, fallbackCondition, visit) || hasOutcome
				}
			} else if bodyHasOutcome && statementsAlwaysReturn(current.Body.List) {
				condition = combineCondition(condition, negateGoCondition(branch))
			}
		case *ast.SwitchStmt:
			tag := analyzer.expressionString(current.Tag)
			for _, clauseNode := range current.Body.List {
				clause, ok := clauseNode.(*ast.CaseClause)
				if !ok {
					continue
				}
				branch := "default"
				if len(clause.List) > 0 {
					values := make([]string, 0, len(clause.List))
					for _, value := range clause.List {
						values = append(values, analyzer.expressionString(value))
					}
					branch = tag + " == " + strings.Join(values, " or ")
				}
				hasOutcome = analyzer.walkStatements(clause.Body, combineCondition(condition, branch), visit) || hasOutcome
			}
		case *ast.BlockStmt:
			hasOutcome = analyzer.walkStatements(current.List, condition, visit) || hasOutcome
		}
	}
	return hasOutcome
}

func statementsAlwaysReturn(statements []ast.Stmt) bool {
	if len(statements) == 0 {
		return false
	}
	switch last := statements[len(statements)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BlockStmt:
		return statementsAlwaysReturn(last.List)
	case *ast.IfStmt:
		if last.Else == nil || !statementsAlwaysReturn(last.Body.List) {
			return false
		}
		switch otherwise := last.Else.(type) {
		case *ast.BlockStmt:
			return statementsAlwaysReturn(otherwise.List)
		case *ast.IfStmt:
			return statementsAlwaysReturn([]ast.Stmt{otherwise})
		}
	}
	return false
}

func (analyzer *goAnalyzer) resolveTransitionTarget(target string, span *Span) string {
	if nodeID := analyzer.steps[target]; nodeID != "" {
		return nodeID
	}
	unknownID := "unknown:step:dynamic"
	message := "Dex transition target must be a registered static Step"
	if target != "" {
		unknownID = "unknown:step:" + target
		message = fmt.Sprintf("Step %s is not registered in this Flow", target)
	}
	analyzer.graph.AddNode(Node{ID: unknownID, Kind: "unknown", Name: valueOr(target, "Dynamic Step"), Span: span})
	analyzer.graph.AddDiagnostic("error", "unknown_step_target", message, span)
	return unknownID
}

func (analyzer *goAnalyzer) resourceForExpression(expression ast.Expr) string {
	switch current := expression.(type) {
	case *ast.Ident:
		if object := analyzer.typeInfo.Uses[current]; object != nil {
			if nodeID := analyzer.resources[object]; nodeID != "" {
				return nodeID
			}
		}
		return analyzer.resourceVars[current.Name]
	case *ast.SelectorExpr:
		return analyzer.resourceVars[current.Sel.Name]
	}
	return ""
}

func (analyzer *goAnalyzer) expressionTypeName(expression ast.Expr) string {
	switch current := expression.(type) {
	case *ast.CompositeLit:
		return baseTypeName(current.Type)
	case *ast.UnaryExpr:
		return analyzer.expressionTypeName(current.X)
	case *ast.CallExpr:
		if name := baseTypeName(unwrapCallFun(current.Fun)); name != "" && !analyzer.dexAliases[name] {
			return name
		}
	case *ast.Ident, *ast.SelectorExpr:
		if typeAndValue, ok := analyzer.typeInfo.Types[expression]; ok {
			if name := namedTypeName(typeAndValue.Type); name != "" {
				return name
			}
		}
		return baseTypeName(expression)
	}
	if typeAndValue, ok := analyzer.typeInfo.Types[expression]; ok {
		return namedTypeName(typeAndValue.Type)
	}
	return ""
}

func (analyzer *goAnalyzer) callName(call *ast.CallExpr) string {
	function := unwrapCallFun(call.Fun)
	switch current := function.(type) {
	case *ast.Ident:
		if object := analyzer.typeInfo.Uses[current]; object != nil && object.Pkg() != nil && object.Pkg().Path() == goSDKPackage {
			return object.Name()
		}
		return current.Name
	case *ast.SelectorExpr:
		if object := analyzer.typeInfo.Uses[current.Sel]; object != nil && object.Pkg() != nil && object.Pkg().Path() == goSDKPackage {
			return object.Name()
		}
		if identifier, ok := current.X.(*ast.Ident); ok && analyzer.dexAliases[identifier.Name] {
			return current.Sel.Name
		}
		return ""
	default:
		return ""
	}
}

func (analyzer *goAnalyzer) customTypeName(typeName string, methodName string) string {
	method := analyzer.methods[typeName][methodName]
	if method == nil || method.Body == nil {
		return typeName
	}
	for _, statement := range method.Body.List {
		returnStatement, ok := statement.(*ast.ReturnStmt)
		if !ok || len(returnStatement.Results) == 0 {
			continue
		}
		if value, isStatic := analyzer.staticString(returnStatement.Results[0]); isStatic {
			if value == "" {
				return typeName
			}
			return value
		}
	}
	analyzer.graph.AddDiagnostic("error", "dynamic_type_name", fmt.Sprintf("%s must return a compile-time string", methodName), analyzer.span(method))
	return typeName
}

func (analyzer *goAnalyzer) staticString(expression ast.Expr) (string, bool) {
	if basic, ok := expression.(*ast.BasicLit); ok && basic.Kind == token.STRING {
		value, err := strconv.Unquote(basic.Value)
		return value, err == nil
	}
	if typeAndValue, ok := analyzer.typeInfo.Types[expression]; ok && typeAndValue.Value != nil && typeAndValue.Value.Kind() == constant.String {
		return constant.StringVal(typeAndValue.Value), true
	}
	return "", false
}

func (analyzer *goAnalyzer) isRPCMethod(method *ast.FuncDecl) bool {
	if method.Type.Results == nil {
		return false
	}
	for _, result := range method.Type.Results.List {
		if strings.Contains(analyzer.expressionString(result.Type), "RPCResult") {
			return true
		}
	}
	return false
}

func (analyzer *goAnalyzer) bodyHasDexCall(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if ok && analyzer.callName(call) == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func (analyzer *goAnalyzer) returnsStepDecisionCall(method *ast.FuncDecl) bool {
	found := false
	ast.Inspect(method.Body, func(current ast.Node) bool {
		returnStatement, ok := current.(*ast.ReturnStmt)
		if !ok || len(returnStatement.Results) == 0 {
			return true
		}
		call, callOK := returnStatement.Results[0].(*ast.CallExpr)
		if !callOK {
			return true
		}
		if typeAndValue, typeOK := analyzer.typeInfo.Types[call]; typeOK && strings.Contains(typeAndValue.Type.String(), "StepDecision") {
			found = true
		}
		return !found
	})
	return found
}

func (analyzer *goAnalyzer) expressionString(expression ast.Expr) string {
	if expression == nil {
		return ""
	}
	var output bytes.Buffer
	if err := format.Node(&output, analyzer.fileSet, expression); err != nil {
		return "condition"
	}
	return output.String()
}

func (analyzer *goAnalyzer) span(node ast.Node) *Span {
	if node == nil {
		return nil
	}
	start := analyzer.fileSet.Position(node.Pos())
	end := analyzer.fileSet.Position(node.End())
	return &Span{StartLine: start.Line, StartColumn: start.Column, EndLine: end.Line, EndColumn: end.Column}
}

func (analyzer *goAnalyzer) addDynamicTargetDiagnostic(message string, node ast.Node) {
	span := analyzer.span(node)
	unknownID := fmt.Sprintf("unknown:step:%d", span.StartLine)
	analyzer.graph.AddNode(Node{ID: unknownID, Kind: "unknown", Name: "Dynamic Step", Span: span})
	analyzer.graph.AddDiagnostic("error", "dynamic_step_target", message, span)
}

func receiverTypeName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return ""
	}
	return baseTypeName(function.Recv.List[0].Type)
}

func baseTypeName(expression ast.Expr) string {
	switch current := expression.(type) {
	case *ast.Ident:
		return current.Name
	case *ast.StarExpr:
		return baseTypeName(current.X)
	case *ast.IndexExpr:
		return baseTypeName(current.X)
	case *ast.IndexListExpr:
		return baseTypeName(current.X)
	case *ast.SelectorExpr:
		return current.Sel.Name
	}
	return ""
}

func namedTypeName(value types.Type) string {
	for {
		switch current := value.(type) {
		case *types.Pointer:
			value = current.Elem()
		case *types.Named:
			if current.Obj() != nil {
				return current.Obj().Name()
			}
			return ""
		default:
			return ""
		}
	}
}

func unwrapCallFun(expression ast.Expr) ast.Expr {
	switch current := expression.(type) {
	case *ast.IndexExpr:
		return unwrapCallFun(current.X)
	case *ast.IndexListExpr:
		return unwrapCallFun(current.X)
	default:
		return expression
	}
}

func (analyzer *goAnalyzer) resourceDetails(call *ast.CallExpr) ResourceDetails {
	details := ResourceDetails{ValueType: "unknown"}
	name := analyzer.callName(call)
	details.Map = name == "DefineAttributeMap" || name == "DefineChannelMap"
	switch function := call.Fun.(type) {
	case *ast.IndexExpr:
		details.ValueType = analyzer.expressionString(function.Index)
	case *ast.IndexListExpr:
		if len(function.Indices) > 0 {
			details.ValueType = analyzer.expressionString(function.Indices[0])
		}
	}
	if details.ValueType == "" {
		details.ValueType = "unknown"
	}
	return details
}

func goWaitType(name string) string {
	switch name {
	case "SkipWaitImmediately":
		return "skipWaitImmediately"
	case "Until":
		return "until"
	case "AllOf":
		return "allOf"
	case "AnyOf":
		return "anyOf"
	case "AnyComboOf":
		return "anyComboOf"
	default:
		return ""
	}
}

func isGoChannelCondition(name string) bool {
	switch name {
	case "ForOne", "ForN", "AtLeast", "AtMost", "AtLeastAtMost":
		return true
	default:
		return false
	}
}

func humanizeGoDuration(expression string) string {
	trimmed := strings.TrimSpace(expression)
	units := []struct {
		source string
		label  string
	}{
		{source: "time.Nanosecond", label: "nanosecond"},
		{source: "time.Microsecond", label: "microsecond"},
		{source: "time.Millisecond", label: "millisecond"},
		{source: "time.Second", label: "second"},
		{source: "time.Minute", label: "minute"},
		{source: "time.Hour", label: "hour"},
	}
	for _, unit := range units {
		if trimmed == unit.source {
			return "1 " + unit.label
		}
		suffix := " * " + unit.source
		if strings.HasSuffix(trimmed, suffix) {
			count := strings.TrimSpace(strings.TrimSuffix(trimmed, suffix))
			if _, err := strconv.Atoi(count); err == nil {
				label := unit.label
				if count != "1" {
					label += "s"
				}
				return count + " " + label
			}
		}
	}
	return trimmed
}

func cancellationScope(label string) string {
	if label == "CancelSiblingSteps" {
		return "siblings"
	}
	return "all"
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func goResourceKind(name string) string {
	switch name {
	case "DefineAttribute", "DefineAttributeMap":
		return "attribute"
	case "DefineChannel", "DefineChannelMap":
		return "channel"
	case "DefineStream":
		return "stream"
	default:
		return ""
	}
}

func goResourceEdgeKind(method string, phase string) string {
	if phase == "wait_for" {
		switch method {
		case "ForOne", "ForN", "AtLeast", "AtMost", "AtLeastAtMost":
			return "wait_condition"
		}
	}
	switch method {
	case "Get", "Size", "MapSize", "AllInstanceKeys", "GetConditionResults":
		return "resource_read"
	case "Set", "Delete":
		return "resource_write"
	case "Publish":
		return "resource_publish"
	case "Write":
		return "resource_write"
	default:
		return ""
	}
}

func terminalLabel(name string) string {
	switch name {
	case "GracefulComplete":
		return "Graceful complete"
	case "ForceComplete":
		return "Force complete"
	case "ForceFail":
		return "Force fail"
	case "DeadEnd":
		return "Dead end"
	case "ForceCompleteIfChannelsEmpty":
		return "Complete if channels empty"
	default:
		return name
	}
}

func combineCondition(parent string, child string) string {
	if parent == "" {
		return child
	}
	return parent + " and " + child
}

func negateGoCondition(condition string) string {
	return "!(" + condition + ")"
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
