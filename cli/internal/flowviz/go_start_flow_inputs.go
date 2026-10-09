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
)

type goStartFlowInputChecker struct {
	analyzer *goAnalyzer
	reported map[*ast.CallExpr]bool
}

type goStartStepInputCollector struct {
	typeInfo  *types.Info
	inputType types.Type
	stepType  types.Type
	count     int
}

func newGoStartFlowInputChecker(analyzer *goAnalyzer) *goStartFlowInputChecker {
	return &goStartFlowInputChecker{analyzer: analyzer, reported: make(map[*ast.CallExpr]bool)}
}

func (checker *goStartFlowInputChecker) report() {
	for _, site := range collectGoStartFlowSites(checker.analyzer) {
		if len(site.call.Args) < 4 || checker.reported[site.call] {
			continue
		}
		checker.reported[site.call] = true
		flowType := checker.analyzer.typeInfo.TypeOf(site.call.Args[1])
		inputType, stepType := checker.startStepInputType(flowType)
		providedType := checker.analyzer.typeInfo.TypeOf(site.call.Args[3])
		if inputType == nil || providedType == nil || types.IsInterface(providedType) ||
			types.AssignableTo(providedType, inputType) {
			continue
		}
		if _, isTypeParameter := types.Unalias(providedType).(*types.TypeParam); isTypeParameter {
			continue
		}
		message := fmt.Sprintf(
			"StartFlow of %s%s passes %s to starting Step %s, whose input is %s. The SDK rejects this input before sending the start request. Pass a matching value; if the Step needs no input, declare dex.None and pass nil.",
			goTypeLabel(flowType), goParenthesizedLocation(checker.analyzer.functions.reachedLocation(site.call, site.reach)),
			goStartFlowPayloadTypeLabel(providedType), goTypeLabel(stepType), goStartFlowPayloadTypeLabel(inputType),
		)
		checker.analyzer.graph.AddDiagnostic("error", "start_flow_input_type_mismatch", message,
			checker.analyzer.functions.reachedSpan(site.call, site.reach))
	}
}

func (checker *goStartFlowInputChecker) startStepInputType(flowType types.Type) (types.Type, types.Type) {
	if flowType == nil || types.IsInterface(flowType) {
		return nil, nil
	}
	flowNamedType := registeredNamedType(flowType)
	if flowNamedType == nil || flowNamedType.TypeArgs().Len() > 0 {
		return nil, nil
	}
	selection := types.NewMethodSet(flowType).Lookup(nil, "GetSteps")
	if selection == nil {
		return nil, nil
	}
	method, isMethod := selection.Obj().(*types.Func)
	if !isMethod {
		return nil, nil
	}
	declaration, typeInfo, isFound := checker.analyzer.typeNames.identityMethodDeclaration(method)
	if !isFound || declaration.Body == nil || len(declaration.Body.List) != 1 {
		return nil, nil
	}
	returned, isReturn := declaration.Body.List[0].(*ast.ReturnStmt)
	if !isReturn || len(returned.Results) != 1 {
		return nil, nil
	}
	literal, isLiteral := ast.Unparen(returned.Results[0]).(*ast.CompositeLit)
	if !isLiteral {
		return nil, nil
	}
	collector := &goStartStepInputCollector{typeInfo: typeInfo}
	ast.Inspect(literal, collector.visit)
	if collector.count != 1 {
		return nil, nil
	}
	return collector.inputType, collector.stepType
}

func (collector *goStartStepInputCollector) visit(node ast.Node) bool {
	if _, isFunctionLiteral := node.(*ast.FuncLit); isFunctionLiteral {
		return false
	}
	call, isCall := node.(*ast.CallExpr)
	if !isCall || len(call.Args) == 0 {
		return true
	}
	selector, isSelector := unwrapCallFun(ast.Unparen(call.Fun)).(*ast.SelectorExpr)
	if !isSelector {
		return true
	}
	function, isFunction := collector.typeInfo.Uses[selector.Sel].(*types.Func)
	if !isFunction || function.Pkg() == nil || function.Pkg().Path() != goSDKPackage || function.Name() != "DefineStartStep" {
		return true
	}
	collector.count++
	collector.stepType = collector.typeInfo.TypeOf(call.Args[0])
	if collector.stepType == nil {
		return false
	}
	selection := types.NewMethodSet(collector.stepType).Lookup(nil, "Execute")
	if selection == nil {
		return false
	}
	signature, isSignature := selection.Obj().Type().(*types.Signature)
	if isSignature && signature.Params().Len() == 2 {
		collector.inputType = signature.Params().At(1).Type()
	}
	return false
}

func goStartFlowPayloadTypeLabel(valueType types.Type) string {
	if basic, isBasic := valueType.(*types.Basic); isBasic && basic.Kind() == types.UntypedNil {
		return "nil"
	}
	return types.TypeString(valueType, func(typePackage *types.Package) string { return typePackage.Name() })
}
