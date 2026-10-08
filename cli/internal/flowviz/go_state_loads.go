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

// goStateLoadChecker reports handler reads of AttributeMap values and pending messages that no load selects.
type goStateLoadChecker struct {
	analyzer          *goAnalyzer
	functions         *goFunctionWalker
	evaluator         *goOptionsEvaluator
	isApplicationLint bool
	sources           *goStateLoadSources
	reported          map[string]bool
}

// goHandlerStateLoads is the may-load union of every load field one handler method can receive.
type goHandlerStateLoads struct {
	attributeMaps         goStateLoadSet
	attributeMapInstances goStateLoadSet
	channels              goStateLoadSet
	channelMaps           goStateLoadSet
	channelMapInstances   goStateLoadSet
}

// goStateLoadSources holds load selections; one whose Step or RPC is unknown applies to all of them.
type goStateLoadSources struct {
	stepOptionOverrides         map[string][]goScopedExpression
	stepOptionUnknowns          map[string][]goUnresolvedConstruct
	unattributedStepOverrides   []goScopedExpression
	unattributedStepUnknowns    []goUnresolvedConstruct
	rpcOptions                  map[string][]goScopedExpression
	rpcInvocationLoads          map[string]*goHandlerStateLoads
	unattributedInvocationLoads *goHandlerStateLoads
	timeoutOptions              []goScopedExpression
	timeoutFieldValues          map[string][]goScopedExpression
}

type goStateLoadSourceCollector struct {
	checker         *goStateLoadChecker
	flowObject      *types.TypeName
	scope           *goEvaluationScope
	invocationLoads *goHandlerStateLoads
}

// goStateLoadFields names one handler kind's load fields and where an application declares them.
type goStateLoadFields struct {
	attributeMaps         string
	attributeMapInstances string
	channels              string
	channelMaps           string
	channelMapInstances   string
	location              string
}

// goInvocationLoadDirectiveKinds maps dex:invocation-load arguments to the resource kind they name.
var goInvocationLoadDirectiveKinds = map[string]string{"attribute-map": "attribute", "channel-map": "channel"}

var goStateLoadFieldsByPhase = map[string]goStateLoadFields{
	"execute": {
		attributeMaps:         "ExecuteLoadAttributeMaps",
		attributeMapInstances: "ExecuteLoadAttributeMapInstances",
		channels:              "ExecuteLoadChannels",
		channelMaps:           "ExecuteLoadChannelMaps",
		channelMapInstances:   "ExecuteLoadChannelMapInstances",
		location:              "GetStepOptions or to dex.WithStepOptions on the GoTo",
	},
	"wait_for": {
		attributeMaps:         "WaitForLoadAttributeMaps",
		attributeMapInstances: "WaitForLoadAttributeMapInstances",
		channels:              "WaitForLoadChannels",
		channelMaps:           "WaitForLoadChannelMaps",
		channelMapInstances:   "WaitForLoadChannelMapInstances",
		location:              "GetStepOptions or to dex.WithStepOptions on the GoTo",
	},
	"rpc": {
		attributeMaps:         "LoadAttributeMaps",
		attributeMapInstances: "LoadAttributeMapInstances",
		channels:              "LoadChannels",
		channelMaps:           "LoadChannelMaps",
		channelMapInstances:   "LoadChannelMapInstances",
		location:              "its dex.RPCOptions",
	},
	"timeout": {
		attributeMaps:         "LoadAttributeMaps",
		attributeMapInstances: "LoadAttributeMapInstances",
		channels:              "LoadChannels",
		channelMaps:           "LoadChannelMaps",
		channelMapInstances:   "LoadChannelMapInstances",
		location:              "the dex.FlowTimeoutHandlerOptions of the StartFlow or SubFlow call",
	},
}

