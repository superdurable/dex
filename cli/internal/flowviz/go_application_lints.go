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
	"strings"
)

// goApplicationLinter reports the opt-in application lints of --lint app.
type goApplicationLinter struct {
	analyzer  *goAnalyzer
	functions *goFunctionWalker
	evaluator *goOptionsEvaluator
	sources   *goStateLoadSources
	reported  map[string]bool
}

// goBranchReadSearch looks for uses of a Connector result value and for reads of its Branch or Failure.
type goBranchReadSearch struct {
	linter         *goApplicationLinter
	tainted        map[types.Object]bool
	remainingDepth int
	isUsed         bool
	isBranchRead   bool
	hasChanged     bool
}

type goStartFlowSite struct {
	call     *ast.CallExpr
	reach    goCallReach
	function *ast.FuncDecl
}

type goFlowFileStartFlowCollector struct {
	linter   *goApplicationLinter
	function *ast.FuncDecl
	sites    []goStartFlowSite
}

func newGoApplicationLinter(analyzer *goAnalyzer, evaluator *goOptionsEvaluator, sources *goStateLoadSources) *goApplicationLinter {
	return &goApplicationLinter{
		analyzer:  analyzer,
		functions: evaluator.functions,
		evaluator: evaluator,
		sources:   sources,
		reported:  make(map[string]bool),
	}
}

func (linter *goApplicationLinter) report() {
	linter.reportMergedConnectorBranches()
	linter.reportActionsWithoutStateRecheck()
	linter.reportStartFlowsWithoutRequestID()
	linter.reportCrossFlowReadsWithoutMissingFlowHandling()
}

func (linter *goApplicationLinter) reportMergedConnectorBranches() {
	for _, connectorStepType := range linter.analyzer.registeredSteps {
		factory, isFactory := linter.analyzer.connectorFactories[connectorStepType]
		if !isFactory {
			continue
		}
		targets := make([]string, 0)
		branchesByTarget := make(map[string][]string)
		for _, branch := range factory.branches {
			targetID := linter.branchTargetNodeID(branch)
			if targetID == "" {
				continue
			}
			if _, isKnownTarget := branchesByTarget[targetID]; !isKnownTarget {
				targets = append(targets, targetID)
			}
			branchesByTarget[targetID] = append(branchesByTarget[targetID], branch.id)
		}
		for _, targetID := range targets {
			targetStepType := strings.TrimPrefix(targetID, "step:")
			branchIDs := branchesByTarget[targetID]
			if len(branchIDs) < 2 || !goHasConnectorFailureBranch(branchIDs) || !linter.usesResultWithoutBranch(targetStepType) {
				continue
			}
			target := linter.analyzer.node(targetID)
			linter.addDiagnostic("connector_branches_merged_unread", fmt.Sprintf(
				"%s receives branches %s of %s but never reads result.Branch; failure branches would be handled as success. Switch on result.Branch or route failures to their own Step.",
				target.Name, strings.Join(branchIDs, ", "), connectorStepType,
			), linter.stepMethodSpan(targetStepType, target))
		}
	}
}

func (linter *goApplicationLinter) branchTargetNodeID(branch goConnectorBranch) string {
	if branch.optionalUnwired || branch.target == "" {
		return ""
	}
	targetID := linter.analyzer.steps[branch.target]
	if branch.isStepRefTarget {
		targetID = linter.analyzer.registeredStepNodeIDs[branch.target]
	}
	if _, isFactory := linter.analyzer.connectorFactories[strings.TrimPrefix(targetID, "step:")]; isFactory {
		return ""
	}
	return targetID
}

// A Step that never uses its input cannot mistake a failure branch for a success.
func (linter *goApplicationLinter) usesResultWithoutBranch(stepType string) bool {
	isUsed := false
	for _, methodName := range []string{"Execute", "WaitFor"} {
		method := linter.analyzer.methods[stepType][methodName]
		if method == nil {
			continue
		}
		parameters := goParameterNames(method)
		if len(parameters) < 2 || parameters[1] == nil {
			continue
		}
		input := linter.analyzer.typeInfo.Defs[parameters[1]]
		if input == nil {
			continue
		}
		search := &goBranchReadSearch{linter: linter, tainted: map[types.Object]bool{input: true}, remainingDepth: goMaximumHelperDepth}
		search.searchBody(method.Body)
		if search.isBranchRead {
			return false
		}
		isUsed = isUsed || search.isUsed
	}
	return isUsed
}

