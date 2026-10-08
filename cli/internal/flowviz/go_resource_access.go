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
	"sort"
	"strings"
)

// Deeper helper chains are not followed, which bounds analysis time.
const goMaximumHelperDepth = 6

// goFunctionWalker summarizes what a Flow handler reaches directly and through same-package helpers.
type goFunctionWalker struct {
	analyzer     *goAnalyzer
	declarations map[*types.Func]*ast.FuncDecl
	summaries    map[string]*goFunctionSummary
}

type goFunctionSummary struct {
	accesses []goResourceAccess
}

type goResourceAccess struct {
	resourceID string
	methodName string
	call       *ast.CallExpr
	reach      goCallReach
}

// goCallReach names the helper that contains a call and the walked-body call that leads to it.
type goCallReach struct {
	helper    *types.Func
	entryCall *ast.CallExpr
}

type goFunctionScope struct {
	function       *types.Func
	isHandler      bool
	resources      map[types.Object]string
	remainingDepth int
}

type goFunctionBodyCollector struct {
	walker  *goFunctionWalker
	scope   *goFunctionScope
	summary *goFunctionSummary
}

func newGoFunctionWalker(analyzer *goAnalyzer) *goFunctionWalker {
	walker := &goFunctionWalker{
		analyzer:     analyzer,
		declarations: make(map[*types.Func]*ast.FuncDecl),
		summaries:    make(map[string]*goFunctionSummary),
	}
	for _, file := range analyzer.packageFiles {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Body == nil {
				continue
			}
			if object, isObject := analyzer.typeInfo.Defs[function.Name].(*types.Func); isObject {
				walker.declarations[object] = function
			}
		}
	}
	return walker
}

func (walker *goFunctionWalker) summarizeHandler(method *ast.FuncDecl) *goFunctionSummary {
	scope := &goFunctionScope{
		isHandler:      true,
		resources:      make(map[types.Object]string),
		remainingDepth: goMaximumHelperDepth,
	}
	return walker.summarizeBody(method.Body, scope)
}

func (walker *goFunctionWalker) summarizeHelper(
	function *types.Func,
	declaration *ast.FuncDecl,
	argumentResources map[int]string,
	remainingDepth int,
) *goFunctionSummary {
	key := goHelperSummaryKey(function, argumentResources, remainingDepth)
	if summary, isSummarized := walker.summaries[key]; isSummarized {
		return summary
	}
	scope := &goFunctionScope{
		function:       function,
		resources:      walker.parameterResources(declaration, argumentResources),
		remainingDepth: remainingDepth,
	}
	summary := walker.summarizeBody(declaration.Body, scope)
	walker.summaries[key] = summary
	return summary
}

func (walker *goFunctionWalker) summarizeBody(body *ast.BlockStmt, scope *goFunctionScope) *goFunctionSummary {
	collector := &goFunctionBodyCollector{walker: walker, scope: scope, summary: &goFunctionSummary{}}
	if body == nil {
		return collector.summary
	}
	ast.Inspect(body, collector.bindLocalAlias)
	ast.Inspect(body, collector.visit)
	return collector.summary
}

// A single multi-value call, such as NewBufferedTextStream, aliases its first result.
func (collector *goFunctionBodyCollector) bindLocalAlias(node ast.Node) bool {
	switch statement := node.(type) {
	case *ast.AssignStmt:
		collector.bindAliases(statement.Lhs, statement.Rhs)
	case *ast.ValueSpec:
		names := make([]ast.Expr, 0, len(statement.Names))
		for _, name := range statement.Names {
			names = append(names, name)
		}
		collector.bindAliases(names, statement.Values)
	}
	return true
}

func (collector *goFunctionBodyCollector) bindAliases(targets []ast.Expr, values []ast.Expr) {
	if len(values) == 1 && len(targets) > 1 {
		collector.bindAlias(targets[0], values[0])
		return
	}
	if len(targets) != len(values) {
		return
	}
	for index, target := range targets {
		collector.bindAlias(target, values[index])
	}
}

func (collector *goFunctionBodyCollector) bindAlias(left ast.Expr, value ast.Expr) {
	identifier, isIdentifier := left.(*ast.Ident)
	if !isIdentifier {
		return
	}
	object := collector.walker.analyzer.typeInfo.ObjectOf(identifier)
	resourceID := collector.walker.aliasedResource(value, collector.scope)
	if object != nil && resourceID != "" {
		collector.scope.resources[object] = resourceID
	}
}

func (collector *goFunctionBodyCollector) visit(node ast.Node) bool {
	call, isCall := node.(*ast.CallExpr)
	if !isCall {
		return true
	}
	collector.collectResourceAccess(call)
	collector.followHelper(call)
	return true
}

func (collector *goFunctionBodyCollector) collectResourceAccess(call *ast.CallExpr) {
	selector, isSelector := unwrapCallFun(ast.Unparen(call.Fun)).(*ast.SelectorExpr)
	if !isSelector || goResourceEdgeKind(selector.Sel.Name, "execute") == "" {
		return
	}
	resourceID := collector.walker.resourceOf(selector.X, collector.scope)
	if resourceID == "" {
		return
	}
	collector.summary.accesses = append(collector.summary.accesses, goResourceAccess{
		resourceID: resourceID,
		methodName: selector.Sel.Name,
		call:       call,
		reach:      goCallReach{helper: collector.scope.function},
	})
}

func (collector *goFunctionBodyCollector) followHelper(call *ast.CallExpr) {
	if collector.scope.remainingDepth == 0 {
		return
	}
	function := collector.walker.calledFunction(call)
	declaration := collector.walker.declarations[function]
	if declaration == nil {
		return
	}
	argumentResources := collector.walker.argumentResources(collector.walker.callArguments(call), collector.scope)
	helper := collector.walker.summarizeHelper(function, declaration, argumentResources, collector.scope.remainingDepth-1)
	collector.mergeHelperSummary(call, helper)
}