func newGoStateLoadChecker(
	analyzer *goAnalyzer,
	evaluator *goOptionsEvaluator,
	flowObject *types.TypeName,
	isApplicationLint bool,
) *goStateLoadChecker {
	checker := &goStateLoadChecker{
		analyzer:          analyzer,
		functions:         evaluator.functions,
		evaluator:         evaluator,
		isApplicationLint: isApplicationLint,
		reported:          make(map[string]bool),
		sources: &goStateLoadSources{
			stepOptionOverrides:         make(map[string][]goScopedExpression),
			stepOptionUnknowns:          make(map[string][]goUnresolvedConstruct),
			rpcOptions:                  make(map[string][]goScopedExpression),
			rpcInvocationLoads:          make(map[string]*goHandlerStateLoads),
			unattributedInvocationLoads: &goHandlerStateLoads{},
			timeoutFieldValues:          make(map[string][]goScopedExpression),
		},
	}
	checker.collectSources(flowObject)
	return checker
}

func (checker *goStateLoadChecker) collectSources(flowObject *types.TypeName) {
	packageScope := &goEvaluationScope{}
	for _, file := range checker.analyzer.packageFiles {
		for _, declaration := range file.Decls {
			scope := packageScope
			if function, isFunction := declaration.(*ast.FuncDecl); isFunction {
				scope = &goEvaluationScope{function: function}
			}
			collector := &goStateLoadSourceCollector{checker: checker, flowObject: flowObject, scope: scope}
			ast.Inspect(declaration, collector.visit)
		}
	}
}

func (checker *goStateLoadChecker) report() {
	for _, handler := range checker.analyzer.handlerSummaries {
		loads, isChecked := checker.handlerLoads(handler)
		if !isChecked {
			continue
		}
		for _, access := range handler.summary.accesses {
			checker.checkAccess(handler, loads, access)
		}
	}
}

func (checker *goStateLoadChecker) handlerLoads(handler goHandlerSummary) (goHandlerStateLoads, bool) {
	fields := goStateLoadFieldsByPhase[handler.phase]
	switch handler.phase {
	case "execute", "wait_for":
		return checker.stepLoads(handler, fields), true
	case "rpc":
		return checker.rpcLoads(handler, fields)
	case "timeout":
		return checker.timeoutLoads(handler, fields), true
	}
	return goHandlerStateLoads{}, false
}

func (checker *goStateLoadChecker) stepLoads(handler goHandlerSummary, fields goStateLoadFields) goHandlerStateLoads {
	stepType := strings.TrimPrefix(handler.ownerID, "step:")
	registered := checker.evaluator.stepOptionsValue(checker.analyzer.stepValueTypes[stepType], handler.method.Pos())
	loads := checker.loadsFromStruct(registered, fields)
	for _, overrides := range [][]goScopedExpression{checker.sources.stepOptionOverrides[handler.ownerID], checker.sources.unattributedStepOverrides} {
		for _, override := range overrides {
			loads.merge(checker.loadsFromStruct(checker.evaluator.evaluateStruct(override), fields))
		}
	}
	for _, unknowns := range [][]goUnresolvedConstruct{checker.sources.stepOptionUnknowns[handler.ownerID], checker.sources.unattributedStepUnknowns} {
		for _, unresolved := range unknowns {
			loads.markUnresolved(unresolved)
		}
	}
	return loads
}

// An RPC that no DefineRPC registers never runs, so its reads are not checked.
func (checker *goStateLoadChecker) rpcLoads(handler goHandlerSummary, fields goStateLoadFields) (goHandlerStateLoads, bool) {
	rpcName := strings.TrimPrefix(handler.ownerID, "rpc:")
	registrations, isRegistered := checker.sources.rpcOptions[rpcName]
	if !isRegistered {
		return goHandlerStateLoads{}, false
	}
	loads := goHandlerStateLoads{}
	for _, registration := range registrations {
		loads.merge(checker.loadsFromStruct(checker.evaluator.evaluateStruct(registration), fields))
	}
	if invocationLoads := checker.sources.rpcInvocationLoads[rpcName]; invocationLoads != nil {
		loads.merge(*invocationLoads)
	}
	loads.merge(*checker.sources.unattributedInvocationLoads)
	checker.addInvocationLoadDirectives(handler, &loads)
	return loads, true
}

