// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

// Longer construct text is truncated in diagnostics.
const goMaximumConstructLength = 80

const goMaximumOptionExpressions = 10000

// goOptionsEvaluator reads the statically declared fields of Dex option values in the analyzed package.
type goOptionsEvaluator struct {
	analyzer            *goAnalyzer
	functions           *goFunctionWalker
	packageInitializers map[types.Object]goScopedExpression
	mutatedPackageVars  map[types.Object]bool
}

// goScopedExpression is an expression with the function whose locals and parameters it may use.
type goScopedExpression struct {
	expression  ast.Expr
	scope       *goEvaluationScope
	resultIndex int
}

type goEvaluationScope struct {
	function  *ast.FuncDecl
	arguments map[types.Object]goScopedExpression
	depth     int
	parent    *goEvaluationScope
}

// goStructValue is the may-be union of every field contribution an options value can receive.
type goStructValue struct {
	fields     map[string][]goScopedExpression
	unresolved []goUnresolvedConstruct
}

// goListValue is the may-be union of the elements a slice value can hold.
type goListValue struct {
	elements   []goScopedExpression
	unresolved []goUnresolvedConstruct
}

type goUnresolvedConstruct struct {
	construct string
	position  token.Position
}

// goStateLoadSet is the may-load union of one load option field.
type goStateLoadSet struct {
	loads      []goStateLoad
	unresolved []goUnresolvedConstruct
}

type goStateLoad struct {
	resourceID  string
	instanceKey goInstanceKey
}

// goEvaluation expands options iteratively with a shared expression budget.
type goEvaluation struct {
	evaluator            *goOptionsEvaluator
	remainingExpressions int
}

type goEvaluationKey struct {
	expression  ast.Expr
	object      types.Object
	scope       *goEvaluationScope
	resultIndex int
}

type goEvaluationQueue struct {
	evaluation *goEvaluation
	pending    []goScopedExpression
	seen       map[goEvaluationKey]bool
	unresolved []goUnresolvedConstruct
	isLimited  bool
}

type goLocalAssignments struct {
	values     []goScopedExpression
	fields     map[string][]goScopedExpression
	isEscaping bool
}

type goLocalAssignmentCollector struct {
	evaluator   *goOptionsEvaluator
	object      types.Object
	scope       *goEvaluationScope
	assignments *goLocalAssignments
}

type goReturnCollector struct {
	scope       *goEvaluationScope
	resultIndex int
	values      []goScopedExpression
	isComplete  bool
}

func newGoOptionsEvaluator(analyzer *goAnalyzer, functions *goFunctionWalker) *goOptionsEvaluator {
	evaluator := &goOptionsEvaluator{
		analyzer:            analyzer,
		functions:           functions,
		packageInitializers: make(map[types.Object]goScopedExpression),
		mutatedPackageVars:  make(map[types.Object]bool),
	}
	packageScope := &goEvaluationScope{}
	for _, file := range analyzer.packageFiles {
		for _, declaration := range file.Decls {
			evaluator.indexPackageInitializers(declaration, packageScope)
		}
	}
	for _, file := range analyzer.packageFiles {
		ast.Inspect(file, evaluator.markPackageVarMutation)
	}
	return evaluator
}

func (evaluator *goOptionsEvaluator) indexPackageInitializers(declaration ast.Decl, packageScope *goEvaluationScope) {
	general, isGeneral := declaration.(*ast.GenDecl)
	if !isGeneral || general.Tok != token.VAR {
		return
	}
	for _, specification := range general.Specs {
		valueSpec, isValue := specification.(*ast.ValueSpec)
		if !isValue || len(valueSpec.Values) != len(valueSpec.Names) {
			continue
		}
		for index, name := range valueSpec.Names {
			if object := evaluator.analyzer.typeInfo.Defs[name]; object != nil {
				evaluator.packageInitializers[object] = goScopedExpression{expression: valueSpec.Values[index], scope: packageScope}
			}
		}
	}
}

// Any later assignment to a package var makes its initializer an incomplete description.
func (evaluator *goOptionsEvaluator) markPackageVarMutation(node ast.Node) bool {
	switch statement := node.(type) {
	case *ast.AssignStmt:
		if statement.Tok == token.DEFINE {
			return true
		}
		for _, left := range statement.Lhs {
			evaluator.markMutatedRoot(left)
		}
	case *ast.IncDecStmt:
		evaluator.markMutatedRoot(statement.X)
	}
	return true
}

