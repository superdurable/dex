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
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"

	"github.com/superdurable/dex/cli/internal/pointervalue"
)

type goConditionReturnCollector struct {
	returnExpression ast.Expr
	returnCount      int
}

type goWaitConditionCollector struct {
	analyzer   *goAnalyzer
	waitID     string
	method     *ast.FuncDecl
	conditions []WaitCondition
}

type goConditionVariableUses struct {
	analyzer   *goAnalyzer
	object     types.Object
	read       *ast.Ident
	isSlice    bool
	isEscaping bool
}

func (collector *goWaitConditionCollector) collectSpread(waitCall *ast.CallExpr, argument ast.Expr, index int) {
	analyzer := collector.analyzer
	switch analyzer.callName(waitCall) {
	case "AllOf", "AnyOf":
		if run := analyzer.spreadSubFlowRun(waitCall, argument, collector.method.Body); run != nil {
			collector.collect(run, index, nil, goMaximumHelperDepth, goCallReach{})
			return
		}
	}
	collector.addDynamicSpread(argument, index, collector.method)
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
	condition := WaitCondition{Index: pointervalue.Pointer(index), Span: analyzer.functions.reachedSpan(call, reach)}
	switch name := analyzer.callName(call); name {
	case "Combo":
		for argumentIndex, argument := range call.Args {
			if call.Ellipsis.IsValid() && argumentIndex == len(call.Args)-1 {
				method := collector.method
				if reach.helper != nil {
					method = analyzer.functions.declarations[reach.helper]
				}
				collector.addDynamicSpread(argument, argumentIndex, method)
				continue
			}
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
		Kind: "unknown", Index: pointervalue.Pointer(index), Label: collector.analyzer.expressionString(expression),
		Expression: collector.analyzer.expressionString(expression), Span: collector.analyzer.span(expression),
	})
}

func (collector *goWaitConditionCollector) addDynamicSpread(expression ast.Expr, index int, method *ast.FuncDecl) {
	collector.addUnknown(expression, index)
	analyzer := collector.analyzer
	severity := "error"
	label := analyzer.expressionString(expression)
	message := fmt.Sprintf("Spread wait condition %s must be a local slice that one range loop over a slice or array fills by appending dex.SubFlow calls for a statically known child Flow type, returned at once in dex.AllOf or dex.AnyOf", label)
	value := goScopedExpression{expression: expression, scope: &goEvaluationScope{function: method}}
	if analyzer.isKnownNonSubFlowConditionValue(value, goMaximumHelperDepth) {
		severity = "warning"
		message = fmt.Sprintf("Spread wait condition %s is drawn as an unknown condition", label)
	}
	analyzer.graph.AddDiagnostic(severity, "dynamic_wait_condition", message,
		analyzer.span(expression))
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

func (analyzer *goAnalyzer) spreadSubFlowRun(waitCall *ast.CallExpr, argument ast.Expr, body *ast.BlockStmt) *ast.CallExpr {
	conditionSlice, isIdentifier := ast.Unparen(argument).(*ast.Ident)
	if !isIdentifier {
		return nil
	}
	conditionSliceObject := analyzer.typeInfo.Uses[conditionSlice]
	if conditionSliceObject == nil {
		return nil
	}
	if _, isSlice := conditionSliceObject.Type().Underlying().(*types.Slice); !isSlice {
		return nil
	}
	initialization, loop := goSpreadWaitPreparation(waitCall, body)
	if loop == nil || !analyzer.initializesEmptyConditionSlice(initialization, conditionSliceObject) {
		return nil
	}
	return analyzer.appendedSubFlowRun(loop, conditionSliceObject)
}

func (analyzer *goAnalyzer) appendedSubFlowRun(loop *ast.RangeStmt, conditionSliceObject types.Object) *ast.CallExpr {
	rangeType := analyzer.typeInfo.TypeOf(loop.X)
	if rangeType == nil {
		return nil
	}
	switch rangeType.Underlying().(type) {
	case *types.Slice, *types.Array:
	default:
		return nil
	}
	if len(loop.Body.List) != 1 {
		return nil
	}
	assignment, isAssignment := loop.Body.List[0].(*ast.AssignStmt)
	if !isAssignment || assignment.Tok != token.ASSIGN || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return nil
	}
	appendCall, isCall := ast.Unparen(assignment.Rhs[0]).(*ast.CallExpr)
	if !isCall || !analyzer.isWaitBuiltin(appendCall, "append") || len(appendCall.Args) != 2 || appendCall.Ellipsis.IsValid() {
		return nil
	}
	appendDestination, isIdentifier := ast.Unparen(assignment.Lhs[0]).(*ast.Ident)
	appendSource, isSourceIdentifier := ast.Unparen(appendCall.Args[0]).(*ast.Ident)
	if !isIdentifier || !isSourceIdentifier || analyzer.typeInfo.Uses[appendDestination] != conditionSliceObject || analyzer.typeInfo.Uses[appendSource] != conditionSliceObject {
		return nil
	}
	for node := range ast.Preorder(loop) {
		if identifier, ok := node.(*ast.Ident); ok && identifier != appendDestination && identifier != appendSource && analyzer.typeInfo.Uses[identifier] == conditionSliceObject {
			return nil
		}
	}
	run, isCall := ast.Unparen(appendCall.Args[1]).(*ast.CallExpr)
	if !isCall || len(run.Args) < 2 {
		return nil
	}
	function := analyzer.functions.calledFunction(run)
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != goSDKPackage || function.Name() != "SubFlow" {
		return nil
	}
	if analyzer.subFlowTypeName(ast.Unparen(run.Args[0])) == "" {
		return nil
	}
	return run
}