// Timeout handler loads come from options at StartFlow sites, which may all be outside this package.
func (checker *goStateLoadChecker) timeoutLoads(handler goHandlerSummary, fields goStateLoadFields) goHandlerStateLoads {
	if len(checker.sources.timeoutOptions) == 0 && len(checker.sources.timeoutFieldValues) == 0 {
		loads := goHandlerStateLoads{}
		loads.markUnresolved(checker.evaluator.unresolvedAt("HandleTimeout without dex.FlowTimeoutHandlerOptions in this package", handler.method.Pos()))
		return loads
	}
	loads := goHandlerStateLoads{}
	for _, options := range checker.sources.timeoutOptions {
		loads.merge(checker.loadsFromStruct(checker.evaluator.evaluateStruct(options), fields))
	}
	assigned := goStructValue{fields: checker.sources.timeoutFieldValues}
	loads.merge(checker.loadsFromStruct(assigned, fields))
	return loads
}

func (checker *goStateLoadChecker) loadsFromStruct(value goStructValue, fields goStateLoadFields) goHandlerStateLoads {
	return goHandlerStateLoads{
		attributeMaps:         checker.evaluator.evaluateLoads(value, fields.attributeMaps, false),
		attributeMapInstances: checker.evaluator.evaluateLoads(value, fields.attributeMapInstances, true),
		channels:              checker.evaluator.evaluateLoads(value, fields.channels, false),
		channelMaps:           checker.evaluator.evaluateLoads(value, fields.channelMaps, false),
		channelMapInstances:   checker.evaluator.evaluateLoads(value, fields.channelMapInstances, true),
	}
}

// The directive declares that callers in another package select instances of the named map.
func (checker *goStateLoadChecker) addInvocationLoadDirectives(handler goHandlerSummary, loads *goHandlerStateLoads) {
	if handler.method.Doc == nil {
		return
	}
	for _, comment := range handler.method.Doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
		if text != "dex:invocation-load" && !strings.HasPrefix(text, "dex:invocation-load ") {
			continue
		}
		resourceID := checker.invocationLoadDirectiveResource(text, comment)
		switch {
		case resourceID == "":
			checker.analyzer.graph.AddDiagnostic("error", "invocation_load_directive", fmt.Sprintf(
				"dex:invocation-load on RPC %s must name one declared AttributeMap (attribute-map:<name>) or ChannelMap (channel-map:<name>)",
				handler.method.Name.Name,
			), checker.analyzer.span(comment))
		case strings.HasPrefix(resourceID, "resource:attribute:"):
			loads.attributeMapInstances.loads = append(loads.attributeMapInstances.loads, goStateLoad{resourceID: resourceID})
		default:
			loads.channelMapInstances.loads = append(loads.channelMapInstances.loads, goStateLoad{resourceID: resourceID})
		}
	}
}

func (checker *goStateLoadChecker) invocationLoadDirectiveResource(text string, comment *ast.Comment) string {
	directive, err := parseV2Directive(text, comment)
	if err != nil || len(directive.arguments) != 1 {
		return ""
	}
	for argumentName, argument := range directive.arguments {
		kind := goInvocationLoadDirectiveKinds[argumentName]
		if kind == "" || argument.array {
			return ""
		}
		for _, node := range checker.analyzer.graph.Nodes {
			if node.Kind == kind && node.Name == argument.text && node.Resource != nil && node.Resource.Map {
				return node.ID
			}
		}
	}
	return ""
}

func (checker *goStateLoadChecker) checkAccess(handler goHandlerSummary, loads goHandlerStateLoads, access goResourceAccess) {
	resource := checker.analyzer.node(access.resourceID)
	if resource.Resource == nil {
		return
	}
	isMap := resource.Resource.Map
	switch {
	case resource.Kind == "attribute" && isMap && access.methodName == "Get":
		checker.checkAttributeMapRead(handler, loads, access, resource)
	case resource.Kind == "attribute" && isMap && (access.methodName == "AllInstanceKeys" || access.methodName == "MapSize"):
		checker.checkAttributeMapEnumeration(handler, loads, access, resource)
	case resource.Kind == "channel" && (access.methodName == "PendingMessages" || access.methodName == "FindPendingMessage"):
		checker.checkPendingMessagesRead(handler, loads, access, resource)
	}
}