func (evaluator *goOptionsEvaluator) markMutatedRoot(expression ast.Expr) {
	root := goRootIdentifier(expression)
	if root == nil {
		return
	}
	if object := evaluator.analyzer.typeInfo.Uses[root]; object != nil {
		if _, isPackageVar := evaluator.packageInitializers[object]; isPackageVar {
			evaluator.mutatedPackageVars[object] = true
		}
	}
}

func (evaluator *goOptionsEvaluator) evaluateStruct(value goScopedExpression) goStructValue {
	return evaluator.newEvaluation().structValue(value)
}

func (evaluator *goOptionsEvaluator) evaluateList(value goScopedExpression) goListValue {
	return evaluator.newEvaluation().listValue(value)
}

// evaluateLoads returns the loads of one field, with the value's unresolved parts.
func (evaluator *goOptionsEvaluator) evaluateLoads(value goStructValue, fieldName string, isInstanceLoad bool) goStateLoadSet {
	evaluation := evaluator.newEvaluation()
	loads := goStateLoadSet{unresolved: append([]goUnresolvedConstruct(nil), value.unresolved...)}
	for _, contribution := range value.fields[fieldName] {
		list := evaluation.listValue(contribution)
		loads.unresolved = append(loads.unresolved, list.unresolved...)
		for _, element := range list.elements {
			loads.merge(evaluation.loadElement(element, isInstanceLoad))
		}
	}
	return loads
}

func (evaluator *goOptionsEvaluator) resolveResource(value goScopedExpression) string {
	return evaluator.newEvaluation().resource(value)
}

// definingValue returns the one value a variable holds, or false when it has none or several.
func (evaluator *goOptionsEvaluator) definingValue(value goScopedExpression) (goScopedExpression, bool) {
	return evaluator.newEvaluation().definingValue(value)
}

// stepOptionsValue evaluates the GetStepOptions method that a registered Step value uses.
func (evaluator *goOptionsEvaluator) stepOptionsValue(stepValueType types.Type, position token.Pos) goStructValue {
	unresolved := goStructValue{
		fields:     make(map[string][]goScopedExpression),
		unresolved: []goUnresolvedConstruct{evaluator.unresolvedAt("GetStepOptions", position)},
	}
	if stepValueType == nil {
		return unresolved
	}
	selection := types.NewMethodSet(stepValueType).Lookup(nil, "GetStepOptions")
	if selection == nil {
		return unresolved
	}
	method, isFunction := selection.Obj().(*types.Func)
	if !isFunction {
		return unresolved
	}
	method = method.Origin()
	if method.Pkg() != nil && method.Pkg().Path() == goSDKPackage {
		return goStructValue{fields: make(map[string][]goScopedExpression)}
	}
	declaration := evaluator.functions.declarations[method]
	if declaration == nil {
		return unresolved
	}
	return evaluator.newEvaluation().functionResultStruct(declaration, &goEvaluationScope{function: declaration}, 0)
}

func (evaluator *goOptionsEvaluator) newEvaluation() *goEvaluation {
	return &goEvaluation{evaluator: evaluator, remainingExpressions: goMaximumOptionExpressions}
}

func (evaluator *goOptionsEvaluator) unresolved(value goScopedExpression) goUnresolvedConstruct {
	construct := evaluator.analyzer.expressionString(value.expression)
	if len(construct) > goMaximumConstructLength {
		construct = construct[:goMaximumConstructLength] + "…"
	}
	return goUnresolvedConstruct{construct: construct, position: evaluator.analyzer.fileSet.Position(value.expression.Pos())}
}

func (evaluator *goOptionsEvaluator) unresolvedAt(construct string, position token.Pos) goUnresolvedConstruct {
	return goUnresolvedConstruct{construct: construct, position: evaluator.analyzer.fileSet.Position(position)}
}

