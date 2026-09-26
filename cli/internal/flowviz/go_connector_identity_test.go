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

func TestConnectorIdentityUsesExactModuleAndStaticConnectionName(t *testing.T) {
	const packagePath = "github.com/superdurable/dex-connectors-library/connectors/google/gmail"
	analyzer := connectorIdentityTestAnalyzer(packagePath, goModule{path: packagePath, version: "v0.1.1"})
	identity := analyzer.parseConnectorIdentity(
		goConnectorFactoryConfig{
			connectorID: "gmail", operationID: "sendMessage", packagePath: packagePath,
			fieldNames: map[string]string{"connectionName": "ConnectionName"},
		},
		map[string]ast.Expr{"ConnectionName": &ast.BasicLit{Kind: token.STRING, Value: `"sender"`}},
		&ast.CallExpr{Fun: &ast.Ident{Name: "NewSendMessageStep"}},
		connectorMutationFactory,
		true,
	)
	if identity == nil || !identity.configurationEnabled || identity.connectionName != "sender" ||
		identity.modulePath != packagePath || identity.moduleVersion != "v0.1.1" {
		t.Fatalf("identity = %+v", identity)
	}
	metadata := analyzer.connectorFactoryMetadata(connectorMutationFactory, identity)
	connector, ok := metadata["connector"].(map[string]any)
	if !ok || connector["connectorId"] != "gmail" || connector["operationId"] != "sendMessage" ||
		connector["connectionName"] != "sender" || connector["moduleVersion"] != "v0.1.1" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestConnectorIdentityWarnsAndDisablesLocalReplacementOrMissingName(t *testing.T) {
	const packagePath = "github.com/superdurable/dex-connectors-library/connectors/github"
	analyzer := connectorIdentityTestAnalyzer(packagePath, goModule{
		path: packagePath, version: "v0.1.1", replaced: true,
	})
	identity := analyzer.parseConnectorIdentity(
		goConnectorFactoryConfig{
			connectorID: "github", operationID: "getAuthenticatedProfile", packagePath: packagePath,
			fieldNames: map[string]string{"connectionName": "ConnectionName"},
		},
		map[string]ast.Expr{},
		&ast.CallExpr{Fun: &ast.Ident{Name: "NewGetAuthenticatedProfileStep"}},
		connectorQueryFactory,
		true,
	)
	if identity == nil || identity.configurationEnabled {
		t.Fatalf("identity = %+v", identity)
	}
	if len(analyzer.graph.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %+v", analyzer.graph.Diagnostics)
	}
	for _, diagnostic := range analyzer.graph.Diagnostics {
		if diagnostic.Severity != "warning" {
			t.Fatalf("diagnostic = %+v", diagnostic)
		}
	}
}

func TestConnectorFactoryConfigRequiresAnnotationsMetadata(t *testing.T) {
	connectorSDKPackageType := types.NewPackage(connectorSDKPackage, "sdkgo")
	queryMarkerName := types.NewTypeName(token.NoPos, connectorSDKPackageType, "QueryFactoryConfigMarker", nil)
	queryMarkerType := types.NewNamed(queryMarkerName, types.NewStruct(nil, nil), nil)
	configPackage := types.NewPackage("example.com/connector", "connector")

	newConfigType := func(metadataFieldName, metadataTag string) types.Type {
		fields := []*types.Var{
			types.NewField(token.NoPos, configPackage, "QueryFactoryConfigMarker", queryMarkerType, true),
			types.NewField(token.NoPos, configPackage, "StepType", types.Typ[types.String], false),
			types.NewField(token.NoPos, configPackage, metadataFieldName, types.Typ[types.String], false),
			types.NewField(token.NoPos, configPackage, "Found", types.Typ[types.String], false),
		}
		tags := []string{`connector:"factory=query"`, `connector:"stepType"`, metadataTag, `connector:"branch=found"`}
		structure := types.NewStruct(fields, tags)
		configName := types.NewTypeName(token.NoPos, configPackage, metadataFieldName+"Config", nil)
		return types.NewNamed(configName, structure, nil)
	}

	_, annotationsAccepted := connectorFactoryConfig(newConfigType("Annotations", `connector:"annotations"`))
	if !annotationsAccepted {
		t.Fatal("Annotations metadata was rejected")
	}
}

func TestConnectorFactoryConfigParsesOptionalBranch(t *testing.T) {
	connectorSDKPackageType := types.NewPackage(connectorSDKPackage, "sdkgo")
	queryMarkerName := types.NewTypeName(token.NoPos, connectorSDKPackageType, "QueryFactoryConfigMarker", nil)
	queryMarkerType := types.NewNamed(queryMarkerName, types.NewStruct(nil, nil), nil)
	configPackage := types.NewPackage("example.com/connector", "connector")
	fields := []*types.Var{
		types.NewField(token.NoPos, configPackage, "QueryFactoryConfigMarker", queryMarkerType, true),
		types.NewField(token.NoPos, configPackage, "StepType", types.Typ[types.String], false),
		types.NewField(token.NoPos, configPackage, "Annotations", types.Typ[types.String], false),
		types.NewField(token.NoPos, configPackage, "Found", types.Typ[types.String], false),
		types.NewField(token.NoPos, configPackage, "Defect", types.Typ[types.String], false),
	}
	tags := []string{
		`connector:"factory=query"`, `connector:"stepType"`, `connector:"annotations"`,
		`connector:"branch=found"`, `connector:"branch=defect,optional"`,
	}
	structure := types.NewStruct(fields, tags)
	configName := types.NewTypeName(token.NoPos, configPackage, "ReadConfig", nil)
	config, ok := connectorFactoryConfig(types.NewNamed(configName, structure, nil))
	if !ok || len(config.branches) != 2 || config.branches[0].id != "found" || config.branches[0].optional ||
		config.branches[1].id != "defect" || !config.branches[1].optional {
		t.Fatalf("config = %+v ok=%v", config, ok)
	}
}

func TestUnwiredOptionalConnectorBranchRecordsForceFail(t *testing.T) {
	analyzer := &goAnalyzer{graph: NewGraph("go", "workflow.go"), fileSet: token.NewFileSet()}
	branches := analyzer.parseConnectorNamedBranches(map[string]ast.Expr{}, []goConnectorFactoryBranchField{
		{id: "found", fieldName: "Found"},
		{id: "defect", fieldName: "Defect", optional: true},
	})
	if len(analyzer.graph.Diagnostics) != 1 || analyzer.graph.Diagnostics[0].Code != "connector_factory_branch" {
		t.Fatalf("diagnostics = %+v", analyzer.graph.Diagnostics)
	}
	if len(branches) != 1 || branches[0].id != "defect" || !branches[0].optionalUnwired {
		t.Fatalf("branches = %+v", branches)
	}
	analyzer.analyzeConnectorFactoryStep("step:Read", goConnectorFactoryStep{branches: branches})
	var decision *Node
	for index := range analyzer.graph.Nodes {
		node := &analyzer.graph.Nodes[index]
		if node.Kind == "decision" {
			decision = node
		}
	}
	if decision == nil || decision.ParentID != "step:Read" || decision.Condition != "defect" ||
		decision.Decision == nil || decision.Decision.Type != "forceFail" {
		t.Fatalf("decision = %+v", decision)
	}
}

func connectorIdentityTestAnalyzer(packagePath string, module goModule) *goAnalyzer {
	graph := NewGraph("go", "workflow.go")
	return &goAnalyzer{
		graph: graph, fileSet: token.NewFileSet(), typeInfo: &types.Info{},
		modules: map[string]goModule{packagePath: module},
	}
}
