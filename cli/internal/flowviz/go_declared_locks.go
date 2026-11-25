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
	"sort"
	"strings"
)

// declaredLockMissingSeverity is the severity of declared_lock_missing for every analyzer.
// It is a warning because the Worker enforces declared requirements at run time.
const declaredLockMissingSeverity = "warning"

// goLockRequirement is the lock that a resource declaration requires for every write or publish.
type goLockRequirement struct {
	lockResourceID string
	// sameInstance means a write to an AttributeMap instance needs that instance's own lock.
	sameInstance bool
	// unresolved names the declaration construct dexcli cannot read; the requirement is then unknown.
	unresolved string
}

type goHeldLock struct {
	resourceID string
	instance   goInstanceKey
}

// goHandlerLocks is the may-hold union of the registration locks of one handler.
type goHandlerLocks struct {
	locks      []goHeldLock
	unresolved string
	// notLiteral names options that an alias may change; their locks are known but cannot prove one missing.
	notLiteral string
	// mayHideMissing names options whose held locks may never be registered, so a held lock proves nothing.
	mayHideMissing string
}

// goDeclaredLockChecker reports writes to declared resources without the registration lock they require.
type goDeclaredLockChecker struct {
	analyzer  *goAnalyzer
	evaluator *goOptionsEvaluator
	sources   *goStateLoadSources
	reported  map[string]bool
	rebound   map[types.Object]bool
}

func newGoDeclaredLockChecker(analyzer *goAnalyzer, evaluator *goOptionsEvaluator, sources *goStateLoadSources) *goDeclaredLockChecker {
	return &goDeclaredLockChecker{analyzer: analyzer, evaluator: evaluator, sources: sources, reported: make(map[string]bool)}
}

func (checker *goDeclaredLockChecker) report() {
	requirements, declarationIsReadable := checker.lockRequirements()
	for _, handler := range checker.analyzer.handlerSummaries {
		if handler.phase == "timeout" {
			continue
		}
		var locks *goHandlerLocks
		for _, access := range handler.summary.accesses {
			requirement, isDeclared := requirements[access.resourceID]
			if !isDeclared || !checker.isGuardedWrite(access) {
				continue
			}
			if locks == nil {
				resolved, isRegistered := checker.handlerLocks(handler)
				if !isRegistered {
					break
				}
				locks = &resolved
			}
			checker.checkWrite(handler, *locks, access, requirement)
		}
	}
	checker.reportUndeclaredRequirements(requirements, declarationIsReadable)
}

// lockRequirements reads the lock options of every resource defined in the Flow file.
// It returns the declared requirements, and the resources whose options dexcli read completely.
func (checker *goDeclaredLockChecker) lockRequirements() (map[string]goLockRequirement, map[string]bool) {
	requirements := make(map[string]goLockRequirement)
	readable := make(map[string]bool)
	for resourceID, definition := range checker.analyzer.resourceDefinitions {
		resource := checker.analyzer.node(resourceID)
		if resource.ID == "" {
			continue
		}
		requirement, isDeclared, isReadable := checker.declaredRequirement(resource, definition)
		if isDeclared {
			requirements[resourceID] = requirement
		}
		readable[resourceID] = isReadable
	}
	return requirements, readable
}