func (evaluator *goOptionsEvaluator) localAssignments(object types.Object, scope *goEvaluationScope) *goLocalAssignments {
	collector := &goLocalAssignmentCollector{
		evaluator:   evaluator,
		object:      object,
		scope:       scope,
		assignments: &goLocalAssignments{fields: make(map[string][]goScopedExpression)},
	}
	if scope.function != nil && scope.function.Body != nil {
		ast.Inspect(scope.function.Body, collector.visit)
	}
	return collector.assignments
}

func (evaluation *goEvaluation) structValue(value goScopedExpression) goStructValue {
	result := goStructValue{fields: make(map[string][]goScopedExpression)}
	queue := newGoEvaluationQueue(evaluation, value)
	for currentValue, hasValue := queue.next(); hasValue; currentValue, hasValue = queue.next() {
		switch current := ast.Unparen(currentValue.expression).(type) {
		case *ast.Ident:
			if !evaluation.isNil(current) {
				for _, definition := range evaluation.variableValues(current, currentValue, &result.unresolved, &result.fields) {
					queue.add(definition)
				}
			}
			continue
		case *ast.UnaryExpr:
			if current.Op == token.AND {
				queue.add(currentValue.with(current.X))
				continue
			}
		case *ast.StarExpr:
			queue.add(currentValue.with(current.X))
			continue
		case *ast.CompositeLit:
			for _, element := range current.Elts {
				fieldName, fieldValue, isKeyed := goKeyedField(element)
				if !isKeyed {
					result.unresolved = append(result.unresolved, evaluation.evaluator.unresolved(currentValue.with(element)))
					continue
				}
				result.fields[fieldName] = append(result.fields[fieldName], currentValue.with(fieldValue))
			}
			continue
		case *ast.CallExpr:
			if evaluation.isBuiltin(current, "new") && len(current.Args) == 1 {
				if pointer, isPointer := evaluation.evaluator.analyzer.typeInfo.TypeOf(current).Underlying().(*types.Pointer); isPointer {
					if _, isStruct := pointer.Elem().Underlying().(*types.Struct); isStruct {
						continue
					}
				}
			}
			declaration, calleeScope := evaluation.callee(current, currentValue)
			if declaration != nil {
				returned, isComplete := goReturnedValues(declaration, calleeScope, currentValue.resultIndex)
				if !isComplete {
					result.unresolved = append(result.unresolved, evaluation.evaluator.unresolvedAt(declaration.Name.Name, declaration.Pos()))
				}
				for _, returnedValue := range returned {
					queue.add(returnedValue)
				}
				continue
			}
		}
		result.unresolved = append(result.unresolved, evaluation.evaluator.unresolved(currentValue))
	}
	result.unresolved = append(result.unresolved, queue.unresolved...)
	return result
}

func (evaluation *goEvaluation) functionResultStruct(declaration *ast.FuncDecl, scope *goEvaluationScope, resultIndex int) goStructValue {
	result := goStructValue{fields: make(map[string][]goScopedExpression)}
	returned, isComplete := goReturnedValues(declaration, scope, resultIndex)
	if !isComplete {
		result.unresolved = append(result.unresolved, evaluation.evaluator.unresolvedAt(declaration.Name.Name, declaration.Pos()))
	}
	for _, returnedValue := range returned {
		result.merge(evaluation.structValue(returnedValue))
	}
	return result
}

