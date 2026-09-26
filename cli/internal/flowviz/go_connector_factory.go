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
	"reflect"
	"regexp"
	"strings"
)

const (
	connectorQueryFactory    = "query"
	connectorMutationFactory = "mutation"
)

var connectorReleaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
var officialConnectorModulePattern = regexp.MustCompile(`^github\.com/superdurable/dex-connectors-library/connectors/[a-z0-9][a-z0-9-]*(?:/[a-z0-9][a-z0-9-]*)*$`)

type goConnectorFactoryStep struct {
	stepType             string
	annotations          goConnectorAnnotations
	branches             []goConnectorBranch
	resultAttributeID    string
	progressStreamID     string
	textStreamID         string
	executeFailureTarget string
	connector            *goConnectorIdentity
}

type goConnectorIdentity struct {
	connectorID          string
	operationID          string
	operationKind        string
	connectionName       string
	modulePath           string
	moduleVersion        string
	configurationEnabled bool
	configurationUI      ConnectorConfigurationUI
}

type goConnectorAnnotations struct {
	groupID     string
	groupLabel  string
	explanation string
}

type goConnectorBranch struct {
	id     string
	target string
	span   *Span
}

type goConnectorFactoryConfig struct {
	kind        string
	connectorID string
	operationID string
	packagePath string
	fieldNames  map[string]string
	branches    []goConnectorFactoryBranchField
}

type goConnectorFactoryBranchField struct {
	id        string
	fieldName string
}

func (analyzer *goAnalyzer) connectorFactoryMetadata(kind string, identity *goConnectorIdentity) map[string]any {
	metadata := map[string]any{"connectorFactory": true, "connectorOperationKind": kind}
	if identity == nil {
		return metadata
	}
	metadata["connector"] = map[string]any{
		"connectorId": identity.connectorID, "operationId": identity.operationID,
		"operationKind": identity.operationKind, "connectionName": identity.connectionName,
		"modulePath": identity.modulePath, "moduleVersion": identity.moduleVersion,
		"configurationEnabled": identity.configurationEnabled,
		"configurationUI":      identity.configurationUI,
	}
	return metadata
}

func (analyzer *goAnalyzer) connectorFactoryCall(expression ast.Expr) (string, *ast.CallExpr, bool) {
	call, ok := unwrappedExpression(expression).(*ast.CallExpr)
	if !ok {
		return "", nil, false
	}
	packagePath, functionName := analyzer.goCallIdentity(call)
	if packagePath == connectorSDKPackage {
		switch functionName {
		case "MustNewQueryStep":
			return connectorQueryFactory, call, true
		case "MustNewMutationStep":
			return connectorMutationFactory, call, true
		}
	}
	if len(call.Args) != 1 {
		return "", nil, false
	}
	configType := analyzer.typeInfo.Types[call.Args[0]].Type
	config, ok := connectorFactoryConfig(configType)
	if !ok || !isConnectorFactoryStepType(analyzer.typeInfo.Types[call].Type, config.kind) {
		return "", nil, false
	}
	return config.kind, call, true
}

func (analyzer *goAnalyzer) parseConnectorFactoryStep(kind string, call *ast.CallExpr) (goConnectorFactoryStep, bool) {
	if len(call.Args) != 1 {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_config", "Connector Step factory requires one static config literal", call)
		return goConnectorFactoryStep{}, false
	}
	config, ok := connectorCompositeLiteral(call.Args[0])
	if !ok {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_config", "Connector Step factory config must be an inline Connector SDK config literal", call.Args[0])
		return goConnectorFactoryStep{}, false
	}
	configMetadata, operationSpecific := connectorFactoryConfig(analyzer.typeInfo.Types[config].Type)
	generic := analyzer.isConnectorConfigType(config, kind)
	if (!operationSpecific || configMetadata.kind != kind) && !generic {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_config", "Connector Step factory config must be an inline Connector SDK config literal", call.Args[0])
		return goConnectorFactoryStep{}, false
	}
	fields, ok := analyzer.connectorCompositeFields(config, "Connector Step factory config")
	if !ok {
		return goConnectorFactoryStep{}, false
	}
	fieldName := func(metadataName string, fallback string) string {
		if operationSpecific && configMetadata.fieldNames[metadataName] != "" {
			return configMetadata.fieldNames[metadataName]
		}
		return fallback
	}
	stepTypeExpression := fields[fieldName("stepType", "StepType")]
	stepType, static := analyzer.staticString(stepTypeExpression)
	if !static || stepType == "" {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_step_type", "Connector factory StepType must be a non-empty compile-time string", stepTypeExpression)
		return goConnectorFactoryStep{}, false
	}
	definition := goConnectorFactoryStep{stepType: stepType}
	definition.annotations = analyzer.parseConnectorAnnotations(fields[fieldName("annotations", "Annotations")])
	if operationSpecific {
		definition.branches = analyzer.parseConnectorNamedBranches(fields, configMetadata.branches)
	} else {
		definition.branches = analyzer.parseConnectorBranches(fields["Branches"])
	}
	definition.resultAttributeID = analyzer.parseConnectorResource(fields[fieldName("resultAttribute", "ResultAttribute")], "attribute", "ResultAttribute")
	definition.progressStreamID = analyzer.parseConnectorResource(fields[fieldName("progressStream", "ProgressStream")], "stream", "ProgressStream")
	definition.textStreamID = analyzer.parseConnectorResource(fields[fieldName("textStream", "TextStream")], "stream", "TextStream")
	definition.executeFailureTarget = analyzer.parseConnectorExecuteFailure(fields[fieldName("stepOptionsOverride", "StepOptionsOverride")])
	definition.connector = analyzer.parseConnectorIdentity(configMetadata, fields, call, kind, operationSpecific)
	return definition, true
}