// declaredRequirement returns the declared requirement, whether one is declared, and whether every option is readable.
// An option dexcli cannot read may or may not declare a lock, so its resource is neither checked nor nudged.
func (checker *goDeclaredLockChecker) declaredRequirement(resource Node, definition *ast.CallExpr) (goLockRequirement, bool, bool) {
	if definition.Ellipsis.IsValid() {
		return goLockRequirement{}, false, false
	}
	if !goResourceConstructors[checker.analyzer.callName(definition)].acceptsLockOptions {
		return goLockRequirement{}, false, true
	}
	// The SDK applies options in order, so the last lock option wins; scan backward.
	isReadable := true
	options := definition.Args[min(1, len(definition.Args)):]
	for index := len(options) - 1; index >= 0; index-- {
		option := options[index]
		call, isCall := ast.Unparen(option).(*ast.CallExpr)
		function := ""
		if isCall {
			function = checker.sdkFunctionName(call)
		}
		switch function {
		case "":
			isReadable = false
		case "WritesRequireLock", "WritesRequireAttributeLock", "PublishesRequireAttributeLock":
			if !isReadable {
				// An unreadable later option may override this one.
				return goLockRequirement{unresolved: checker.analyzer.expressionString(options[len(options)-1])}, true, false
			}
		}
		switch function {
		case "WritesRequireLock":
			if resource.Kind != "attribute" {
				return goLockRequirement{unresolved: checker.analyzer.expressionString(option)}, true, true
			}
			return goLockRequirement{lockResourceID: resource.ID, sameInstance: resource.Resource != nil && resource.Resource.Map}, true, true
		case "WritesRequireAttributeLock", "PublishesRequireAttributeLock":
			if len(call.Args) != 1 {
				return goLockRequirement{unresolved: checker.analyzer.expressionString(option)}, true, true
			}
			lock := checker.analyzer.node(checker.strictResource(call.Args[0]))
			if lock.Kind != "attribute" || lock.Resource == nil || lock.Resource.Map {
				return goLockRequirement{unresolved: checker.analyzer.expressionString(call.Args[0])}, true, true
			}
			return goLockRequirement{lockResourceID: lock.ID}, true, true
		}
	}
	return goLockRequirement{}, false, isReadable
}

// sdkFunctionName returns the name of the SDK package function that the type checker resolves for a call, or "".
func (checker *goDeclaredLockChecker) sdkFunctionName(call *ast.CallExpr) string {
	var identifier *ast.Ident
	switch callee := unwrapCallFun(ast.Unparen(call.Fun)).(type) {
	case *ast.Ident:
		identifier = callee
	case *ast.SelectorExpr:
		identifier = callee.Sel
	default:
		return ""
	}
	function, isFunction := checker.analyzer.typeInfo.Uses[identifier].(*types.Func)
	if !isFunction || function.Pkg() == nil || function.Pkg().Path() != goSDKPackage || function.Signature().Recv() != nil {
		return ""
	}
	return function.Name()
}

func (checker *goDeclaredLockChecker) isGuardedWrite(access goResourceAccess) bool {
	resource := checker.analyzer.node(access.resourceID)
	switch resource.Kind {
	case "attribute":
		return access.methodName == "Set" || access.methodName == "Delete"
	case "channel":
		return access.methodName == "Publish"
	}
	return false
}

// handlerLocks returns the registration locks of a Step method or an RPC, and false for an RPC that no DefineRPC registers.
// Movement overrides and invocation locks do not count, so they are not read.
func (checker *goDeclaredLockChecker) handlerLocks(handler goHandlerSummary) (goHandlerLocks, bool) {
	switch handler.phase {
	case "execute", "wait_for":
		stepType := strings.TrimPrefix(handler.ownerID, "step:")
		stepValueType := checker.analyzer.stepValueTypes[stepType]
		registered := checker.evaluator.stepOptionsValue(stepValueType, handler.method.Pos())
		field := "ExecuteLockAttributes"
		if handler.phase == "wait_for" {
			field = "WaitForLockAttributes"
		}
		locks := checker.locksFromStruct(registered, field)
		isLiteral, hasUnreachableReturn := checker.stepOptionsAreLiteral(stepValueType)
		if !isLiteral {
			locks.notLiteral = "GetStepOptions does not return only literal options with literal lock lists"
		}
		if hasUnreachableReturn {
			locks.mayHideMissing = "GetStepOptions has a return that may not run"
		}
		return locks, true
	case "rpc":
		registrations, isRegistered := checker.sources.rpcOptions[strings.TrimPrefix(handler.ownerID, "rpc:")]
		if !isRegistered {
			return goHandlerLocks{}, false
		}
		locks := goHandlerLocks{}
		for _, registration := range registrations {
			registered := checker.locksFromStruct(checker.evaluator.evaluateStruct(registration), "LockAttributes")
			locks.locks = append(locks.locks, registered.locks...)
			if locks.unresolved == "" {
				locks.unresolved = registered.unresolved
			}
			if locks.notLiteral == "" && !checker.optionsAreLiteral(registration.expression) {
				locks.notLiteral = "the RPCOptions are not a literal with a literal lock list (" + checker.analyzer.expressionString(registration.expression) + ")"
			}
		}
		return locks, true
	}
	return goHandlerLocks{}, false
}