func (checker *goStateLoadChecker) checkAttributeMapRead(handler goHandlerSummary, loads goHandlerStateLoads, access goResourceAccess, resource Node) {
	if loads.attributeMaps.hasResource(access.resourceID) {
		return
	}
	isUnresolved := loads.attributeMaps.isUnresolved() || loads.attributeMapInstances.isUnresolved()
	instanceLoads := loads.attributeMapInstances.loadsOf(access.resourceID)
	if len(instanceLoads) > 0 {
		loadedKeys, isEveryKeyConstant := goConstantInstanceKeys(instanceLoads)
		if isUnresolved || !isEveryKeyConstant || !access.instanceKey.isConstant || containsString(loadedKeys, access.instanceKey.value) {
			return
		}
		checker.addDiagnostic(handler, access, "attribute_map_instance_not_loaded", fmt.Sprintf(
			"%s reads instance %q of %q (%s%s) but loads only %s.",
			goHandlerLabel(checker.analyzer, handler), access.instanceKey.value, resource.Name, access.methodName,
			checker.functions.reachedLocation(access.call, access.reach), goQuotedList(loadedKeys),
		))
		return
	}
	if isUnresolved {
		checker.reportUnresolved(handler, access, loads.attributeMaps, loads.attributeMapInstances)
		return
	}
	fields := goStateLoadFieldsByPhase[handler.phase]
	variable := goResourceVariableName(access.resourceID)
	checker.addDiagnostic(handler, access, "attribute_map_read_not_loaded", fmt.Sprintf(
		"%s reads AttributeMap %q (%s%s) without loading it; AttributeMaps are not loaded by default. Add %s: []dex.AttributeMapLoad{%s.Load(key)} (or %s: []dex.AttributeDef{%s} for every instance) to %s.%s",
		goHandlerLabel(checker.analyzer, handler), resource.Name, access.methodName, checker.functions.reachedLocation(access.call, access.reach),
		fields.attributeMapInstances, variable, fields.attributeMaps, variable, fields.location,
		goInvocationLoadHint(handler, "attribute-map", resource.Name),
	))
}

func (checker *goStateLoadChecker) checkAttributeMapEnumeration(handler goHandlerSummary, loads goHandlerStateLoads, access goResourceAccess, resource Node) {
	if loads.attributeMaps.hasResource(access.resourceID) {
		return
	}
	if loads.attributeMaps.isUnresolved() {
		checker.reportUnresolved(handler, access, loads.attributeMaps)
		return
	}
	fields := goStateLoadFieldsByPhase[handler.phase]
	checker.addDiagnostic(handler, access, "attribute_map_enumeration_not_loaded", fmt.Sprintf(
		"%s enumerates AttributeMap %q (%s%s) without loading the whole map; this panics at run time. Add %s: []dex.AttributeDef{%s} to %s; exact-instance loads cannot enumerate.",
		goHandlerLabel(checker.analyzer, handler), resource.Name, access.methodName, checker.functions.reachedLocation(access.call, access.reach),
		fields.attributeMaps, goResourceVariableName(access.resourceID), fields.location,
	))
}

func (checker *goStateLoadChecker) checkPendingMessagesRead(handler goHandlerSummary, loads goHandlerStateLoads, access goResourceAccess, resource Node) {
	fields := goStateLoadFieldsByPhase[handler.phase]
	variable := goResourceVariableName(access.resourceID)
	location := checker.functions.reachedLocation(access.call, access.reach)
	const noLoadNeeded = "ForOne/ForN waits, GetConditionResults, Size, Publish and Delete need no load."
	if !resource.Resource.Map {
		if loads.channels.hasResource(access.resourceID) {
			return
		}
		if loads.channels.isUnresolved() {
			checker.reportUnresolved(handler, access, loads.channels)
			return
		}
		checker.addDiagnostic(handler, access, "channel_messages_not_loaded", fmt.Sprintf(
			"%s reads pending messages of Channel %q (%s%s) without loading them. Add %s: []dex.ChannelDef{%s} to %s. %s",
			goHandlerLabel(checker.analyzer, handler), resource.Name, access.methodName, location, fields.channels, variable, fields.location, noLoadNeeded,
		))
		return
	}
	if loads.channelMaps.hasResource(access.resourceID) || loads.channelMapInstances.hasResource(access.resourceID) {
		return
	}
	if loads.channelMaps.isUnresolved() || loads.channelMapInstances.isUnresolved() {
		checker.reportUnresolved(handler, access, loads.channelMaps, loads.channelMapInstances)
		return
	}
	checker.addDiagnostic(handler, access, "channel_messages_not_loaded", fmt.Sprintf(
		"%s reads pending messages of ChannelMap %q (%s%s) without loading them. Add %s: []dex.ChannelMapLoad{%s.LoadMessages(key)} (or %s: []dex.ChannelDef{%s} for every instance) to %s.%s %s",
		goHandlerLabel(checker.analyzer, handler), resource.Name, access.methodName, location,
		fields.channelMapInstances, variable, fields.channelMaps, variable, fields.location,
		goInvocationLoadHint(handler, "channel-map", resource.Name), noLoadNeeded,
	))
}

