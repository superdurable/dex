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
}

// goDeclaredLockChecker reports writes to declared resources without the registration lock they require.
type goDeclaredLockChecker struct {
	analyzer  *goAnalyzer
	evaluator *goOptionsEvaluator
	sources   *goStateLoadSources
	reported  map[string]bool
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
	isReadable := true
	for _, option := range definition.Args[min(1, len(definition.Args)):] {
		call, isCall := ast.Unparen(option).(*ast.CallExpr)
		function := ""
		if isCall {
			function = checker.sdkFunctionName(call)
		}
		switch function {
		case "":
			isReadable = false
		case "WritesRequireLock":
			if resource.Kind != "attribute" {
				return goLockRequirement{unresolved: checker.analyzer.expressionString(option)}, true, true
			}
			return goLockRequirement{lockResourceID: resource.ID, sameInstance: resource.Resource != nil && resource.Resource.Map}, true, true
		case "WritesRequireAttributeLock", "PublishesRequireAttributeLock":
			if len(call.Args) != 1 {
				return goLockRequirement{unresolved: checker.analyzer.expressionString(option)}, true, true
			}
			lock := checker.analyzer.node(checker.analyzer.resourceForExpression(call.Args[0]))
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
		registered := checker.evaluator.stepOptionsValue(checker.analyzer.stepValueTypes[stepType], handler.method.Pos())
		field := "ExecuteLockAttributes"
		if handler.phase == "wait_for" {
			field = "WaitForLockAttributes"
		}
		return checker.locksFromStruct(registered, field), true
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
		}
		return locks, true
	}
	return goHandlerLocks{}, false
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
	isUncertain := locks.unresolved != ""
	reason := locks.unresolved
	for _, held := range locks.locks {
		if held.resourceID != requirement.lockResourceID {
			continue
		}
		if !requirement.sameInstance {
			return
		}
		if !held.instance.isConstant || !isWriteKeyKnown {
			isUncertain = true
			if reason == "" && !isWriteKeyKnown {
				reason = "the written instance key is dynamic"
			}
			continue
		}
		if held.instance.value == access.instanceKey.value {
			return
		}
	}
	if isUncertain {
		if reason == "" {
			reason = "an AttributeMap lock with a dynamic instance key"
		}
		checker.addDiagnostic(handler, access, "warning", "declared_lock_unresolved", fmt.Sprintf(
			"%s, which requires %s, but dexcli cannot prove that the registration locks include it (%s)", subject, lockCall, reason))
		return
	}
	checker.addDiagnostic(handler, access, "error", "declared_lock_missing", fmt.Sprintf(
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
// A writer that registers the Attribute's own lock cannot lose an update, so it alone triggers nothing.
func (checker *goDeclaredLockChecker) reportUndeclaredRequirements(requirements map[string]goLockRequirement, declarationIsReadable map[string]bool) {
	writers := make(map[string]map[string]bool)
	rpcWriters := make(map[string]bool)
	unlockedWriters := make(map[string]map[string]bool)
	unlockedRPCReadWriters := make(map[string]map[string]bool)
	for _, handler := range checker.analyzer.handlerSummaries {
		label := goHandlerLabel(checker.analyzer, handler)
		reads := make(map[string]bool)
		writes := make(map[string]bool)
		for _, access := range handler.summary.accesses {
			if checker.analyzer.node(access.resourceID).Kind != "attribute" {
				continue
			}
			switch goResourceEdgeKind(access.methodName, handler.phase) {
			case "resource_read":
				reads[access.resourceID] = true
			case "resource_write":
				writes[access.resourceID] = true
			}
		}
		heldResources := make(map[string]bool)
		if len(writes) > 0 {
			heldResources = checker.ownLockResources(handler)
		}
		for resourceID := range writes {
			addHandlerLabel(writers, resourceID, label)
			if handler.phase == "rpc" {
				rpcWriters[resourceID] = true
			}
			if heldResources == nil || heldResources[resourceID] {
				continue
			}
			addHandlerLabel(unlockedWriters, resourceID, label)
			if reads[resourceID] && handler.phase == "rpc" {
				addHandlerLabel(unlockedRPCReadWriters, resourceID, label)
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
		resource := checker.analyzer.node(resourceID)
		var reason string
		switch {
		case len(writers[resourceID]) >= 2 && rpcWriters[resourceID] && len(unlockedWriters[resourceID]) > 0:
			reason = "is written by " + strings.Join(sortedLabels(writers[resourceID]), ", ") +
				", and " + strings.Join(sortedLabels(unlockedWriters[resourceID]), ", ") + " registers no lock of it"
		case len(unlockedRPCReadWriters[resourceID]) > 0:
			reason = "is read and then written without its lock by " + strings.Join(sortedLabels(unlockedRPCReadWriters[resourceID]), ", ")
		default:
			continue
		}
		checker.analyzer.graph.AddDiagnostic("warning", "lock_requirement_not_declared", fmt.Sprintf(
			"attribute %s %s; its definition declares no lock requirement. If these writes can run concurrently, declare dex.WritesRequireLock() or dex.WritesRequireAttributeLock and register that lock",
			resource.Name, reason), resource.Span)
	}
}

// ownLockResources returns the resources whose own lock, for any instance, the handler registers.
// It returns nil when the registration locks cannot be read, so no write of the handler counts as unlocked.
func (checker *goDeclaredLockChecker) ownLockResources(handler goHandlerSummary) map[string]bool {
	held := make(map[string]bool)
	if handler.phase == "timeout" {
		return nil
	}
	locks, isRegistered := checker.handlerLocks(handler)
	if !isRegistered || locks.unresolved != "" {
		return nil
	}
	for _, lock := range locks.locks {
		held[lock.resourceID] = true
	}
	return held
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