// optionsAreLiteral reports whether options are nil or a literal whose lock lists hold only direct SDK lock calls.
// Only such a value cannot be changed through an alias, so only it can prove that a lock is missing.
func (checker *goDeclaredLockChecker) optionsAreLiteral(expression ast.Expr) bool {
	switch current := ast.Unparen(expression).(type) {
	case *ast.Ident:
		return current.Name == "nil"
	case *ast.UnaryExpr:
		return current.Op == token.AND && checker.optionsAreLiteral(current.X)
	case *ast.CompositeLit:
		for _, element := range current.Elts {
			field, isKeyed := element.(*ast.KeyValueExpr)
			if !isKeyed {
				return false
			}
			key, isIdentifier := field.Key.(*ast.Ident)
			if !isIdentifier || !strings.HasSuffix(key.Name, "LockAttributes") {
				continue
			}
			switch value := ast.Unparen(field.Value).(type) {
			case *ast.CompositeLit:
				for _, lock := range value.Elts {
					if !checker.isDirectLockCall(lock) {
						return false
					}
				}
			case *ast.Ident:
				if value.Name != "nil" {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	return false
}

// isDirectLockCall reports whether expression calls LockAttribute or LockAttributeMap on a strictly resolved resource.
func (checker *goDeclaredLockChecker) isDirectLockCall(expression ast.Expr) bool {
	call, isCall := ast.Unparen(expression).(*ast.CallExpr)
	if !isCall || len(call.Args) == 0 {
		return false
	}
	switch checker.sdkFunctionName(call) {
	case "LockAttribute", "LockAttributeMap":
		return checker.strictResource(call.Args[0]) != ""
	}
	return false
}

// stepOptionsAreLiteral reports whether the Step's GetStepOptions is the SDK default or returns only literal options.
// It also reports whether a return may not run, since that return's locks join the union and can hide a missing lock.
func (checker *goDeclaredLockChecker) stepOptionsAreLiteral(stepValueType types.Type) (bool, bool) {
	shape := checker.stepOptionsShape(stepValueType)
	return shape.isLiteral, shape.hasUnreachableReturn
}

type goStepOptionsShape struct {
	isLiteral            bool
	hasUnreachableReturn bool
	// returnCount is 0 for the SDK default and -1 when GetStepOptions cannot be read.
	returnCount int
}

func (checker *goDeclaredLockChecker) stepOptionsShape(stepValueType types.Type) goStepOptionsShape {
	unreadable := goStepOptionsShape{returnCount: -1}
	if stepValueType == nil {
		return unreadable
	}
	selection := types.NewMethodSet(stepValueType).Lookup(nil, "GetStepOptions")
	if selection == nil {
		return unreadable
	}
	method, isFunction := selection.Obj().(*types.Func)
	if !isFunction {
		return unreadable
	}
	method = method.Origin()
	if method.Pkg() != nil && method.Pkg().Path() == goSDKPackage {
		return goStepOptionsShape{isLiteral: true}
	}
	declaration := checker.evaluator.functions.declarations[method]
	if declaration == nil || declaration.Body == nil {
		return unreadable
	}
	shape := goStepOptionsShape{isLiteral: true}
	ast.Inspect(declaration.Body, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			shape.returnCount++
			if len(current.Results) != 1 || !checker.optionsAreLiteral(current.Results[0]) {
				shape.isLiteral = false
			}
			if goMayNotRun(declaration.Body, current, checker.analyzer.typeInfo) {
				shape.hasUnreachableReturn = true
			}
		}
		return true
	})
	return shape
}

// unprovenWriteReason returns why dexcli cannot prove that the write runs on the declared resource, or "".
// Graph edges resolve receivers loosely; declared_lock_missing needs the strict facts below.
func (checker *goDeclaredLockChecker) unprovenWriteReason(handler goHandlerSummary, access goResourceAccess) string {
	selector, isSelector := unwrapCallFun(ast.Unparen(access.call.Fun)).(*ast.SelectorExpr)
	if !isSelector {
		return "the write is not a method call"
	}
	method, isMethod := checker.analyzer.typeInfo.Uses[selector.Sel].(*types.Func)
	if !isMethod || method.Pkg() == nil || method.Pkg().Path() != goSDKPackage {
		return "the write method is not resolved to the Dex SDK"
	}
	if checker.strictResource(selector.X) != access.resourceID {
		return "the receiver is not a package variable that defines the resource"
	}
	body := handler.method.Body
	if access.reach.helper != nil {
		declaration := checker.evaluator.functions.declarations[access.reach.helper]
		if declaration == nil {
			return "the helper that writes cannot be read"
		}
		if access.reach.entryCall != nil {
			if checker.calledFunction(access.reach.entryCall) != access.reach.helper.Origin() {
				return "the write is in a helper called through another helper"
			}
			if goMayNotRun(handler.method.Body, access.reach.entryCall, checker.analyzer.typeInfo) {
				return "the helper call may not run"
			}
		}
		body = declaration.Body
	}
	if goMayNotRun(body, access.call, checker.analyzer.typeInfo) {
		return "the write is in a function literal or a branch that may not run"
	}
	return ""
}

// strictResource returns the resource of a package variable reference, or "" for any other expression.
func (checker *goDeclaredLockChecker) strictResource(expression ast.Expr) string {
	var identifier *ast.Ident
	switch current := ast.Unparen(expression).(type) {
	case *ast.Ident:
		identifier = current
	case *ast.SelectorExpr:
		identifier = current.Sel
	default:
		return ""
	}
	variable, isVariable := checker.analyzer.typeInfo.Uses[identifier].(*types.Var)
	if !isVariable || variable.Parent() == nil || variable.Parent() != variable.Pkg().Scope() || checker.reboundVariables()[variable] {
		return ""
	}
	return checker.analyzer.resources[variable]
}

// calledFunction returns the package function or method that a call names, or nil.
func (checker *goDeclaredLockChecker) calledFunction(call *ast.CallExpr) *types.Func {
	var identifier *ast.Ident
	switch callee := unwrapCallFun(ast.Unparen(call.Fun)).(type) {
	case *ast.Ident:
		identifier = callee
	case *ast.SelectorExpr:
		identifier = callee.Sel
	default:
		return nil
	}
	function, isFunction := checker.analyzer.typeInfo.Uses[identifier].(*types.Func)
	if !isFunction {
		return nil
	}
	return function.Origin()
}

// reboundVariables returns the package variables that a function body assigns or takes the address of.
// Such a variable may no longer hold the resource its declaration defines.
func (checker *goDeclaredLockChecker) reboundVariables() map[types.Object]bool {
	if checker.rebound != nil {
		return checker.rebound
	}
	checker.rebound = make(map[types.Object]bool)
	markIdentifier := func(expression ast.Expr) {
		if identifier, isIdentifier := ast.Unparen(expression).(*ast.Ident); isIdentifier {
			if object := checker.analyzer.typeInfo.Uses[identifier]; object != nil {
				checker.rebound[object] = true
			}
		}
	}
	for _, declaration := range checker.evaluator.functions.declarations {
		if declaration.Body == nil {
			continue
		}
		ast.Inspect(declaration.Body, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.AssignStmt:
				for _, target := range current.Lhs {
					markIdentifier(target)
				}
			case *ast.UnaryExpr:
				if current.Op == token.AND {
					markIdentifier(current.X)
				}
			}
			return true
		})
	}
	return checker.rebound
}

