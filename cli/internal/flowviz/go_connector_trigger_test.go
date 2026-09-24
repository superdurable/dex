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
	"go/token"
	"go/types"
	"testing"
)

func TestConnectorTriggerBindingUsesStaticIdentityAndExactModule(t *testing.T) {
	const packagePath = "github.com/superdurable/dex-connectors-library/connectors/slack"
	configType := connectorTriggerTestConfigType(packagePath)
	literal := &ast.CompositeLit{Type: &ast.Ident{Name: "ChannelThreadCreatedFlowTriggerBindingConfig"}, Elts: []ast.Expr{
		&ast.KeyValueExpr{Key: &ast.Ident{Name: "ConnectionName"}, Value: &ast.BasicLit{Kind: token.STRING, Value: `"slack-workspace"`}},
		&ast.KeyValueExpr{Key: &ast.Ident{Name: "BindingName"}, Value: &ast.BasicLit{Kind: token.STRING, Value: `"slack-thread-approval-start"`}},
	}}
	call := &ast.CallExpr{Fun: &ast.Ident{Name: "DefineChannelThreadCreatedFlowTriggerBinding"}, Args: []ast.Expr{literal}}
	analyzer := connectorIdentityTestAnalyzer(packagePath, goModule{path: packagePath, version: "v0.1.0"})
	analyzer.typeInfo.Types = make(map[ast.Expr]types.TypeAndValue)
	analyzer.typeInfo.Types[literal] = types.TypeAndValue{Type: configType}
	binding, ok := analyzer.parseConnectorTriggerBinding(call)
	if !ok || !binding.ConfigurationEnabled || binding.ConnectorID != "slack" ||
		binding.TriggerName != "channelThreadCreated" ||
		binding.ConnectionName != "slack-workspace" || binding.BindingName != "slack-thread-approval-start" ||
		binding.ModulePath != packagePath || binding.ModuleVersion != "v0.1.0" {
		t.Fatalf("binding = %+v, ok = %v", binding, ok)
	}
}

func connectorTriggerTestConfigType(packagePath string) types.Type {
	connectorPackage := types.NewPackage(connectorSDKPackage, "connector")
	marker := types.NewNamed(types.NewTypeName(token.NoPos, connectorPackage, "TriggerBindingFactoryConfigMarker", nil), types.NewStruct(nil, nil), nil)
	slackPackage := types.NewPackage(packagePath, "slack")
	fields := []*types.Var{
		types.NewField(token.NoPos, slackPackage, "TriggerBindingFactoryConfigMarker", marker, true),
		types.NewField(token.NoPos, slackPackage, "connectorID", types.NewStruct(nil, nil), false),
		types.NewField(token.NoPos, slackPackage, "triggerName", types.NewStruct(nil, nil), false),
		types.NewField(token.NoPos, slackPackage, "ConnectionName", types.Typ[types.String], false),
		types.NewField(token.NoPos, slackPackage, "BindingName", types.Typ[types.String], false),
	}
	tags := []string{
		`connector:"factory=triggerBinding"`, `connector:"connectorId=slack"`,
		`connector:"triggerName=channelThreadCreated"`,
		`connector:"connectionName"`, `connector:"bindingName"`,
	}
	structure := types.NewStruct(fields, tags)
	return types.NewNamed(types.NewTypeName(token.NoPos, slackPackage, "ChannelThreadCreatedTriggerBindingConfig", nil), structure, nil)
}