// Default mode stays silent when a load field cannot be evaluated; application lints name it.
func (checker *goStateLoadChecker) reportUnresolved(handler goHandlerSummary, access goResourceAccess, loadSets ...goStateLoadSet) {
	if !checker.isApplicationLint {
		return
	}
	for _, loads := range loadSets {
		if !loads.isUnresolved() {
			continue
		}
		unresolved := loads.unresolved[0]
		checker.addDiagnostic(handler, access, "state_load_unresolved", fmt.Sprintf(
			"dexcli cannot resolve the load options of %s (%s at %s:%d); declare loads in GetStepOptions, RPCOptions or a variable of this package.",
			goHandlerLabel(checker.analyzer, handler), unresolved.construct, checker.analyzer.displayFilename(unresolved.position.Filename), unresolved.position.Line,
		))
		return
	}
}

func (checker *goStateLoadChecker) addDiagnostic(handler goHandlerSummary, access goResourceAccess, code string, message string) {
	key := handler.ownerID + "|" + handler.phase + "|" + code + "|" + message
	if checker.reported[key] {
		return
	}
	checker.reported[key] = true
	checker.analyzer.graph.AddDiagnostic("error", code, message, checker.functions.reachedSpan(access.call, access.reach))
}

func (collector *goStateLoadSourceCollector) visit(node ast.Node) bool {
	switch current := node.(type) {
	case *ast.CallExpr:
		collector.collectCall(current)
	case *ast.CompositeLit:
		if isGoSDKNamedType(collector.checker.analyzer.typeInfo.TypeOf(current), "FlowTimeoutHandlerOptions") {
			collector.checker.sources.timeoutOptions = append(collector.checker.sources.timeoutOptions, goScopedExpression{expression: current, scope: collector.scope})
		}
	case *ast.AssignStmt:
		collector.collectTimeoutFieldAssignment(current)
	}
	return true
}

func (collector *goStateLoadSourceCollector) collectCall(call *ast.CallExpr) {
	switch collector.checker.analyzer.callName(call) {
	case "GoTo", "MovementOf":
		collector.collectMovement(call)
	case "ProceedToOnExecuteFailure", "ProceedToOnFlowTimeoutHandlerFailure":
		collector.collectFailureRoute(call)
	case "DefineRPC":
		collector.collectRPCRegistration(call)
	default:
		collector.collectInvocationLoads(call)
	}
}

func (collector *goStateLoadSourceCollector) collectMovement(call *ast.CallExpr) {
	if len(call.Args) < 2 {
		return
	}
	stepNodeID := collector.stepNodeID(call.Args[0])
	for _, option := range call.Args[2:] {
		optionCall, isCall := ast.Unparen(option).(*ast.CallExpr)
		if isCall && collector.checker.analyzer.callName(optionCall) == "WithStepOptions" && len(optionCall.Args) == 1 {
			collector.addStepOverride(stepNodeID, goScopedExpression{expression: optionCall.Args[0], scope: collector.scope})
			continue
		}
		unresolved := collector.checker.evaluator.unresolved(goScopedExpression{expression: option, scope: collector.scope})
		sources := collector.checker.sources
		if stepNodeID == "" {
			sources.unattributedStepUnknowns = append(sources.unattributedStepUnknowns, unresolved)
			continue
		}
		sources.stepOptionUnknowns[stepNodeID] = append(sources.stepOptionUnknowns[stepNodeID], unresolved)
	}
}

func (collector *goStateLoadSourceCollector) collectFailureRoute(call *ast.CallExpr) {
	if len(call.Args) < 2 {
		return
	}
	collector.addStepOverride(collector.stepNodeID(call.Args[0]), goScopedExpression{expression: call.Args[1], scope: collector.scope})
}