func (evaluation *goEvaluation) listValue(value goScopedExpression) goListValue {
	result := goListValue{}
	queue := newGoEvaluationQueue(evaluation, value)
	for currentValue, hasValue := queue.next(); hasValue; currentValue, hasValue = queue.next() {
		switch current := ast.Unparen(currentValue.expression).(type) {
		case *ast.Ident:
			if evaluation.isNil(current) {
				continue
			}
			var assignedFields map[string][]goScopedExpression
			for _, definition := range evaluation.variableValues(current, currentValue, &result.unresolved, &assignedFields) {
				queue.add(definition)
			}
			if len(assignedFields) > 0 {
				result.unresolved = append(result.unresolved, evaluation.evaluator.unresolved(currentValue))
			}
			continue
		case *ast.SelectorExpr:
			structure := evaluation.structValue(currentValue.with(current.X))
			result.unresolved = append(result.unresolved, structure.unresolved...)
			for _, contribution := range structure.fields[current.Sel.Name] {
				queue.add(contribution)
			}
			continue
		case *ast.CompositeLit:
			for _, element := range current.Elts {
				if _, isKeyValue := element.(*ast.KeyValueExpr); isKeyValue {
					result.unresolved = append(result.unresolved, evaluation.evaluator.unresolved(currentValue.with(element)))
					continue
				}
				result.elements = append(result.elements, currentValue.with(element))
			}
			continue
		case *ast.CallExpr:
			if evaluation.isBuiltin(current, "make") && len(current.Args) >= 2 {
				length := evaluation.evaluator.analyzer.typeInfo.Types[current.Args[1]].Value
				if _, isSlice := evaluation.evaluator.analyzer.typeInfo.TypeOf(current).Underlying().(*types.Slice); isSlice && length != nil && constant.Sign(length) == 0 {
					continue
				}
			}
			if evaluation.isBuiltin(current, "append") && len(current.Args) > 0 {
				queue.add(currentValue.with(current.Args[0]))
				for index, argument := range current.Args[1:] {
					if current.Ellipsis.IsValid() && index == len(current.Args)-2 {
						queue.add(currentValue.with(argument))
						continue
					}
					result.elements = append(result.elements, currentValue.with(argument))
				}
				continue
			}
			declaration, calleeScope := evaluation.callee(current, currentValue)
			if declaration != nil {
				returned, isComplete := goReturnedValues(declaration, calleeScope, currentValue.resultIndex)
				if !isComplete {
					result.unresolved = append(result.unresolved, evaluation.evaluator.unresolvedAt(declaration.Name.Name, declaration.Pos()))
				}
				for _, returnedValue := range returned {
					queue.add(returnedValue)
				}
				continue
			}
		}
		result.unresolved = append(result.unresolved, evaluation.evaluator.unresolved(currentValue))
	}
	result.unresolved = append(result.unresolved, queue.unresolved...)
	return result
}

// variableValues lists what an identifier can hold: a bound argument, a package initializer, or local assignments.
func (evaluation *goEvaluation) variableValues(
	identifier *ast.Ident,
	value goScopedExpression,
	unresolved *[]goUnresolvedConstruct,
	assignedFields *map[string][]goScopedExpression,
) []goScopedExpression {
	object := evaluation.evaluator.analyzer.typeInfo.Uses[identifier]
	if object == nil {
		*unresolved = append(*unresolved, evaluation.evaluator.unresolved(value))
		return nil
	}
	argument, isArgument := value.scope.arguments[object]
	if initializer, isPackageVar := evaluation.evaluator.packageInitializers[object]; isPackageVar {
		if evaluation.evaluator.mutatedPackageVars[object] {
			*unresolved = append(*unresolved, evaluation.evaluator.unresolved(value))
			return nil
		}
		return []goScopedExpression{initializer}
	}
	if !evaluation.isLocalVariable(object, value.scope) || (!isArgument && goIsSignatureVariable(value.scope.function, object)) {
		*unresolved = append(*unresolved, evaluation.evaluator.unresolved(value))
		return nil
	}
	assignments := evaluation.evaluator.localAssignments(object, value.scope)
	if assignments.isEscaping {
		*unresolved = append(*unresolved, evaluation.evaluator.unresolved(value))
		return nil
	}
	if len(assignments.fields) > 0 {
		if *assignedFields == nil {
			*assignedFields = make(map[string][]goScopedExpression)
		}
		for fieldName, contributions := range assignments.fields {
			(*assignedFields)[fieldName] = append((*assignedFields)[fieldName], contributions...)
		}
	}
	if isArgument {
		return append([]goScopedExpression{argument}, assignments.values...)
	}
	return assignments.values
}

func (evaluation *goEvaluation) loadElement(value goScopedExpression, isInstanceLoad bool) goStateLoadSet {
	unresolved := goStateLoadSet{unresolved: []goUnresolvedConstruct{evaluation.evaluator.unresolved(value)}}
	if !isInstanceLoad {
		resourceID := evaluation.resource(value)
		if resourceID == "" {
			return unresolved
		}
		return goStateLoadSet{loads: []goStateLoad{{resourceID: resourceID}}}
	}
	call, isCall := ast.Unparen(value.expression).(*ast.CallExpr)
	if !isCall || len(call.Args) != 1 {
		return unresolved
	}
	selector, isSelector := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !isSelector || (selector.Sel.Name != "Load" && selector.Sel.Name != "LoadMessages") {
		return unresolved
	}
	resourceID := evaluation.resource(value.with(selector.X))
	if resourceID == "" {
		return unresolved
	}
	key, isConstant := evaluation.evaluator.analyzer.staticString(call.Args[0])
	return goStateLoadSet{loads: []goStateLoad{{resourceID: resourceID, instanceKey: goInstanceKey{value: key, isConstant: isConstant}}}}
}

