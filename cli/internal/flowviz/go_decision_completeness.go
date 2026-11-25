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
	"go/types"
)

// isDecisionHandlerProvable reports whether every return of the handler is an allowlisted SDK shape.
// Any other construct may hide a Flow-closing decision, so it returns false.
func (analyzer *goAnalyzer) isDecisionHandlerProvable(method *ast.FuncDecl, phase string) bool {
	if method.Body == nil || method.Type.Results == nil {
		return false
	}
	for _, field := range method.Type.Results.List {
		if len(field.Names) > 0 {
			return false
		}
	}
	provable := true
	ast.Inspect(method.Body, func(current ast.Node) bool {
		if !provable {
			return false
		}
		switch statement := current.(type) {
		case *ast.FuncLit:
			return false
		case *ast.DeferStmt:
			provable = false
			return false
		case *ast.ReturnStmt:
			if len(statement.Results) == 0 {
				provable = false
				return false
			}
			result := statement.Results[0]
			switch {
			case analyzer.isUniverseNil(result):
			case phase == "rpc":
				provable = analyzer.isProvableRPCResult(result)
			default:
				provable = analyzer.isProvableStepDecision(result)
			}
			return false
		}
		return true
	})
	return provable
}

func (analyzer *goAnalyzer) isUniverseNil(expression ast.Expr) bool {
	identifier, ok := ast.Unparen(expression).(*ast.Ident)
	return ok && analyzer.typeInfo.Uses[identifier] == types.Universe.Lookup("nil")
}

func (analyzer *goAnalyzer) isProvableStepDecision(expression ast.Expr) bool {
	call, ok := ast.Unparen(expression).(*ast.CallExpr)
	if !ok || call.Ellipsis.IsValid() {
		return false
	}
	function := analyzer.sdkFunction(call.Fun)
	if function == nil {
		return false
	}
	if function.Signature().Recv() != nil {
		switch function.Name() {
		case "CancelSteps", "CancelSiblingSteps":
			selector, isSelector := unwrapCallFun(call.Fun).(*ast.SelectorExpr)
			return isSelector && analyzer.isProvableStepDecision(selector.X)
		case "EndBranch":
			return true
		}
		return false
	}
	switch function.Name() {
	case "GoTo", "GracefulComplete", "ForceComplete", "ForceFail", "DeadEnd", "ForceCompleteIfChannelsEmpty":
		return true
	case "GoToMany":
		return analyzer.areProvableMovements(call.Args)
	}
	return false
}

// isProvableRPCResult accepts only &dex.RPCResult[T]{...} whose NextSteps lists direct MovementOf calls.
func (analyzer *goAnalyzer) isProvableRPCResult(expression ast.Expr) bool {
	address, ok := ast.Unparen(expression).(*ast.UnaryExpr)
	if !ok {
		return false
	}
	literal, ok := ast.Unparen(address.X).(*ast.CompositeLit)
	if !ok || !isSDKNamedType(analyzer.typeInfo.TypeOf(literal), "RPCResult") {
		return false
	}
	for _, element := range literal.Elts {
		field, isKeyed := element.(*ast.KeyValueExpr)
		if !isKeyed {
			return false
		}
		key, isIdentifier := field.Key.(*ast.Ident)
		if !isIdentifier {
			return false
		}
		if key.Name != "NextSteps" {
			continue
		}
		movements, isLiteral := ast.Unparen(field.Value).(*ast.CompositeLit)
		if !isLiteral || !analyzer.areProvableMovements(movements.Elts) {
			return false
		}
	}
	return true
}

func (analyzer *goAnalyzer) areProvableMovements(expressions []ast.Expr) bool {
	for _, expression := range expressions {
		call, ok := ast.Unparen(expression).(*ast.CallExpr)
		if !ok || call.Ellipsis.IsValid() {
			return false
		}
		function := analyzer.sdkFunction(call.Fun)
		if function == nil || function.Signature().Recv() != nil || function.Name() != "MovementOf" {
			return false
		}
	}
	return true
}

// sdkFunction returns the SDK function or method that the type checker resolves for a call, or nil.
func (analyzer *goAnalyzer) sdkFunction(callee ast.Expr) *types.Func {
	var identifier *ast.Ident
	switch current := unwrapCallFun(ast.Unparen(callee)).(type) {
	case *ast.Ident:
		identifier = current
	case *ast.SelectorExpr:
		identifier = current.Sel
	default:
		return nil
	}
	function, ok := analyzer.typeInfo.Uses[identifier].(*types.Func)
	if !ok || function.Pkg() == nil || function.Pkg().Path() != goSDKPackage {
		return nil
	}
	return function
}

func isSDKNamedType(typ types.Type, name string) bool {
	named, ok := types.Unalias(typ).(*types.Named)
	return ok && named.Obj().Name() == name && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == goSDKPackage
}

// embedsOnlySDKTypes reports whether every embedded field comes from the SDK, so no user type promotes a handler.
func embedsOnlySDKTypes(typ types.Type) bool {
	if typ == nil {
		return false
	}
	if pointer, ok := types.Unalias(typ).(*types.Pointer); ok {
		typ = pointer.Elem()
	}
	structure, ok := typ.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for index := range structure.NumFields() {
		field := structure.Field(index)
		if !field.Embedded() {
			continue
		}
		fieldType := types.Unalias(field.Type())
		if pointer, isPointer := fieldType.(*types.Pointer); isPointer {
			fieldType = types.Unalias(pointer.Elem())
		}
		named, isNamed := fieldType.(*types.Named)
		if !isNamed || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != goSDKPackage {
			return false
		}
	}
	return true
}

// checkPromotedHandlers marks decision analysis incomplete when a user type embedded in the Flow or a Step could supply a handler.
func (analyzer *goAnalyzer) checkPromotedHandlers(getSteps *ast.FuncDecl) {
	var flowType types.Type
	if receiver := analyzer.flowReceiverType(getSteps); receiver != nil {
		flowType = receiver
	}
	if !embedsOnlySDKTypes(flowType) {
		analyzer.graph.decisionAnalysisComplete = false
	}
	for stepType := range analyzer.steps {
		if _, isFactory := analyzer.connectorFactories[stepType]; isFactory {
			continue
		}
		if !embedsOnlySDKTypes(analyzer.stepValueTypes[stepType]) {
			analyzer.graph.decisionAnalysisComplete = false
		}
	}
}
