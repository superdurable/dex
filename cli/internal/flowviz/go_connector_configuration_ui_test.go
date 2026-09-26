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
	"testing"
)

func TestConnectorConfigurationUIUsesStaticUnitComposition(t *testing.T) {
	stringLiteral := func(value string) *ast.BasicLit { return &ast.BasicLit{Kind: token.STRING, Value: `"` + value + `"`} }
	bindingLiteral := &ast.CompositeLit{Elts: []ast.Expr{
		&ast.KeyValueExpr{Key: &ast.Ident{Name: "Port"}, Value: stringLiteral("text")},
		&ast.KeyValueExpr{Key: &ast.Ident{Name: "JSONPointer"}, Value: stringLiteral("/message/text")},
	}}
	required := &ast.Ident{Name: "true"}
	unitLiteral := &ast.CompositeLit{Elts: []ast.Expr{
		&ast.KeyValueExpr{Key: &ast.Ident{Name: "ID"}, Value: stringLiteral("completionMessage")},
		&ast.KeyValueExpr{Key: &ast.Ident{Name: "UnitID"}, Value: stringLiteral("textInput")},
		&ast.KeyValueExpr{Key: &ast.Ident{Name: "Label"}, Value: stringLiteral("Completion message")},
		&ast.KeyValueExpr{Key: &ast.Ident{Name: "Required"}, Value: required},
		&ast.KeyValueExpr{Key: &ast.Ident{Name: "Bindings"}, Value: &ast.CompositeLit{Elts: []ast.Expr{bindingLiteral}}},
	}}
	configurationLiteral := &ast.CompositeLit{Elts: []ast.Expr{
		&ast.KeyValueExpr{Key: &ast.Ident{Name: "Units"}, Value: &ast.CompositeLit{Elts: []ast.Expr{unitLiteral}}},
	}}

	connectorPackage := types.NewPackage(connectorSDKPackage, "sdkgo")
	namedType := func(name string) types.Type {
		return types.NewNamed(types.NewTypeName(token.NoPos, connectorPackage, name, nil), types.NewStruct(nil, nil), nil)
	}
	analyzer := connectorIdentityTestAnalyzer("", goModule{})
	analyzer.typeInfo.Types = map[ast.Expr]types.TypeAndValue{
		configurationLiteral: {Type: namedType("ConnectorConfigurationUI")},
		unitLiteral:          {Type: namedType("ConnectorUIUnit")},
		bindingLiteral:       {Type: namedType("ConnectorUIBinding")},
		required:             {Type: types.Typ[types.Bool], Value: constant.MakeBool(true)},
	}

	configuration := analyzer.parseConnectorConfigurationUI(configurationLiteral)
	if len(configuration.Units) != 1 || configuration.Units[0].ID != "completionMessage" ||
		configuration.Units[0].UnitID != "textInput" || !configuration.Units[0].Required ||
		len(configuration.Units[0].Bindings) != 1 || configuration.Units[0].Bindings[0].JSONPointer != "/message/text" {
		t.Fatalf("configuration = %+v", configuration)
	}
}