func (analyzer *goAnalyzer) parseConnectorIdentity(
	config goConnectorFactoryConfig,
	fields map[string]ast.Expr,
	call *ast.CallExpr,
	kind string,
	operationSpecific bool,
) *goConnectorIdentity {
	if !operationSpecific || config.connectorID == "" || config.operationID == "" {
		analyzer.addConnectorConfigurationDiagnostic(
			"connector_configuration_unsupported",
			"Generic Connector factories cannot be configured automatically in Dex Web",
			call,
		)
		return nil
	}
	identity := &goConnectorIdentity{
		connectorID: config.connectorID, operationID: config.operationID, operationKind: kind,
	}
	identity.configurationUI = analyzer.parseConnectorConfigurationUI(fields[config.fieldNames["configurationUI"]])
	connectionNameExpression := fields[config.fieldNames["connectionName"]]
	connectionName, isStatic := analyzer.staticString(connectionNameExpression)
	if !isStatic || connectionName == "" {
		analyzer.addConnectorConfigurationDiagnostic(
			"connector_connection_name_required",
			"Connector Step requires a non-empty compile-time ConnectionName for automatic configuration",
			call,
		)
	} else {
		identity.connectionName = connectionName
	}
	module, found := analyzer.modules[config.packagePath]
	if !found || !officialConnectorModulePattern.MatchString(module.path) ||
		!connectorReleaseVersionPattern.MatchString(module.version) || module.replaced {
		analyzer.addConnectorConfigurationDiagnostic(
			"connector_release_required",
			"Connector Step requires an exact official published module version without a local replacement for automatic configuration",
			call,
		)
	} else {
		identity.modulePath = module.path
		identity.moduleVersion = module.version
	}
	identity.configurationEnabled = identity.connectionName != "" && identity.modulePath != "" && identity.moduleVersion != ""
	return identity
}

func connectorFactoryConfig(value types.Type) (goConnectorFactoryConfig, bool) {
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	named, ok := value.(*types.Named)
	if !ok {
		return goConnectorFactoryConfig{}, false
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok {
		return goConnectorFactoryConfig{}, false
	}
	config := goConnectorFactoryConfig{fieldNames: make(map[string]string)}
	if named.Obj() != nil && named.Obj().Pkg() != nil {
		config.packagePath = named.Obj().Pkg().Path()
	}
	valid := true
	branchIDs := make(map[string]bool)
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		metadata := reflect.StructTag(structure.Tag(index)).Get("connector")
		if metadata == "" {
			continue
		}
		key, value, found := strings.Cut(metadata, "=")
		switch key {
		case "factory":
			packagePath, typeName := namedGoTypeIdentity(field.Type())
			if packagePath != connectorSDKPackage || !field.Embedded() {
				valid = false
				continue
			}
			markerKind := ""
			if value == connectorQueryFactory && typeName == "QueryFactoryConfigMarker" {
				markerKind = connectorQueryFactory
			}
			if value == connectorMutationFactory && typeName == "MutationFactoryConfigMarker" {
				markerKind = connectorMutationFactory
			}
			if markerKind == "" || config.kind != "" {
				valid = false
				continue
			}
			config.kind = markerKind
		case "branch":
			if !found || value == "" || branchIDs[value] {
				valid = false
				continue
			}
			branchIDs[value] = true
			config.branches = append(config.branches, goConnectorFactoryBranchField{id: value, fieldName: field.Name()})
		case "connectorId":
			if !found || value == "" || config.connectorID != "" {
				valid = false
				continue
			}
			config.connectorID = value
		case "operationId":
			if !found || value == "" || config.operationID != "" {
				valid = false
				continue
			}
			config.operationID = value
		default:
			if found || config.fieldNames[key] != "" {
				valid = false
				continue
			}
			config.fieldNames[key] = field.Name()
		}
	}
	return config, valid && config.kind != "" && len(config.branches) != 0 &&
		config.fieldNames["stepType"] != "" && config.fieldNames["annotations"] != ""
}