func (evaluation *goEvaluation) resource(value goScopedExpression) string {
	queue := newGoEvaluationQueue(evaluation, value)
	for currentValue, hasValue := queue.next(); hasValue; currentValue, hasValue = queue.next() {
		switch current := ast.Unparen(currentValue.expression).(type) {
		case *ast.UnaryExpr:
			queue.add(currentValue.with(current.X))
		case *ast.StarExpr:
			queue.add(currentValue.with(current.X))
		case *ast.Ident:
			object := evaluation.evaluator.analyzer.typeInfo.Uses[current]
			if resourceID := evaluation.evaluator.analyzer.resources[object]; object != nil && resourceID != "" {
				return resourceID
			}
			if definition, isDefined := evaluation.definingValue(currentValue); isDefined {
				queue.add(definition)
			}
		}
	}
	return ""
}

func (evaluation *goEvaluation) definingValue(value goScopedExpression) (goScopedExpression, bool) {
	identifier, isIdentifier := ast.Unparen(value.expression).(*ast.Ident)
	if !isIdentifier {
		return goScopedExpression{}, false
	}
	var unresolved []goUnresolvedConstruct
	var assignedFields map[string][]goScopedExpression
	definitions := evaluation.variableValues(identifier, value, &unresolved, &assignedFields)
	if len(definitions) != 1 || len(unresolved) > 0 || len(assignedFields) > 0 {
		return goScopedExpression{}, false
	}
	return definitions[0], true
}

// callee binds a same-package function's parameters to the call's arguments in the caller's scope.
func (evaluation *goEvaluation) callee(call *ast.CallExpr, value goScopedExpression) (*ast.FuncDecl, *goEvaluationScope) {
	if value.scope.depth >= goMaximumHelperDepth {
		return nil, nil
	}
	functions := evaluation.evaluator.functions
	function := functions.calledFunction(call)
	declaration := functions.declarations[function]
	if declaration == nil {
		return nil, nil
	}
	for caller := value.scope; caller != nil; caller = caller.parent {
		if caller.function == declaration {
			return nil, nil
		}
	}
	scope := &goEvaluationScope{
		parent:    value.scope,
		function:  declaration,
		arguments: make(map[types.Object]goScopedExpression),
		depth:     value.scope.depth + 1,
	}
	arguments := functions.callArguments(call)
	for index, name := range goParameterNames(declaration) {
		if name == nil || index >= len(arguments) {
			continue
		}
		if object := evaluation.evaluator.analyzer.typeInfo.Defs[name]; object != nil {
			scope.arguments[object] = value.with(arguments[index])
		}
	}
	return declaration, scope
}

func (evaluation *goEvaluation) isNil(identifier *ast.Ident) bool {
	object := evaluation.evaluator.analyzer.typeInfo.Uses[identifier]
	if object == nil {
		return identifier.Name == "nil"
	}
	_, isNil := object.(*types.Nil)
	return isNil
}

func (evaluation *goEvaluation) isBuiltin(call *ast.CallExpr, name string) bool {
	identifier, isIdentifier := ast.Unparen(call.Fun).(*ast.Ident)
	if !isIdentifier {
		return false
	}
	builtin, isBuiltin := evaluation.evaluator.analyzer.typeInfo.Uses[identifier].(*types.Builtin)
	return isBuiltin && builtin.Name() == name
}

func (evaluation *goEvaluation) isLocalVariable(object types.Object, scope *goEvaluationScope) bool {
	variable, isVariable := object.(*types.Var)
	if !isVariable || variable.Pkg() == nil || scope.function == nil {
		return false
	}
	return variable.Parent() != variable.Pkg().Scope()
}