func (linter *goApplicationLinter) reportActionsWithoutStateRecheck() {
	for _, handler := range linter.analyzer.handlerSummaries {
		if handler.phase != "rpc" {
			continue
		}
		rpcName := strings.TrimPrefix(handler.ownerID, "rpc:")
		conditionID, lockedIDs := linter.actionStateResources(rpcName)
		if conditionID == "" || goReadsAnyAttribute(handler.summary, append(lockedIDs, conditionID)) {
			continue
		}
		condition := linter.analyzer.node(conditionID).Name
		linter.addDiagnostic("action_state_not_rechecked", fmt.Sprintf(
			"Action %s is enabled when %q matches, but its handler never reads %q; the Action condition does not authorize the write. Read it under the lock, reject a stale or repeated decision, then change it.",
			rpcName, condition, condition,
		), linter.analyzer.span(handler.method.Name))
	}
}

// actionStateResources returns the Action condition Attribute and the Attributes the RPC locks.
func (linter *goApplicationLinter) actionStateResources(rpcName string) (string, []string) {
	conditionID := ""
	lockedIDs := make([]string, 0)
	for _, registration := range linter.sources.rpcOptions[rpcName] {
		options := linter.evaluator.evaluateStruct(registration)
		for _, action := range options.fields["Action"] {
			if resourceID := linter.actionConditionResource(action); resourceID != "" {
				conditionID = resourceID
			}
		}
		for _, locks := range options.fields["LockAttributes"] {
			lockedIDs = append(lockedIDs, linter.lockedResources(locks)...)
		}
	}
	return conditionID, lockedIDs
}

func (linter *goApplicationLinter) actionConditionResource(action goScopedExpression) string {
	defineAction := linter.definingCall(action)
	if defineAction == nil || linter.analyzer.callName(defineAction) != "DefineAction" || len(defineAction.Args) < 2 {
		return ""
	}
	condition := linter.definingCall(action.with(defineAction.Args[1]))
	if condition == nil || linter.analyzer.callName(condition) != "WhenAttributeMatches" || len(condition.Args) == 0 {
		return ""
	}
	return linter.evaluator.resolveResource(action.with(condition.Args[0]))
}

func (linter *goApplicationLinter) lockedResources(locks goScopedExpression) []string {
	resourceIDs := make([]string, 0)
	for _, element := range linter.evaluator.evaluateList(locks).elements {
		lock := linter.definingCall(element)
		if lock == nil || len(lock.Args) == 0 {
			continue
		}
		switch linter.analyzer.callName(lock) {
		case "LockAttribute", "LockAttributeMap":
			if resourceID := linter.evaluator.resolveResource(element.with(lock.Args[0])); resourceID != "" {
				resourceIDs = append(resourceIDs, resourceID)
			}
		}
	}
	return resourceIDs
}

// definingCall follows variables to the call that initializes them.
func (linter *goApplicationLinter) definingCall(value goScopedExpression) *ast.CallExpr {
	for depth := 0; depth < goMaximumHelperDepth; depth++ {
		if call, isCall := ast.Unparen(value.expression).(*ast.CallExpr); isCall {
			return call
		}
		definition, isDefined := linter.evaluator.definingValue(value)
		if !isDefined {
			return nil
		}
		value = definition
	}
	return nil
}

func (linter *goApplicationLinter) reportStartFlowsWithoutRequestID() {
	reportedCalls := make(map[*ast.CallExpr]bool)
	for _, site := range linter.startFlowSites() {
		if reportedCalls[site.call] || !linter.isRequestIDMissing(site) {
			continue
		}
		reportedCalls[site.call] = true
		linter.addDiagnostic("start_flow_request_id_missing", fmt.Sprintf(
			"StartFlow of %s%s has no RequestID; a retried start cannot be recognized. Derive it from the business identity and set AlreadyStarted IgnoreError.",
			linter.startedFlowName(site.call), goParenthesizedLocation(linter.functions.reachedLocation(site.call, site.reach)),
		), linter.functions.reachedSpan(site.call, site.reach))
	}
}