func isConnectorFactoryStepType(value types.Type, kind string) bool {
	packagePath, typeName := namedGoTypeIdentity(value)
	expected := "QueryStep"
	if kind == connectorMutationFactory {
		expected = "MutationStep"
	}
	return packagePath == connectorSDKPackage && typeName == expected
}

func (analyzer *goAnalyzer) isConnectorConfigType(literal *ast.CompositeLit, kind string) bool {
	typeAndValue, ok := analyzer.typeInfo.Types[literal]
	if !ok || typeAndValue.Type == nil {
		return false
	}
	packagePath, typeName := namedGoTypeIdentity(typeAndValue.Type)
	expectedType := "QueryStepConfig"
	if kind == connectorMutationFactory {
		expectedType = "MutationStepConfig"
	}
	return packagePath == connectorSDKPackage && typeName == expectedType
}

func (analyzer *goAnalyzer) parseConnectorAnnotations(expression ast.Expr) goConnectorAnnotations {
	literal, ok := connectorCompositeLiteral(expression)
	if !ok {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_annotations", "Connector factory Annotations must be a static literal", expression)
		return goConnectorAnnotations{}
	}
	fields, ok := analyzer.connectorCompositeFields(literal, "Connector factory Annotations")
	if !ok {
		return goConnectorAnnotations{}
	}
	groupID, groupIDStatic := analyzer.staticString(fields["GroupID"])
	groupLabel, groupLabelStatic := analyzer.staticString(fields["GroupLabel"])
	explanation, explanationStatic := analyzer.staticString(fields["Explanation"])
	if !groupIDStatic || !groupLabelStatic || !explanationStatic || groupID == "" || groupLabel == "" || explanation == "" {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_annotations", "Connector factory annotation fields must be non-empty compile-time strings", expression)
	}
	return goConnectorAnnotations{groupID: groupID, groupLabel: groupLabel, explanation: explanation}
}

func (analyzer *goAnalyzer) parseConnectorBranches(expression ast.Expr) []goConnectorBranch {
	literal, ok := connectorCompositeLiteral(expression)
	if !ok {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_branches", "Connector factory Branches must be a static slice literal", expression)
		return nil
	}
	if len(literal.Elts) == 0 {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_branches", "Connector factory Branches must not be empty", expression)
		return nil
	}
	branches := make([]goConnectorBranch, 0, len(literal.Elts))
	seen := make(map[string]bool, len(literal.Elts))
	for _, element := range literal.Elts {
		call, callOK := unwrappedExpression(element).(*ast.CallExpr)
		if !callOK {
			analyzer.addConnectorFactoryDiagnostic("connector_factory_branch", "Connector factory branch must directly call GoToBranch", element)
			continue
		}
		packagePath, functionName := analyzer.goCallIdentity(call)
		if packagePath != connectorSDKPackage || functionName != "GoToBranch" || len(call.Args) != 2 {
			analyzer.addConnectorFactoryDiagnostic("connector_factory_branch", "Connector factory branch must directly call Connector SDK GoToBranch", element)
			continue
		}
		branchID, static := analyzer.staticString(call.Args[0])
		if !static || branchID == "" {
			analyzer.addConnectorFactoryDiagnostic("connector_factory_branch", "Connector factory branch ID must be a non-empty compile-time string", call.Args[0])
			continue
		}
		if seen[branchID] {
			analyzer.addConnectorFactoryDiagnostic("connector_factory_branch", fmt.Sprintf("Connector factory branch %q is duplicated", branchID), call.Args[0])
			continue
		}
		seen[branchID] = true
		target, targetOK := analyzer.connectorBranchTarget(call.Args[1])
		if !targetOK {
			analyzer.addConnectorFactoryDiagnostic("connector_factory_target", fmt.Sprintf("Connector factory branch %q target must be a concrete Step or static StepRef", branchID), call.Args[1])
		}
		branches = append(branches, goConnectorBranch{id: branchID, target: target, span: analyzer.span(call)})
	}
	return branches
}