func newGoEvaluationQueue(evaluation *goEvaluation, value goScopedExpression) *goEvaluationQueue {
	queue := &goEvaluationQueue{evaluation: evaluation, seen: make(map[goEvaluationKey]bool)}
	queue.add(value)
	return queue
}

func (queue *goEvaluationQueue) next() (goScopedExpression, bool) {
	if len(queue.pending) == 0 {
		return goScopedExpression{}, false
	}
	index := len(queue.pending) - 1
	value := queue.pending[index]
	queue.pending = queue.pending[:index]
	return value, true
}

func (queue *goEvaluationQueue) add(value goScopedExpression) {
	key := goEvaluationKey{expression: ast.Unparen(value.expression), scope: value.scope, resultIndex: value.resultIndex}
	if identifier, isIdentifier := key.expression.(*ast.Ident); isIdentifier {
		if object := queue.evaluation.evaluator.analyzer.typeInfo.Uses[identifier]; object != nil {
			key.object = object
			key.expression = nil
		}
	}
	if queue.seen[key] {
		return
	}
	queue.seen[key] = true
	if queue.evaluation.remainingExpressions == 0 {
		if !queue.isLimited {
			queue.isLimited = true
			queue.unresolved = append(queue.unresolved, queue.evaluation.evaluator.unresolvedAt("option expansion exceeds 10000 expressions", value.expression.Pos()))
		}
		return
	}
	queue.evaluation.remainingExpressions--
	queue.pending = append(queue.pending, value)
}

func (collector *goLocalAssignmentCollector) visit(node ast.Node) bool {
	switch current := node.(type) {
	case *ast.AssignStmt:
		collector.collectAssignment(current)
	case *ast.ValueSpec:
		collector.collectDeclaration(current)
	case *ast.CallExpr:
		collector.collectEscape(current)
	case *ast.RangeStmt:
		if collector.isObject(current.Key) || collector.isObject(current.Value) {
			collector.assignments.isEscaping = true
		}
	case *ast.FuncLit:
		if position := collector.object.Pos(); position >= current.Type.Pos() && position < current.Type.End() {
			collector.assignments.isEscaping = true
		}
	}
	return true
}

func (collector *goLocalAssignmentCollector) collectAssignment(assignment *ast.AssignStmt) {
	for index, left := range assignment.Lhs {
		if collector.isObject(left) {
			collector.collectValue(assignment.Rhs, index, len(assignment.Lhs))
			continue
		}
		selector, isSelector := left.(*ast.SelectorExpr)
		if isSelector && collector.isObject(selector.X) && len(assignment.Lhs) == len(assignment.Rhs) {
			collector.assignments.fields[selector.Sel.Name] = append(
				collector.assignments.fields[selector.Sel.Name],
				goScopedExpression{expression: assignment.Rhs[index], scope: collector.scope},
			)
			continue
		}
		if root := goRootIdentifier(left); root != nil && collector.isObject(root) {
			collector.assignments.isEscaping = true
		}
	}
}

func (collector *goLocalAssignmentCollector) collectDeclaration(specification *ast.ValueSpec) {
	for index, name := range specification.Names {
		if collector.evaluator.analyzer.typeInfo.Defs[name] == collector.object {
			collector.collectValue(specification.Values, index, len(specification.Names))
		}
	}
}

// A single multi-value right side assigns one result of a call.
func (collector *goLocalAssignmentCollector) collectValue(values []ast.Expr, index int, targetCount int) {
	switch {
	case len(values) == 0:
	case len(values) == targetCount:
		collector.assignments.values = append(collector.assignments.values, goScopedExpression{expression: values[index], scope: collector.scope})
	case len(values) == 1:
		collector.assignments.values = append(collector.assignments.values, goScopedExpression{expression: values[0], scope: collector.scope, resultIndex: index})
	default:
		collector.assignments.isEscaping = true
	}
}

// A value passed by reference to code outside the Dex SDK may gain fields this analysis cannot see.
func (collector *goLocalAssignmentCollector) collectEscape(call *ast.CallExpr) {
	if function := collector.evaluator.functions.calledFunction(call); function != nil && function.Pkg() != nil && function.Pkg().Path() == goSDKPackage {
		return
	}
	for _, argument := range call.Args {
		if unary, isUnary := ast.Unparen(argument).(*ast.UnaryExpr); isUnary && unary.Op == token.AND && collector.isObject(unary.X) {
			collector.assignments.isEscaping = true
			continue
		}
		if collector.isObject(argument) {
			if _, isPointer := collector.object.Type().Underlying().(*types.Pointer); isPointer {
				collector.assignments.isEscaping = true
			}
		}
	}
}