// goMayNotRun reports whether target sits in a function literal or a constant-false branch inside body.
func goMayNotRun(body ast.Node, target ast.Node, typeInfo *types.Info) bool {
	if body == nil {
		return true
	}
	mayNotRun := false
	var visit func(node ast.Node, isGuarded bool) bool
	visit = func(node ast.Node, isGuarded bool) bool {
		if node == nil {
			return false
		}
		if node == target {
			mayNotRun = isGuarded
			return true
		}
		if node.Pos() > target.Pos() || node.End() < target.End() {
			return false
		}
		switch current := node.(type) {
		case *ast.FuncLit:
			return visit(current.Body, true)
		case *ast.IfStmt:
			value, isConstant := goConstantBool(current.Cond, typeInfo)
			return visit(current.Init, isGuarded) || visit(current.Cond, isGuarded) ||
				visit(current.Body, isGuarded || (isConstant && !value)) ||
				visit(current.Else, isGuarded || (isConstant && value))
		case *ast.ForStmt:
			value, isConstant := false, false
			if current.Cond != nil {
				value, isConstant = goConstantBool(current.Cond, typeInfo)
			}
			isSkipped := isGuarded || (isConstant && !value)
			return visit(current.Init, isGuarded) || visit(current.Cond, isGuarded) ||
				visit(current.Post, isSkipped) || visit(current.Body, isSkipped)
		}
		isFound := false
		ast.Inspect(node, func(child ast.Node) bool {
			if isFound || child == nil || child == node {
				return !isFound
			}
			isFound = visit(child, isGuarded)
			return false
		})
		return isFound
	}
	if !visit(body, false) {
		return true
	}
	return mayNotRun
}