func (analyzer *goAnalyzer) parseConnectorNamedBranches(
	fields map[string]ast.Expr,
	branchFields []goConnectorFactoryBranchField,
) []goConnectorBranch {
	branches := make([]goConnectorBranch, 0, len(branchFields))
	for _, branchField := range branchFields {
		expression := fields[branchField.fieldName]
		call, ok := unwrappedExpression(expression).(*ast.CallExpr)
		if !ok {
			analyzer.addConnectorFactoryDiagnostic("connector_factory_branch", fmt.Sprintf("Connector factory branch %q must directly call Connector SDK GoTo", branchField.id), expression)
			continue
		}
		packagePath, functionName := analyzer.goCallIdentity(call)
		if packagePath != connectorSDKPackage || functionName != "GoTo" || len(call.Args) != 1 {
			analyzer.addConnectorFactoryDiagnostic("connector_factory_branch", fmt.Sprintf("Connector factory branch %q must directly call Connector SDK GoTo", branchField.id), expression)
			continue
		}
		target, targetOK := analyzer.connectorBranchTarget(call.Args[0])
		if !targetOK {
			analyzer.addConnectorFactoryDiagnostic("connector_factory_target", fmt.Sprintf("Connector factory branch %q target must be a concrete Step or static StepRef", branchField.id), call.Args[0])
		}
		branches = append(branches, goConnectorBranch{id: branchField.id, target: target, span: analyzer.span(call)})
	}
	return branches
}

func (analyzer *goAnalyzer) connectorBranchTarget(expression ast.Expr) (string, bool) {
	current := unwrappedExpression(expression)
	if call, ok := current.(*ast.CallExpr); ok {
		packagePath, functionName := analyzer.goCallIdentity(call)
		if packagePath != connectorSDKPackage || functionName != "StepRef" || len(call.Args) != 1 {
			return "", false
		}
		target, static := analyzer.staticString(call.Args[0])
		return target, static && target != ""
	}
	if _, ok := current.(*ast.CompositeLit); !ok {
		return "", false
	}
	target := analyzer.expressionTypeName(current)
	return target, target != ""
}

func (analyzer *goAnalyzer) parseConnectorResource(expression ast.Expr, expectedKind string, fieldName string) string {
	if expression == nil || isNilIdentifier(unwrappedExpression(expression)) {
		return ""
	}
	resourceID := analyzer.resourceForExpression(expression)
	if resourceID == "" || analyzer.node(resourceID).Kind != expectedKind {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_resource", fmt.Sprintf("Connector factory %s must reference a declared %s", fieldName, expectedKind), expression)
		return ""
	}
	return resourceID
}

func (analyzer *goAnalyzer) parseConnectorExecuteFailure(expression ast.Expr) string {
	if expression == nil || isNilIdentifier(unwrappedExpression(expression)) {
		return ""
	}
	literal, ok := connectorCompositeLiteral(expression)
	if !ok {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_step_options", "Connector factory StepOptionsOverride must be a static literal", expression)
		return ""
	}
	fields, ok := analyzer.connectorCompositeFields(literal, "Connector factory StepOptionsOverride")
	if !ok {
		return ""
	}
	failure := fields["ExecuteFailure"]
	if failure == nil || isNilIdentifier(unwrappedExpression(failure)) {
		return ""
	}
	call, ok := unwrappedExpression(failure).(*ast.CallExpr)
	if !ok {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_execute_failure", "Connector factory ExecuteFailure must directly call ProceedToOnExecuteFailure", failure)
		return ""
	}
	packagePath, functionName := analyzer.goCallIdentity(call)
	if packagePath != goSDKPackage || functionName != "ProceedToOnExecuteFailure" || len(call.Args) == 0 {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_execute_failure", "Connector factory ExecuteFailure must directly call Dex ProceedToOnExecuteFailure", failure)
		return ""
	}
	target, targetOK := analyzer.connectorBranchTarget(call.Args[0])
	if !targetOK {
		analyzer.addConnectorFactoryDiagnostic("connector_factory_execute_failure", "Connector factory Execute failure target must be a concrete Step or static StepRef", call.Args[0])
		return ""
	}
	return target
}