func (collector *goStateLoadSourceCollector) addStepOverride(stepNodeID string, options goScopedExpression) {
	sources := collector.checker.sources
	if stepNodeID == "" {
		sources.unattributedStepOverrides = append(sources.unattributedStepOverrides, options)
		return
	}
	sources.stepOptionOverrides[stepNodeID] = append(sources.stepOptionOverrides[stepNodeID], options)
}

func (collector *goStateLoadSourceCollector) collectRPCRegistration(call *ast.CallExpr) {
	if len(call.Args) < 2 {
		return
	}
	rpcName := collector.flowMethodName(call.Args[0])
	if rpcName == "" {
		return
	}
	sources := collector.checker.sources
	sources.rpcOptions[rpcName] = append(sources.rpcOptions[rpcName], goScopedExpression{expression: call.Args[1], scope: collector.scope})
}

// Load selections passed with an RPC method value load those instances; unnamed InvokeRPCWithOptions targets apply to every RPC.
func (collector *goStateLoadSourceCollector) collectInvocationLoads(call *ast.CallExpr) {
	rpcName := ""
	for _, argument := range call.Args {
		if rpcName = collector.flowMethodName(argument); rpcName != "" {
			break
		}
	}
	sources := collector.checker.sources
	switch {
	case rpcName != "":
		if sources.rpcInvocationLoads[rpcName] == nil {
			sources.rpcInvocationLoads[rpcName] = &goHandlerStateLoads{}
		}
		collector.invocationLoads = sources.rpcInvocationLoads[rpcName]
	case collector.checker.functions.clientMethodName(call) == "InvokeRPCWithOptions" && !collector.isMethodValue(call.Args[2]):
		collector.invocationLoads = sources.unattributedInvocationLoads
	default:
		return
	}
	for _, argument := range call.Args {
		ast.Inspect(argument, collector.collectInvocationLoad)
		collector.collectInvocationOptions(argument)
	}
	collector.invocationLoads = nil
}

func (collector *goStateLoadSourceCollector) collectInvocationLoad(node ast.Node) bool {
	call, isCall := node.(*ast.CallExpr)
	if !isCall || len(call.Args) != 1 {
		return true
	}
	selector, isSelector := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !isSelector || (selector.Sel.Name != "Load" && selector.Sel.Name != "LoadMessages") {
		return true
	}
	resourceID := collector.checker.evaluator.resolveResource(goScopedExpression{expression: selector.X, scope: collector.scope})
	if resourceID == "" {
		return true
	}
	key, isConstant := collector.checker.analyzer.staticString(call.Args[0])
	load := goStateLoad{resourceID: resourceID, instanceKey: goInstanceKey{value: key, isConstant: isConstant}}
	loads := collector.invocationLoads
	if selector.Sel.Name == "Load" {
		loads.attributeMapInstances.loads = append(loads.attributeMapInstances.loads, load)
	} else {
		loads.channelMapInstances.loads = append(loads.channelMapInstances.loads, load)
	}
	return true
}

// Invocation options held in a variable are evaluated; literals are covered by the Load scan.
func (collector *goStateLoadSourceCollector) collectInvocationOptions(argument ast.Expr) {
	if _, isLiteral := ast.Unparen(argument).(*ast.CompositeLit); isLiteral {
		return
	}
	if !isGoSDKNamedType(collector.checker.analyzer.typeInfo.TypeOf(argument), "RPCInvokeOptions") {
		return
	}
	value := collector.checker.evaluator.evaluateStruct(goScopedExpression{expression: argument, scope: collector.scope})
	loads := collector.invocationLoads
	loads.attributeMapInstances.merge(collector.checker.evaluator.evaluateLoads(value, "LoadAttributeMapInstances", true))
	loads.channelMapInstances.merge(collector.checker.evaluator.evaluateLoads(value, "LoadChannelMapInstances", true))
}

