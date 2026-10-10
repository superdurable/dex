// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"

	"github.com/superdurable/dex/service/common/ptr"
)

type goConditionReturnCollector struct {
	returnExpression ast.Expr
	returnCount      int
}

type goWaitConditionCollector struct {
	analyzer   *goAnalyzer
	waitID     string
	conditions []WaitCondition
}

func (collector *goWaitConditionCollector) collect(
	expression ast.Expr, index int, bindings map[types.Object]ast.Expr, remainingDepth int, reach goCallReach,
) {
	expression = collector.boundExpression(expression, bindings)
	call, isCall := expression.(*ast.CallExpr)
	if !isCall {
		collector.addUnknown(expression, index)
		return
	}
	analyzer := collector.analyzer
	condition := WaitCondition{Index: ptr.Any(index), Span: analyzer.functions.reachedSpan(call, reach)}
	switch name := analyzer.callName(call); name {
	case "Combo":
		for argumentIndex, argument := range call.Args {
			collector.collect(argument, argumentIndex, bindings, remainingDepth, reach)
		}
		return
	case "Timer":
		condition.Kind = "timer"
		condition.Expression = "duration"
		if len(call.Args) > 0 {
			condition.Expression = analyzer.expressionString(call.Args[0])
		}
		condition.Label = humanizeGoDuration(condition.Expression) + " timer"
	case "SubFlow":
		condition.Kind = "subflow"
		goTypeName := "SubFlow"
		metadata := make(map[string]any)
		if len(call.Args) > 0 {
			argument := collector.boundExpression(call.Args[0], bindings)
			goTypeName = valueOr(analyzer.expressionTypeName(argument), goTypeName)
			condition.Label = valueOr(analyzer.subFlowTypeName(argument), goTypeName)
			metadata = collector.subFlowDefinition(argument)
		}
		position := analyzer.fileSet.Position(call.Pos())
		condition.SubFlowID = fmt.Sprintf("subflow:%s:%d:%s:%d:%d", collector.waitID, index, goTypeName, position.Line, position.Column)
		analyzer.graph.AddNode(Node{
			ID: condition.SubFlowID, Kind: "subflow", Name: valueOr(condition.Label, goTypeName),
			External: true, Span: condition.Span, Metadata: metadata,
		})
		collector.addEdge("subflow", collector.waitID, condition.SubFlowID, "start", condition.Span, index, reach)
	default:
		selector, isSelector := unwrapCallFun(call.Fun).(*ast.SelectorExpr)
		if isSelector && isGoChannelCondition(name) {
			resourceID := analyzer.resourceForExpression(collector.boundExpression(selector.X, bindings))
			if resourceID != "" {
				condition.Kind = "channel"
				condition.ResourceID = resourceID
				condition.Label, condition.Expression = analyzer.goChannelConditionLabel(resourceID, name, call.Args)
				collector.addEdge("wait_condition", resourceID, collector.waitID, condition.Label, condition.Span, index, reach)
			}
		}
		if condition.Kind == "" {
			collector.collectHelper(call, index, bindings, remainingDepth, reach)
			return
		}
	}
	collector.conditions = append(collector.conditions, condition)
}

func (collector *goWaitConditionCollector) collectHelper(
	call *ast.CallExpr, index int, bindings map[types.Object]ast.Expr, remainingDepth int, reach goCallReach,
) {
	function := collector.analyzer.functions.calledFunction(call)
	declaration := collector.analyzer.functions.declarations[function]
	if remainingDepth <= 0 || declaration == nil {
		collector.addUnknown(call, index)
		return
	}
	returned := singleGoConditionReturn(declaration.Body)
	if returned == nil {
		collector.addUnknown(call, index)
		return
	}
	argumentBindings := make(map[types.Object]ast.Expr)
	arguments := collector.analyzer.functions.callArguments(call)
	parameterIndex := 0
	for _, field := range declaration.Type.Params.List {
		for _, parameter := range field.Names {
			if parameterIndex < len(arguments) {
				argumentBindings[collector.analyzer.typeInfo.Defs[parameter]] = collector.boundExpression(arguments[parameterIndex], bindings)
			}
			parameterIndex++
		}
	}
	if reach.entryCall == nil {
		reach.entryCall = call
	}
	reach.helper = function
	collector.collect(returned, index, argumentBindings, remainingDepth-1, reach)
}

func (collector *goWaitConditionCollector) addEdge(
	kind string, from string, to string, label string, span *Span, index int, reach goCallReach,
) {
	metadata := map[string]any{"conditionIndex": index}
	if reach.helper != nil {
		metadata["via"] = "helper"
		metadata["function"] = goHelperName(reach.helper)
	}
	collector.analyzer.graph.AddEdge(Edge{
		Kind: kind, From: from, To: to, Label: label, Span: span, Metadata: metadata,
	})
}

func (collector *goWaitConditionCollector) addUnknown(expression ast.Expr, index int) {
	collector.conditions = append(collector.conditions, WaitCondition{
		Kind: "unknown", Index: ptr.Any(index), Label: collector.analyzer.expressionString(expression),
		Expression: collector.analyzer.expressionString(expression), Span: collector.analyzer.span(expression),
	})
}

func (collector *goWaitConditionCollector) subFlowDefinition(argument ast.Expr) map[string]any {
	analyzer := collector.analyzer
	flowType := analyzer.subFlowTypeName(argument)
	child := registeredNamedType(analyzer.typeInfo.Types[argument].Type)
	if child == nil || flowType == "" {
		return nil
	}
	selection := types.NewMethodSet(types.NewPointer(child)).Lookup(nil, "GetSteps")
	if selection == nil {
		return nil
	}
	method, isMethod := selection.Obj().(*types.Func)
	if !isMethod {
		return nil
	}
	declaration, _, isFound := analyzer.typeNames.identityMethodDeclaration(method)
	if !isFound {
		return nil
	}
	declaringPackage := analyzer.typeNames.packagesByTypes[method.Origin().Pkg()]
	filename := declaringPackage.Fset.Position(declaration.Pos()).Filename
	relativePath, err := filepath.Rel(filepath.Dir(analyzer.sourcePath), filename)
	if err != nil {
		return nil
	}
	return map[string]any{"flowType": flowType, "sourcePath": filepath.ToSlash(relativePath)}
}

func (collector *goWaitConditionCollector) boundExpression(expression ast.Expr, bindings map[types.Object]ast.Expr) ast.Expr {
	for remaining := goMaximumHelperDepth; remaining > 0; remaining-- {
		expression = ast.Unparen(expression)
		identifier, isIdentifier := expression.(*ast.Ident)
		if !isIdentifier {
			break
		}
		bound := bindings[collector.analyzer.typeInfo.ObjectOf(identifier)]
		if bound == nil || bound == expression {
			break
		}
		expression = bound
	}
	return expression
}

func singleGoConditionReturn(body *ast.BlockStmt) ast.Expr {
	collector := &goConditionReturnCollector{}
	ast.Inspect(body, collector.visit)
	if collector.returnCount != 1 {
		return nil
	}
	return collector.returnExpression
}

func (collector *goConditionReturnCollector) visit(node ast.Node) bool {
	if _, isLiteral := node.(*ast.FuncLit); isLiteral {
		return false
	}
	if statement, isReturn := node.(*ast.ReturnStmt); isReturn {
		collector.returnCount++
		if len(statement.Results) == 1 {
			collector.returnExpression = statement.Results[0]
		}
		return false
	}
	return true
}
