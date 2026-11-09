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
	"sort"
)

// goRPCLockRecorder records RPCOptions.LockAttributes as resource_lock edges, which Dex Web Actions send.
type goRPCLockRecorder struct {
	analyzer  *goAnalyzer
	evaluator *goOptionsEvaluator
	sources   *goStateLoadSources
}

func (recorder *goRPCLockRecorder) record() {
	methodNames := make([]string, 0, len(recorder.sources.rpcOptions))
	for methodName := range recorder.sources.rpcOptions {
		methodNames = append(methodNames, methodName)
	}
	sort.Strings(methodNames)
	for _, methodName := range methodNames {
		nodeID := "rpc:" + methodName
		if recorder.analyzer.node(nodeID).ID == "" {
			continue
		}
		recorded := make(map[string]bool)
		for _, registration := range recorder.sources.rpcOptions[methodName] {
			protocolName := recorder.protocolName(methodName, registration.expression)
			options := recorder.evaluator.evaluateStruct(registration)
			if len(options.unresolved) > 0 {
				recorder.reportUnresolved(methodName, protocolName, options.unresolved[0])
				continue
			}
			for _, locks := range options.fields["LockAttributes"] {
				list := recorder.evaluator.evaluateList(locks)
				if len(list.unresolved) > 0 {
					recorder.reportUnresolved(methodName, protocolName, list.unresolved[0])
				}
				for _, element := range list.elements {
					resourceID := recorder.lockedResource(element)
					if resourceID == "" {
						recorder.reportUnresolved(methodName, protocolName, recorder.evaluator.unresolved(element))
						continue
					}
					if recorded[resourceID] {
						continue
					}
					recorded[resourceID] = true
					recorder.analyzer.graph.AddEdge(Edge{
						Kind: "resource_lock", From: nodeID, To: resourceID, Label: "Lock",
						Span:     recorder.analyzer.span(element.expression),
						Metadata: map[string]any{"phase": "rpc", "rpcName": protocolName},
					})
				}
			}
		}
	}
}

// lockedResource returns the Attribute or AttributeMap that one AttributeLock value locks.
func (recorder *goRPCLockRecorder) lockedResource(value goScopedExpression) string {
	for depth := 0; depth < goMaximumHelperDepth; depth++ {
		if call, isCall := ast.Unparen(value.expression).(*ast.CallExpr); isCall {
			switch recorder.analyzer.callName(call) {
			case "LockAttribute", "LockAttributeMap":
				if len(call.Args) == 0 {
					return ""
				}
				return recorder.evaluator.resolveResource(value.with(call.Args[0]))
			}
			return ""
		}
		definition, isDefined := recorder.evaluator.definingValue(value)
		if !isDefined {
			return ""
		}
		value = definition
	}
	return ""
}

// protocolName is RPCOptions.Name when it is a static string, else the method name.
func (recorder *goRPCLockRecorder) protocolName(methodName string, options ast.Expr) string {
	if nameExpression := recorder.analyzer.v2RPCOption(options, "Name"); nameExpression != nil {
		if name, isStatic := recorder.analyzer.staticString(nameExpression); isStatic && name != "" {
			return name
		}
	}
	return methodName
}

// Only an RPC that Dex Web invokes needs readable locks; Dex Web refuses an unreadable one.
func (recorder *goRPCLockRecorder) reportUnresolved(methodName string, protocolName string, unresolved goUnresolvedConstruct) {
	if !recorder.isInvokedByDexWeb(protocolName) {
		return
	}
	recorder.analyzer.graph.AddDiagnostic("error", "rpc_lock_unresolved", fmt.Sprintf(
		"dexcli cannot resolve the LockAttributes of RPC %s (%s at %s:%d); Dex Web must take them to invoke it. List dex.LockAttribute or dex.LockAttributeMap calls in RPCOptions or in a variable of this package.",
		methodName, unresolved.construct, recorder.analyzer.displayFilename(unresolved.position.Filename), unresolved.position.Line,
	), recorder.analyzer.node("rpc:"+methodName).Span)
}

func (recorder *goRPCLockRecorder) isInvokedByDexWeb(protocolName string) bool {
	definition := recorder.analyzer.graph.V2
	if definition == nil {
		return false
	}
	if definition.Summary.RPCName == protocolName || definition.Display.RPCName == protocolName {
		return true
	}
	for _, action := range definition.Actions {
		if action.RPCName == protocolName {
			return true
		}
	}
	return false
}