func goConstantBool(expression ast.Expr, typeInfo *types.Info) (bool, bool) {
	typeAndValue, isTyped := typeInfo.Types[expression]
	if !isTyped || typeAndValue.Value == nil || typeAndValue.Value.Kind() != constant.Bool {
		return false, false
	}
	return constant.BoolVal(typeAndValue.Value), true
}

func (checker *goDeclaredLockChecker) locksFromStruct(value goStructValue, field string) goHandlerLocks {
	locks := goHandlerLocks{}
	if len(value.unresolved) > 0 {
		locks.unresolved = value.unresolved[0].construct
	}
	for _, lockList := range value.fields[field] {
		list := checker.evaluator.evaluateList(lockList)
		if len(list.unresolved) > 0 && locks.unresolved == "" {
			locks.unresolved = list.unresolved[0].construct
		}
		for _, element := range list.elements {
			lock, isResolved := checker.heldLock(element)
			if !isResolved {
				if locks.unresolved == "" {
					locks.unresolved = checker.evaluator.unresolved(element).construct
				}
				continue
			}
			locks.locks = append(locks.locks, lock)
		}
	}
	return locks
}

// heldLock resolves one AttributeLock value to its Attribute and, for an AttributeMap, its instance key.
func (checker *goDeclaredLockChecker) heldLock(value goScopedExpression) (goHeldLock, bool) {
	for range goMaximumHelperDepth {
		if call, isCall := ast.Unparen(value.expression).(*ast.CallExpr); isCall {
			function := checker.sdkFunctionName(call)
			if function == "" || len(call.Args) == 0 {
				return goHeldLock{}, false
			}
			resourceID := checker.evaluator.resolveResource(value.with(call.Args[0]))
			switch {
			case resourceID == "":
				return goHeldLock{}, false
			case function == "LockAttribute" && len(call.Args) == 1:
				return goHeldLock{resourceID: resourceID}, true
			case function == "LockAttributeMap" && len(call.Args) == 2:
				instance, isConstant := checker.analyzer.staticString(call.Args[1])
				return goHeldLock{resourceID: resourceID, instance: goInstanceKey{value: instance, isConstant: isConstant}}, true
			}
			return goHeldLock{}, false
		}
		definition, isDefined := checker.evaluator.definingValue(value)
		if !isDefined {
			return goHeldLock{}, false
		}
		value = definition
	}
	return goHeldLock{}, false
}