func (analyzer *goAnalyzer) connectorCompositeFields(literal *ast.CompositeLit, subject string) (map[string]ast.Expr, bool) {
	fields := make(map[string]ast.Expr, len(literal.Elts))
	valid := true
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			analyzer.addConnectorFactoryDiagnostic("connector_factory_config", subject+" must use named fields", element)
			valid = false
			continue
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if !ok || fields[key.Name] != nil {
			analyzer.addConnectorFactoryDiagnostic("connector_factory_config", subject+" contains an invalid or duplicate field", element)
			valid = false
			continue
		}
		fields[key.Name] = keyValue.Value
	}
	return fields, valid
}

func (analyzer *goAnalyzer) analyzeConnectorFactoryStep(nodeID string, factory goConnectorFactoryStep) {
	analyzer.graph.SetNodePhase(nodeID, "execute")
	for _, branch := range factory.branches {
		targetID := analyzer.resolveTransitionTarget(branch.target, branch.span)
		analyzer.graph.AddEdge(Edge{
			Kind: "transition", From: nodeID, To: targetID, Label: branch.id, Span: branch.span,
			Metadata: map[string]any{"connectorBranch": true},
		})
	}
	if factory.resultAttributeID != "" {
		analyzer.graph.AddEdge(Edge{
			Kind: "resource_write", From: nodeID, To: factory.resultAttributeID, Label: "Set",
			Metadata: map[string]any{"phase": "execute"},
		})
	}
	analyzer.addConnectorProgressEdge(nodeID, factory.progressStreamID, "structured")
	analyzer.addConnectorProgressEdge(nodeID, factory.textStreamID, "text")
	if factory.executeFailureTarget != "" {
		targetID := analyzer.resolveTransitionTarget(factory.executeFailureTarget, nil)
		analyzer.graph.AddEdge(Edge{
			Kind: "failure_transition", From: nodeID, To: targetID, Label: "Execute failure",
			Metadata: map[string]any{"skipWaitFor": analyzer.connectorTargetSkipsWaitFor(factory.executeFailureTarget)},
		})
	}
}

func (analyzer *goAnalyzer) addConnectorProgressEdge(nodeID string, streamID string, format string) {
	if streamID == "" {
		return
	}
	analyzer.graph.AddEdge(Edge{
		Kind: "resource_write", From: nodeID, To: streamID, Label: "Write",
		Metadata: map[string]any{
			"phase": "execute", "bestEffort": true, "repeatable": true, "role": "progress", "format": format,
		},
	})
}

func (analyzer *goAnalyzer) connectorTargetSkipsWaitFor(target string) bool {
	if _, factory := analyzer.connectorFactories[target]; factory {
		return true
	}
	return analyzer.methods[target]["WaitFor"] == nil
}

func (analyzer *goAnalyzer) addConnectorFactoryDiagnostic(code string, message string, node ast.Node) {
	analyzer.graph.AddDiagnostic("error", code, message, analyzer.span(node))
}

func (analyzer *goAnalyzer) addConnectorConfigurationDiagnostic(code string, message string, node ast.Node) {
	analyzer.graph.AddDiagnostic("warning", code, message, analyzer.span(node))
}

func (analyzer *goAnalyzer) goCallIdentity(call *ast.CallExpr) (string, string) {
	function := unwrapCallFun(call.Fun)
	var object types.Object
	switch current := function.(type) {
	case *ast.Ident:
		object = analyzer.typeInfo.Uses[current]
		if object == nil {
			object = analyzer.typeInfo.Defs[current]
		}
	case *ast.SelectorExpr:
		object = analyzer.typeInfo.Uses[current.Sel]
	}
	if object == nil || object.Pkg() == nil {
		return "", ""
	}
	return object.Pkg().Path(), object.Name()
}

func connectorCompositeLiteral(expression ast.Expr) (*ast.CompositeLit, bool) {
	literal, ok := unwrappedExpression(expression).(*ast.CompositeLit)
	return literal, ok
}

func unwrappedExpression(expression ast.Expr) ast.Expr {
	for {
		switch current := expression.(type) {
		case *ast.ParenExpr:
			expression = current.X
		case *ast.UnaryExpr:
			expression = current.X
		default:
			return expression
		}
	}
}

func namedGoTypeIdentity(value types.Type) (string, string) {
	value = types.Unalias(value)
	for {
		switch current := value.(type) {
		case *types.Pointer:
			value = types.Unalias(current.Elem())
		case *types.Named:
			if current.Obj() == nil || current.Obj().Pkg() == nil {
				return "", ""
			}
			return current.Obj().Pkg().Path(), current.Obj().Name()
		default:
			return "", ""
		}
	}
}