func goSpreadWaitPreparation(waitCall *ast.CallExpr, body *ast.BlockStmt) (ast.Stmt, *ast.RangeStmt) {
	for node := range ast.Preorder(body) {
		var statements []ast.Stmt
		switch container := node.(type) {
		case *ast.BlockStmt:
			statements = container.List
		case *ast.CaseClause:
			statements = container.Body
		}
		for index, statement := range statements {
			returned, isReturn := statement.(*ast.ReturnStmt)
			if index < 2 || !isReturn || len(returned.Results) == 0 || ast.Unparen(returned.Results[0]) != waitCall {
				continue
			}
			loop, _ := statements[index-1].(*ast.RangeStmt)
			return statements[index-2], loop
		}
	}
	return nil, nil
}

func (analyzer *goAnalyzer) initializesEmptyConditionSlice(statement ast.Stmt, conditionSlice types.Object) bool {
	var initializer ast.Expr
	switch declaration := statement.(type) {
	case *ast.AssignStmt:
		if declaration.Tok != token.DEFINE || len(declaration.Lhs) != 1 || len(declaration.Rhs) != 1 {
			return false
		}
		identifier, isIdentifier := declaration.Lhs[0].(*ast.Ident)
		if !isIdentifier || analyzer.typeInfo.Defs[identifier] != conditionSlice {
			return false
		}
		initializer = declaration.Rhs[0]
	case *ast.DeclStmt:
		declared, isDeclaration := declaration.Decl.(*ast.GenDecl)
		if !isDeclaration || declared.Tok != token.VAR || len(declared.Specs) != 1 {
			return false
		}
		value, isValue := declared.Specs[0].(*ast.ValueSpec)
		if !isValue || len(value.Names) != 1 || analyzer.typeInfo.Defs[value.Names[0]] != conditionSlice {
			return false
		}
		if len(value.Values) == 0 {
			return true
		}
		initializer = value.Values[0]
	default:
		return false
	}
	switch expression := ast.Unparen(initializer).(type) {
	case *ast.CompositeLit:
		return len(expression.Elts) == 0
	case *ast.CallExpr:
		if !analyzer.isWaitBuiltin(expression, "make") || len(expression.Args) < 2 {
			return false
		}
		length := analyzer.typeInfo.Types[expression.Args[1]].Value
		return length != nil && length.Kind() == constant.Int && constant.Sign(length) == 0
	default:
		return false
	}
}