func (checker *goDeclaredLockChecker) checkWrite(handler goHandlerSummary, locks goHandlerLocks, access goResourceAccess, requirement goLockRequirement) {
	resource := checker.analyzer.node(access.resourceID)
	action := "writes"
	if resource.Kind == "channel" {
		action = "publishes to"
	}
	subject := fmt.Sprintf("%s %s %s %s", goHandlerLabel(checker.analyzer, handler), action, resource.Kind, resource.Name)
	if requirement.unresolved != "" {
		checker.addDiagnostic(handler, access, "warning", "declared_lock_unresolved", fmt.Sprintf(
			"%s, but dexcli cannot read the lock requirement of its declaration (%s)", subject, requirement.unresolved))
		return
	}
	lock := checker.analyzer.node(requirement.lockResourceID)
	required := goResourceVariableName(lock.ID)
	lockCall := fmt.Sprintf("dex.LockAttribute(%s)", required)
	isWriteKeyKnown := !requirement.sameInstance || access.instanceKey.isConstant
	if requirement.sameInstance {
		lockCall = fmt.Sprintf("dex.LockAttributeMap(%s, <written instance>)", required)
		if isWriteKeyKnown {
			lockCall = fmt.Sprintf("dex.LockAttributeMap(%s, %q)", required, access.instanceKey.value)
		}
	}
	reason := locks.unresolved
	if reason == "" {
		reason = locks.notLiteral
	}
	isUncertain := reason != ""
	isHeld := false
	for _, held := range locks.locks {
		if held.resourceID != requirement.lockResourceID {
			continue
		}
		if !requirement.sameInstance || (held.instance.isConstant && isWriteKeyKnown && held.instance.value == access.instanceKey.value) {
			isHeld = true
			break
		}
		if held.instance.isConstant && isWriteKeyKnown {
			continue
		}
		isUncertain = true
		if reason == "" && !isWriteKeyKnown {
			reason = "the written instance key is dynamic"
		}
	}
	if isHeld {
		if locks.mayHideMissing == "" {
			return
		}
		isUncertain = true
		reason = locks.mayHideMissing
	}
	if isUncertain {
		if reason == "" {
			reason = "an AttributeMap lock with a dynamic instance key"
		}
		checker.addDiagnostic(handler, access, "warning", "declared_lock_unresolved", fmt.Sprintf(
			"%s, which requires %s, but dexcli cannot prove that the registration locks include it (%s)", subject, lockCall, reason))
		return
	}
	if reason := checker.unprovenWriteReason(handler, access); reason != "" {
		checker.addDiagnostic(handler, access, "warning", "declared_lock_unresolved", fmt.Sprintf(
			"%s, which requires %s, but dexcli cannot prove the write (%s)", subject, lockCall, reason))
		return
	}
	checker.addDiagnostic(handler, access, declaredLockMissingSeverity, "declared_lock_missing", fmt.Sprintf(
		"%s without the lock its declaration requires; add %s to %s", subject, lockCall, goLockField(handler.phase)))
}

func goLockField(phase string) string {
	switch phase {
	case "execute":
		return "ExecuteLockAttributes in GetStepOptions"
	case "wait_for":
		return "WaitForLockAttributes in GetStepOptions"
	}
	return "RPCOptions.LockAttributes"
}