func (collector *goLocalAssignmentCollector) isObject(expression ast.Expr) bool {
	identifier, isIdentifier := ast.Unparen(expression).(*ast.Ident)
	if !isIdentifier {
		return false
	}
	return collector.evaluator.analyzer.typeInfo.ObjectOf(identifier) == collector.object
}

func (collector *goReturnCollector) visit(node ast.Node) bool {
	switch current := node.(type) {
	case *ast.FuncLit:
		return false
	case *ast.ReturnStmt:
		switch {
		case len(current.Results) > collector.resultIndex && (len(current.Results) > 1 || collector.resultIndex == 0):
			collector.values = append(collector.values, goScopedExpression{expression: current.Results[collector.resultIndex], scope: collector.scope})
		case len(current.Results) == 1:
			collector.values = append(collector.values, goScopedExpression{
				expression: current.Results[0], scope: collector.scope, resultIndex: collector.resultIndex,
			})
		default:
			collector.isComplete = false
		}
		return false
	}
	return true
}

func (value goScopedExpression) with(expression ast.Expr) goScopedExpression {
	return goScopedExpression{expression: expression, scope: value.scope}
}

func (value *goStructValue) merge(other goStructValue) {
	for fieldName, contributions := range other.fields {
		value.fields[fieldName] = append(value.fields[fieldName], contributions...)
	}
	value.unresolved = append(value.unresolved, other.unresolved...)
}

func (loads *goStateLoadSet) merge(other goStateLoadSet) {
	loads.loads = append(loads.loads, other.loads...)
	loads.unresolved = append(loads.unresolved, other.unresolved...)
}

func (loads goStateLoadSet) isUnresolved() bool {
	return len(loads.unresolved) > 0
}

func (loads goStateLoadSet) hasResource(resourceID string) bool {
	return len(loads.loadsOf(resourceID)) > 0
}

func (loads goStateLoadSet) loadsOf(resourceID string) []goStateLoad {
	matched := make([]goStateLoad, 0)
	for _, load := range loads.loads {
		if load.resourceID == resourceID {
			matched = append(matched, load)
		}
	}
	return matched
}

// goReturnedValues lists every returned value at resultIndex; a bare return makes the list incomplete.
func goReturnedValues(declaration *ast.FuncDecl, scope *goEvaluationScope, resultIndex int) ([]goScopedExpression, bool) {
	collector := &goReturnCollector{scope: scope, resultIndex: resultIndex, isComplete: true}
	if declaration.Body != nil {
		ast.Inspect(declaration.Body, collector.visit)
	}
	return collector.values, collector.isComplete
}

func goKeyedField(element ast.Expr) (string, ast.Expr, bool) {
	keyValue, isKeyValue := element.(*ast.KeyValueExpr)
	if !isKeyValue {
		return "", nil, false
	}
	key, isIdentifier := keyValue.Key.(*ast.Ident)
	if !isIdentifier {
		return "", nil, false
	}
	return key.Name, keyValue.Value, true
}

// Receivers, parameters, and named results are defined by the caller, not by assignments in the body.
func goIsSignatureVariable(declaration *ast.FuncDecl, object types.Object) bool {
	position := object.Pos()
	return position >= declaration.Type.Pos() && position < declaration.Type.End()
}

func goRootIdentifier(expression ast.Expr) *ast.Ident {
	for {
		switch current := expression.(type) {
		case *ast.Ident:
			return current
		case *ast.SelectorExpr:
			expression = current.X
		case *ast.IndexExpr:
			expression = current.X
		case *ast.StarExpr:
			expression = current.X
		case *ast.ParenExpr:
			expression = current.X
		default:
			return nil
		}
	}
}

func isGoSDKNamedType(value types.Type, name string) bool {
	named := registeredNamedType(value)
	return named != nil && named.Obj().Pkg().Path() == goSDKPackage && named.Obj().Name() == name
}