func (analyzer *goAnalyzer) isWaitBuiltin(call *ast.CallExpr, name string) bool {
	identifier, isIdentifier := ast.Unparen(call.Fun).(*ast.Ident)
	return isIdentifier && analyzer.typeInfo.Uses[identifier] == types.Universe.Lookup(name)
}

func (analyzer *goAnalyzer) isKnownNonSubFlowConditionValue(value goScopedExpression, remainingDepth int) bool {
	if remainingDepth <= 0 {
		return false
	}
	switch expression := ast.Unparen(value.expression).(type) {
	case *ast.Ident:
		object := analyzer.typeInfo.Uses[expression]
		if _, isNil := object.(*types.Nil); isNil {
			return true
		}
		return analyzer.isKnownNonSubFlowConditionVariable(expression, object, value.scope, remainingDepth-1)
	case *ast.CompositeLit:
		for _, element := range expression.Elts {
			if keyed, isKeyed := element.(*ast.KeyValueExpr); isKeyed {
				element = keyed.Value
			}
			if !analyzer.isKnownNonSubFlowConditionValue(value.with(element), remainingDepth-1) {
				return false
			}
		}
		return true
	case *ast.CallExpr:
		return analyzer.isKnownNonSubFlowConditionCall(expression, value, remainingDepth-1)
	default:
		return false
	}
}

func (analyzer *goAnalyzer) isKnownNonSubFlowConditionCall(call *ast.CallExpr, value goScopedExpression, remainingDepth int) bool {
	switch {
	case analyzer.isWaitBuiltin(call, "make"):
		return true
	case analyzer.isWaitBuiltin(call, "append"):
		return analyzer.areKnownNonSubFlowConditionArguments(call.Args, value, remainingDepth)
	}
	function := analyzer.functions.calledFunction(call)
	if function == nil || function.Pkg() == nil {
		return false
	}
	if function.Pkg().Path() == goSDKPackage {
		name := function.Name()
		switch name {
		case "Timer", "GracefulCompletionRequested":
			return true
		case "Combo":
			return analyzer.areKnownNonSubFlowConditionArguments(call.Args, value, remainingDepth)
		default:
			return isGoChannelCondition(name)
		}
	}
	return analyzer.isKnownNonSubFlowConditionHelperResult(function, value.resultIndex, remainingDepth)
}

func (analyzer *goAnalyzer) isKnownNonSubFlowConditionHelperResult(function *types.Func, resultIndex int, remainingDepth int) bool {
	declaration := analyzer.functions.declarations[function]
	results := function.Signature().Results()
	if declaration == nil || resultIndex >= results.Len() {
		return false
	}
	resultType, isNamed := types.Unalias(results.At(resultIndex).Type()).(*types.Named)
	if !isNamed || resultType.Obj().Pkg() == nil || resultType.Obj().Pkg().Path() != goSDKPackage || resultType.Obj().Name() != "Condition" {
		return false
	}
	returned, isComplete := goReturnedValues(declaration, &goEvaluationScope{function: declaration}, resultIndex)
	if !isComplete || len(returned) == 0 {
		return false
	}
	for _, value := range returned {
		if !analyzer.isKnownNonSubFlowConditionValue(value, remainingDepth) {
			return false
		}
	}
	return true
}

func (analyzer *goAnalyzer) areKnownNonSubFlowConditionArguments(arguments []ast.Expr, value goScopedExpression, remainingDepth int) bool {
	for _, argument := range arguments {
		if !analyzer.isKnownNonSubFlowConditionValue(value.with(argument), remainingDepth) {
			return false
		}
	}
	return true
}

func (analyzer *goAnalyzer) isKnownNonSubFlowConditionVariable(read *ast.Ident, object types.Object, scope *goEvaluationScope, remainingDepth int) bool {
	method := scope.function
	if object == nil || method == nil || method.Body == nil || object.Pos() < method.Body.Pos() || object.Pos() >= method.Body.End() {
		return false
	}
	assignments := analyzer.localAssignments(object, scope)
	if !assignments.isDeclared || assignments.isEscaping || len(assignments.fields) != 0 {
		return false
	}
	_, isSlice := object.Type().Underlying().(*types.Slice)
	uses := &goConditionVariableUses{analyzer: analyzer, object: object, read: read, isSlice: isSlice}
	ast.PreorderStack(method.Body, nil, uses.visit)
	if uses.isEscaping {
		return false
	}
	for _, value := range assignments.values {
		if appendCall := analyzer.conditionSliceSelfAppend(value.expression, object); appendCall != nil {
			if !analyzer.areKnownNonSubFlowConditionArguments(appendCall.Args[1:], value, remainingDepth) {
				return false
			}
		} else if !analyzer.isKnownNonSubFlowConditionValue(value, remainingDepth) {
			return false
		}
	}
	return true
}