func (checker *goDeclaredLockChecker) addDiagnostic(handler goHandlerSummary, access goResourceAccess, severity string, code string, message string) {
	key := handler.ownerID + "|" + handler.phase + "|" + code + "|" + message
	if checker.reported[key] {
		return
	}
	checker.reported[key] = true
	checker.analyzer.graph.AddDiagnostic(severity, code, message, checker.analyzer.functions.reachedSpan(access.call, access.reach))
}

// reportUndeclaredRequirements nudges an Attribute that may lose an update to declare its lock.
// It considers only writes that can overlap: an RPC can run beside any handler, while Steps alone may run in sequence.
// Writers that all hold one common lock, by exact identity, through each read and write are serialized, so they trigger nothing.
// Blind writes alone trigger nothing either: each commits whole, so a lock would not change the outcome.
// When the analyzer is unsure about locks or reads, it keeps the warning.
func (checker *goDeclaredLockChecker) reportUndeclaredRequirements(requirements map[string]goLockRequirement, declarationIsReadable map[string]bool) {
	stepReads := make(map[string]map[string]bool)
	handlerReads := make([]map[string]bool, len(checker.analyzer.handlerSummaries))
	handlerWrites := make([]map[string]bool, len(checker.analyzer.handlerSummaries))
	for index, handler := range checker.analyzer.handlerSummaries {
		handlerReads[index] = make(map[string]bool)
		handlerWrites[index] = make(map[string]bool)
		for _, access := range handler.summary.accesses {
			if checker.analyzer.node(access.resourceID).Kind != "attribute" {
				continue
			}
			switch goResourceEdgeKind(access.methodName, handler.phase) {
			case "resource_read":
				handlerReads[index][access.resourceID] = true
				if handler.phase == "execute" || handler.phase == "wait_for" {
					addHandlerLabel(stepReads, handler.ownerID, access.resourceID)
				}
			case "resource_write":
				handlerWrites[index][access.resourceID] = true
			}
		}
	}
	writers := make(map[string]map[string]bool)
	rpcWriters := make(map[string]bool)
	readWriters := make(map[string]bool)
	rpcReadWriters := make(map[string]map[string]bool)
	commonLocks := make(map[string]map[string]bool)
	isCommonLockBroken := make(map[string]bool)
	for index, handler := range checker.analyzer.handlerSummaries {
		writes := handlerWrites[index]
		if len(writes) == 0 {
			continue
		}
		label := goHandlerLabel(checker.analyzer, handler)
		heldLocks := checker.exactHeldLocks(handler)
		for resourceID := range writes {
			addHandlerLabel(writers, resourceID, label)
			readsInHandler := handlerReads[index][resourceID]
			// A read in the other phase of the same Step runs under a separate lock hold, so no lock protects the pair.
			readsInOtherPhase := !readsInHandler && stepReads[handler.ownerID][resourceID]
			if readsInHandler || readsInOtherPhase {
				readWriters[resourceID] = true
			}
			if handler.phase == "rpc" {
				rpcWriters[resourceID] = true
				if readsInHandler {
					addHandlerLabel(rpcReadWriters, resourceID, label)
				}
			}
			if heldLocks == nil || readsInOtherPhase {
				isCommonLockBroken[resourceID] = true
				continue
			}
			if existing, isSet := commonLocks[resourceID]; isSet {
				commonLocks[resourceID] = intersectLockSets(existing, heldLocks)
			} else {
				commonLocks[resourceID] = heldLocks
			}
		}
	}
	resourceIDs := make([]string, 0, len(writers))
	for resourceID := range writers {
		resourceIDs = append(resourceIDs, resourceID)
	}
	sort.Strings(resourceIDs)
	for _, resourceID := range resourceIDs {
		if _, isDeclared := requirements[resourceID]; isDeclared || !declarationIsReadable[resourceID] {
			continue
		}
		if !isCommonLockBroken[resourceID] && len(commonLocks[resourceID]) > 0 {
			continue
		}
		resource := checker.analyzer.node(resourceID)
		var reason string
		switch {
		case len(writers[resourceID]) >= 2 && rpcWriters[resourceID] && readWriters[resourceID]:
			reason = "is written by " + strings.Join(sortedLabels(writers[resourceID]), ", ") + ", at least one of which also reads it, and no lock is held by every writer"
		case len(rpcReadWriters[resourceID]) > 0:
			reason = "is both read and written by " + strings.Join(sortedLabels(rpcReadWriters[resourceID]), ", ") + " without a registered lock"
		default:
			continue
		}
		checker.analyzer.graph.AddDiagnostic("warning", "lock_requirement_not_declared", fmt.Sprintf(
			"attribute %s %s; its definition declares no lock requirement. If these writes can run concurrently, declare dex.WritesRequireLock() or dex.WritesRequireAttributeLock and register that lock",
			resource.Name, reason), resource.Span)
	}
}