// Summaries are shared through the memo, so merged facts are copies that take this call as entry.
func (collector *goFunctionBodyCollector) mergeHelperSummary(entryCall *ast.CallExpr, helper *goFunctionSummary) {
	for _, access := range helper.accesses {
		access.reach.entryCall = entryCall
		collector.summary.accesses = append(collector.summary.accesses, access)
	}
}

func (walker *goFunctionWalker) calledFunction(call *ast.CallExpr) *types.Func {
	var object types.Object
	switch function := unwrapCallFun(ast.Unparen(call.Fun)).(type) {
	case *ast.Ident:
		object = walker.analyzer.typeInfo.Uses[function]
	case *ast.SelectorExpr:
		if selection := walker.analyzer.typeInfo.Selections[function]; selection != nil {
			object = selection.Obj()
		} else {
			object = walker.analyzer.typeInfo.Uses[function.Sel]
		}
	}
	called, isFunction := object.(*types.Func)
	if !isFunction {
		return nil
	}
	return called.Origin()
}

// A method expression such as T.Method passes its receiver as the first argument.
func (walker *goFunctionWalker) callArguments(call *ast.CallExpr) []ast.Expr {
	selector, isSelector := unwrapCallFun(ast.Unparen(call.Fun)).(*ast.SelectorExpr)
	if !isSelector || len(call.Args) == 0 {
		return call.Args
	}
	if selection := walker.analyzer.typeInfo.Selections[selector]; selection != nil && selection.Kind() == types.MethodExpr {
		return call.Args[1:]
	}
	return call.Args
}

func (walker *goFunctionWalker) argumentResources(arguments []ast.Expr, scope *goFunctionScope) map[int]string {
	resources := make(map[int]string)
	for index, argument := range arguments {
		if resourceID := walker.typedResourceOf(argument, scope); resourceID != "" {
			resources[index] = resourceID
		}
	}
	return resources
}

func (walker *goFunctionWalker) parameterResources(declaration *ast.FuncDecl, argumentResources map[int]string) map[types.Object]string {
	resources := make(map[types.Object]string)
	for index, name := range goParameterNames(declaration) {
		resourceID := argumentResources[index]
		if name == nil || resourceID == "" {
			continue
		}
		if object := walker.analyzer.typeInfo.Defs[name]; object != nil {
			resources[object] = resourceID
		}
	}
	return resources
}

func (walker *goFunctionWalker) aliasedResource(value ast.Expr, scope *goFunctionScope) string {
	call, isCall := ast.Unparen(value).(*ast.CallExpr)
	if isCall && walker.analyzer.callName(call) == "NewBufferedTextStream" && len(call.Args) >= 2 {
		return walker.resourceOf(call.Args[1], scope)
	}
	return walker.typedResourceOf(value, scope)
}

// Direct handler accesses keep the name-based matches the analyzer has always accepted.
func (walker *goFunctionWalker) resourceOf(expression ast.Expr, scope *goFunctionScope) string {
	if resourceID := walker.typedResourceOf(expression, scope); resourceID != "" {
		return resourceID
	}
	if scope.isHandler {
		return walker.analyzer.resourceForExpression(expression)
	}
	return ""
}

func (walker *goFunctionWalker) typedResourceOf(expression ast.Expr, scope *goFunctionScope) string {
	switch current := expression.(type) {
	case *ast.ParenExpr:
		return walker.typedResourceOf(current.X, scope)
	case *ast.UnaryExpr:
		return walker.typedResourceOf(current.X, scope)
	case *ast.StarExpr:
		return walker.typedResourceOf(current.X, scope)
	case *ast.Ident:
		object := walker.analyzer.typeInfo.Uses[current]
		if object == nil {
			return ""
		}
		if resourceID := walker.analyzer.resources[object]; resourceID != "" {
			return resourceID
		}
		return scope.resources[object]
	}
	return ""
}

// Diagnostic spans stay in the Flow file, so a call in another file is located by its handler call.
func (walker *goFunctionWalker) reachedSpan(call *ast.CallExpr, reach goCallReach) *Span {
	if walker.isInFlowFile(call) || reach.entryCall == nil {
		return walker.analyzer.span(call)
	}
	return walker.analyzer.span(reach.entryCall)
}

func (walker *goFunctionWalker) isInFlowFile(node ast.Node) bool {
	return walker.analyzer.nodeFilename(node) == walker.analyzer.sourcePath
}

func goParameterNames(declaration *ast.FuncDecl) []*ast.Ident {
	names := make([]*ast.Ident, 0)
	if declaration.Type.Params == nil {
		return names
	}
	for _, field := range declaration.Type.Params.List {
		if len(field.Names) == 0 {
			names = append(names, nil)
			continue
		}
		names = append(names, field.Names...)
	}
	return names
}

func goHelperSummaryKey(function *types.Func, argumentResources map[int]string, remainingDepth int) string {
	indexes := make([]int, 0, len(argumentResources))
	for index := range argumentResources {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var key strings.Builder
	fmt.Fprintf(&key, "%p|%d", function, remainingDepth)
	for _, index := range indexes {
		fmt.Fprintf(&key, "|%d=%s", index, argumentResources[index])
	}
	return key.String()
}

func goHelperName(function *types.Func) string {
	receiver := function.Signature().Recv()
	if receiver == nil {
		return function.Name()
	}
	if named := registeredNamedType(receiver.Type()); named != nil {
		return named.Obj().Name() + "." + function.Name()
	}
	return function.Name()
}