func (analyzer *goAnalyzer) conditionSliceSelfAppend(expression ast.Expr, object types.Object) *ast.CallExpr {
	call, isCall := ast.Unparen(expression).(*ast.CallExpr)
	if !isCall || !analyzer.isWaitBuiltin(call, "append") || len(call.Args) == 0 {
		return nil
	}
	base, isIdentifier := ast.Unparen(call.Args[0]).(*ast.Ident)
	if !isIdentifier || analyzer.typeInfo.Uses[base] != object {
		return nil
	}
	return call
}

func (uses *goConditionVariableUses) visit(node ast.Node, stack []ast.Node) bool {
	if uses.isEscaping {
		return false
	}
	identifier, isIdentifier := node.(*ast.Ident)
	if !isIdentifier || identifier == uses.read || uses.analyzer.typeInfo.Uses[identifier] != uses.object {
		return true
	}
	parent, ancestors := goConditionExpressionParent(stack)
	if unary, isUnary := parent.(*ast.UnaryExpr); isUnary && unary.Op == token.AND {
		uses.isEscaping = true
	} else if uses.isSlice && !uses.isKnownSliceUse(identifier, parent, ancestors) {
		uses.isEscaping = true
	}
	return true
}

func (uses *goConditionVariableUses) isKnownSliceUse(identifier *ast.Ident, parent ast.Node, ancestors []ast.Node) bool {
	switch parent := parent.(type) {
	case *ast.AssignStmt:
		for _, left := range parent.Lhs {
			if ast.Unparen(left) == identifier {
				return true
			}
		}
	case *ast.CallExpr:
		analyzer := uses.analyzer
		if analyzer.isWaitBuiltin(parent, "len") || analyzer.isWaitBuiltin(parent, "cap") {
			return true
		}
		if analyzer.conditionSliceSelfAppend(parent, uses.object) != nil && ast.Unparen(parent.Args[0]) == identifier {
			destination, _ := goConditionExpressionParent(ancestors)
			if assignment, isAssignment := destination.(*ast.AssignStmt); isAssignment && len(assignment.Lhs) == 1 && len(assignment.Rhs) == 1 {
				left, isIdentifier := ast.Unparen(assignment.Lhs[0]).(*ast.Ident)
				return isIdentifier && analyzer.typeInfo.ObjectOf(left) == uses.object
			}
			if waitCall, isCall := destination.(*ast.CallExpr); isCall {
				return analyzer.isGoWaitSpreadArgument(waitCall, parent)
			}
			return false
		}
		return analyzer.isGoWaitSpreadArgument(parent, identifier)
	}
	return false
}

func (analyzer *goAnalyzer) isGoWaitSpreadArgument(call *ast.CallExpr, argument ast.Expr) bool {
	if !call.Ellipsis.IsValid() || len(call.Args) == 0 || ast.Unparen(call.Args[len(call.Args)-1]) != argument {
		return false
	}
	function := analyzer.functions.calledFunction(call)
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != goSDKPackage {
		return analyzer.isWaitBuiltin(call, "append")
	}
	switch function.Name() {
	case "AllOf", "AnyOf", "AnyComboOf", "Combo":
		return true
	default:
		return false
	}
}

func goConditionExpressionParent(stack []ast.Node) (ast.Node, []ast.Node) {
	for index := len(stack) - 1; index >= 0; index-- {
		if _, isParenthesized := stack[index].(*ast.ParenExpr); !isParenthesized {
			return stack[index], stack[:index]
		}
	}
	return nil, nil
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