// startFlowSites lists StartFlow calls in the Flow file and in helpers its handlers reach.
func (linter *goApplicationLinter) startFlowSites() []goStartFlowSite {
	collector := &goFlowFileStartFlowCollector{linter: linter}
	for _, declaration := range linter.analyzer.file.Decls {
		if function, isFunction := declaration.(*ast.FuncDecl); isFunction && function.Body != nil {
			collector.function = function
			ast.Inspect(function.Body, collector.visit)
		}
	}
	for _, handler := range linter.analyzer.handlerSummaries {
		for _, reached := range handler.summary.startFlowCalls {
			if reached.reach.helper == nil || linter.functions.isInFlowFile(reached.call) {
				continue
			}
			function := linter.functions.declarations[reached.reach.helper]
			collector.sites = append(collector.sites, goStartFlowSite{call: reached.call, reach: reached.reach, function: function})
		}
	}
	return collector.sites
}

func (linter *goApplicationLinter) isRequestIDMissing(site goStartFlowSite) bool {
	if len(site.call.Args) < 5 || site.function == nil {
		return false
	}
	options := linter.evaluator.evaluateStruct(goScopedExpression{
		expression: site.call.Args[4],
		scope:      &goEvaluationScope{function: site.function},
	})
	if len(options.unresolved) > 0 {
		return false
	}
	for _, requestID := range options.fields["RequestID"] {
		if identifier, isIdentifier := ast.Unparen(requestID.expression).(*ast.Ident); !isIdentifier || identifier.Name != "nil" {
			return false
		}
	}
	return true
}

func (linter *goApplicationLinter) startedFlowName(call *ast.CallExpr) string {
	if len(call.Args) < 2 {
		return "a Flow"
	}
	if name := linter.analyzer.expressionTypeName(call.Args[1]); name != "" {
		return name
	}
	return linter.analyzer.expressionString(call.Args[1])
}

func (linter *goApplicationLinter) reportCrossFlowReadsWithoutMissingFlowHandling() {
	for _, handler := range linter.analyzer.handlerSummaries {
		if handler.phase != "execute" && handler.phase != "wait_for" {
			continue
		}
		for _, reached := range handler.summary.invokeRPCCalls {
			if reached.isMissingFlowHandled {
				continue
			}
			linter.addDiagnostic("cross_flow_read_not_found_unhandled", fmt.Sprintf(
				"%s reads %s%s but does not handle a missing Flow; the Step retries until its budget ends. Treat it as the empty or missing case.",
				goHandlerLabel(linter.analyzer, handler), linter.invokedRPCName(reached.call),
				goParenthesizedLocation(linter.functions.reachedLocation(reached.call, reached.reach)),
			), linter.functions.reachedSpan(reached.call, reached.reach))
		}
	}
}

func (linter *goApplicationLinter) invokedRPCName(call *ast.CallExpr) string {
	if len(call.Args) < 3 {
		return "an RPC"
	}
	selector, isSelector := ast.Unparen(call.Args[2]).(*ast.SelectorExpr)
	if isSelector {
		if selection := linter.analyzer.typeInfo.Selections[selector]; selection != nil {
			if method, isFunction := selection.Obj().(*types.Func); isFunction && method.Signature().Recv() != nil {
				if receiver := registeredNamedType(method.Signature().Recv().Type()); receiver != nil {
					return receiver.Obj().Name() + "." + method.Name()
				}
			}
		}
	}
	return linter.analyzer.expressionString(call.Args[2])
}

func (linter *goApplicationLinter) stepMethodSpan(stepType string, step Node) *Span {
	if method := linter.analyzer.methods[stepType]["Execute"]; method != nil {
		return linter.analyzer.span(method.Name)
	}
	return step.Span
}

func (linter *goApplicationLinter) addDiagnostic(code string, message string, span *Span) {
	if linter.reported[code+"|"+message] {
		return
	}
	linter.reported[code+"|"+message] = true
	linter.analyzer.graph.AddDiagnostic("error", code, message, span)
}