func (collector *goStateLoadSourceCollector) collectTimeoutFieldAssignment(assignment *ast.AssignStmt) {
	if len(assignment.Lhs) != len(assignment.Rhs) {
		return
	}
	for index, left := range assignment.Lhs {
		selector, isSelector := left.(*ast.SelectorExpr)
		if !isSelector || !isGoSDKNamedType(collector.checker.analyzer.typeInfo.TypeOf(selector.X), "FlowTimeoutHandlerOptions") {
			continue
		}
		sources := collector.checker.sources
		sources.timeoutFieldValues[selector.Sel.Name] = append(
			sources.timeoutFieldValues[selector.Sel.Name],
			goScopedExpression{expression: assignment.Rhs[index], scope: collector.scope},
		)
	}
}

func (collector *goStateLoadSourceCollector) stepNodeID(target ast.Expr) string {
	stepType, isStepRef := collector.checker.analyzer.goTransitionTarget(target)
	if isStepRef {
		return collector.checker.analyzer.registeredStepNodeIDs[stepType]
	}
	return collector.checker.analyzer.steps[stepType]
}

func (collector *goStateLoadSourceCollector) isMethodValue(expression ast.Expr) bool {
	selector, isSelector := ast.Unparen(expression).(*ast.SelectorExpr)
	if !isSelector {
		return false
	}
	selection := collector.checker.analyzer.typeInfo.Selections[selector]
	return selection != nil && (selection.Kind() == types.MethodVal || selection.Kind() == types.MethodExpr)
}

// flowMethodName returns the RPC name of a method value or expression on the analyzed Flow type.
func (collector *goStateLoadSourceCollector) flowMethodName(expression ast.Expr) string {
	selector, isSelector := ast.Unparen(expression).(*ast.SelectorExpr)
	if !isSelector || collector.flowObject == nil {
		return ""
	}
	selection := collector.checker.analyzer.typeInfo.Selections[selector]
	if selection == nil || (selection.Kind() != types.MethodVal && selection.Kind() != types.MethodExpr) {
		return ""
	}
	method, isFunction := selection.Obj().(*types.Func)
	if !isFunction || method.Signature().Recv() == nil {
		return ""
	}
	receiver := registeredNamedType(method.Signature().Recv().Type())
	if receiver == nil || receiver.Origin().Obj() != collector.flowObject {
		return ""
	}
	return method.Name()
}

func (loads *goHandlerStateLoads) merge(other goHandlerStateLoads) {
	loads.attributeMaps.merge(other.attributeMaps)
	loads.attributeMapInstances.merge(other.attributeMapInstances)
	loads.channels.merge(other.channels)
	loads.channelMaps.merge(other.channelMaps)
	loads.channelMapInstances.merge(other.channelMapInstances)
}

func (loads *goHandlerStateLoads) markUnresolved(unresolved goUnresolvedConstruct) {
	for _, loadSet := range []*goStateLoadSet{
		&loads.attributeMaps, &loads.attributeMapInstances, &loads.channels, &loads.channelMaps, &loads.channelMapInstances,
	} {
		loadSet.unresolved = append(loadSet.unresolved, unresolved)
	}
}

func goHandlerLabel(analyzer *goAnalyzer, handler goHandlerSummary) string {
	switch handler.phase {
	case "execute":
		return "Step " + analyzer.node(handler.ownerID).Name + " Execute"
	case "wait_for":
		return "Step " + analyzer.node(handler.ownerID).Name + " WaitFor"
	case "rpc":
		return "RPC " + strings.TrimPrefix(handler.ownerID, "rpc:")
	default:
		return "HandleTimeout"
	}
}

func goInvocationLoadHint(handler goHandlerSummary, directiveKind string, resourceName string) string {
	if handler.phase != "rpc" {
		return ""
	}
	return fmt.Sprintf(
		" An InvokeRPCWithOptions call in this package can load the instance it selects; when another package selects it, declare // dex:invocation-load %s:%s on the RPC.",
		directiveKind, resourceName,
	)
}

func goConstantInstanceKeys(loads []goStateLoad) ([]string, bool) {
	keys := make([]string, 0, len(loads))
	for _, load := range loads {
		if !load.instanceKey.isConstant {
			return nil, false
		}
		keys = appendUnique(keys, load.instanceKey.value)
	}
	sort.Strings(keys)
	return keys, true
}

func goQuotedList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return strings.Join(quoted, ", ")
}

func goResourceVariableName(resourceID string) string {
	parts := strings.SplitN(resourceID, ":", 3)
	return parts[len(parts)-1]
}