func intersectLockSets(left map[string]bool, right map[string]bool) map[string]bool {
	common := make(map[string]bool)
	for resourceID := range left {
		if right[resourceID] {
			common[resourceID] = true
		}
	}
	return common
}

// exactHeldLocks returns the lock identities that every registration of the handler surely holds, for the nudge only.
// A singleton lock is its resource ID; an AttributeMap instance lock adds its constant key, and a dynamic key is left out.
// It returns nil when the analyzer cannot be sure: a timeout handler, unreadable locks, several GetStepOptions returns,
// or a movement that overrides the Step's options.
func (checker *goDeclaredLockChecker) exactHeldLocks(handler goHandlerSummary) map[string]bool {
	switch handler.phase {
	case "execute", "wait_for":
		stepType := strings.TrimPrefix(handler.ownerID, "step:")
		shape := checker.stepOptionsShape(checker.analyzer.stepValueTypes[stepType])
		if shape.returnCount > 1 || shape.returnCount < 0 || len(checker.sources.stepOptionOverrides[handler.ownerID]) > 0 ||
			len(checker.sources.unattributedStepOverrides) > 0 {
			return nil
		}
		locks, _ := checker.handlerLocks(handler)
		if locks.unresolved != "" {
			return nil
		}
		return checker.exactLockKeys(locks.locks)
	case "rpc":
		registrations, isRegistered := checker.sources.rpcOptions[strings.TrimPrefix(handler.ownerID, "rpc:")]
		if !isRegistered {
			return nil
		}
		var held map[string]bool
		for _, registration := range registrations {
			locks := checker.locksFromStruct(checker.evaluator.evaluateStruct(registration), "LockAttributes")
			if locks.unresolved != "" {
				return nil
			}
			keys := checker.exactLockKeys(locks.locks)
			if held == nil {
				held = keys
			} else {
				held = intersectLockSets(held, keys)
			}
		}
		if held == nil {
			held = make(map[string]bool)
		}
		return held
	}
	return nil
}

func (checker *goDeclaredLockChecker) exactLockKeys(locks []goHeldLock) map[string]bool {
	keys := make(map[string]bool)
	for _, lock := range locks {
		resource := checker.analyzer.node(lock.resourceID)
		isMap := resource.Resource != nil && resource.Resource.Map
		switch {
		case !isMap:
			keys[lock.resourceID] = true
		case lock.instance.isConstant:
			keys[lock.resourceID+"\x00"+lock.instance.value] = true
		}
	}
	return keys
}

func addHandlerLabel(labels map[string]map[string]bool, resourceID string, label string) {
	if labels[resourceID] == nil {
		labels[resourceID] = make(map[string]bool)
	}
	labels[resourceID][label] = true
}

func sortedLabels(labels map[string]bool) []string {
	sorted := make([]string, 0, len(labels))
	for label := range labels {
		sorted = append(sorted, label)
	}
	sort.Strings(sorted)
	return sorted
}