func (collector *goFlowFileStartFlowCollector) visit(node ast.Node) bool {
	call, isCall := node.(*ast.CallExpr)
	if isCall && collector.linter.functions.clientMethodName(call) == "StartFlow" {
		collector.sites = append(collector.sites, goStartFlowSite{call: call, function: collector.function})
	}
	return true
}

func (search *goBranchReadSearch) searchBody(body *ast.BlockStmt) {
	if body == nil {
		return
	}
	for search.hasChanged = true; search.hasChanged; {
		search.hasChanged = false
		ast.Inspect(body, search.propagate)
	}
	ast.Inspect(body, search.visit)
}

func (search *goBranchReadSearch) propagate(node ast.Node) bool {
	switch current := node.(type) {
	case *ast.AssignStmt:
		if len(current.Lhs) == len(current.Rhs) {
			for index, left := range current.Lhs {
				search.taintFrom(left, current.Rhs[index])
			}
		}
	case *ast.ValueSpec:
		if len(current.Names) == len(current.Values) {
			for index, name := range current.Names {
				search.taintFrom(name, current.Values[index])
			}
		}
	}
	return true
}

func (search *goBranchReadSearch) taintFrom(left ast.Expr, value ast.Expr) {
	identifier, isIdentifier := left.(*ast.Ident)
	if !isIdentifier || !search.isTainted(value) {
		return
	}
	object := search.linter.analyzer.typeInfo.ObjectOf(identifier)
	if object != nil && !search.tainted[object] {
		search.tainted[object] = true
		search.hasChanged = true
	}
}

func (search *goBranchReadSearch) visit(node ast.Node) bool {
	if search.isBranchRead {
		return false
	}
	switch current := node.(type) {
	case *ast.Ident:
		if object := search.linter.analyzer.typeInfo.Uses[current]; object != nil && search.tainted[object] {
			search.isUsed = true
		}
	case *ast.SelectorExpr:
		if (current.Sel.Name == "Branch" || current.Sel.Name == "Failure") && search.isTainted(current.X) {
			search.isBranchRead = true
		}
	case *ast.CallExpr:
		search.followHelper(current)
	}
	return !search.isBranchRead
}

func (search *goBranchReadSearch) followHelper(call *ast.CallExpr) {
	if search.remainingDepth == 0 {
		return
	}
	functions := search.linter.functions
	declaration := functions.declarations[functions.calledFunction(call)]
	if declaration == nil {
		return
	}
	tainted := make(map[types.Object]bool)
	arguments := functions.callArguments(call)
	for index, name := range goParameterNames(declaration) {
		if name == nil || index >= len(arguments) || !search.isTainted(arguments[index]) {
			continue
		}
		if object := search.linter.analyzer.typeInfo.Defs[name]; object != nil {
			tainted[object] = true
		}
	}
	if len(tainted) == 0 {
		return
	}
	helper := &goBranchReadSearch{linter: search.linter, tainted: tainted, remainingDepth: search.remainingDepth - 1}
	helper.searchBody(declaration.Body)
	search.isBranchRead = helper.isBranchRead
}

func (search *goBranchReadSearch) isTainted(expression ast.Expr) bool {
	switch current := ast.Unparen(expression).(type) {
	case *ast.UnaryExpr:
		return search.isTainted(current.X)
	case *ast.StarExpr:
		return search.isTainted(current.X)
	case *ast.Ident:
		object := search.linter.analyzer.typeInfo.ObjectOf(current)
		return object != nil && search.tainted[object]
	}
	return false
}

// The Connector SDK reserves these branches for results that always carry a Failure.
func goHasConnectorFailureBranch(branchIDs []string) bool {
	return containsString(branchIDs, "defect") || containsString(branchIDs, "uncertain")
}

func goParenthesizedLocation(location string) string {
	if location == "" {
		return ""
	}
	return " (" + strings.TrimPrefix(location, " ") + ")"
}

func goReadsAnyAttribute(summary *goFunctionSummary, resourceIDs []string) bool {
	for _, access := range summary.accesses {
		if access.methodName == "Get" && containsString(resourceIDs, access.resourceID) {
			return true
		}
	}
	return false
}
