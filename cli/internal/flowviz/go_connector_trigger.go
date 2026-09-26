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
	"reflect"
	"sort"
	"strings"
)

type goConnectorTriggerBindingConfig struct {
	connectorID string
	triggerName string
	packagePath string
	fieldNames  map[string]string
}

func (analyzer *goAnalyzer) collectConnectorTriggerBindings(flowType string) []ConnectorTriggerBinding {
	method := analyzer.methods[flowType]["GetConnectorTriggerBindings"]
	if method == nil {
		return nil
	}
	if method.Body == nil || len(method.Body.List) != 1 {
		analyzer.addConnectorConfigurationDiagnostic("connector_trigger_bindings_dynamic", "Connector Trigger bindings must be returned as one static slice literal", method)
		return nil
	}
	returnStatement, ok := method.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returnStatement.Results) != 1 {
		analyzer.addConnectorConfigurationDiagnostic("connector_trigger_bindings_dynamic", "Connector Trigger bindings must be returned as one static slice literal", method)
		return nil
	}
	literal, ok := unwrappedExpression(returnStatement.Results[0]).(*ast.CompositeLit)
	if !ok {
		analyzer.addConnectorConfigurationDiagnostic("connector_trigger_bindings_dynamic", "Connector Trigger bindings must be returned as one static slice literal", method)
		return nil
	}
	bindings := make([]ConnectorTriggerBinding, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		binding, found := analyzer.parseConnectorTriggerBinding(element)
		if found {
			bindings = append(bindings, binding)
		}
	}
	sort.Slice(bindings, func(left int, right int) bool {
		if bindings[left].ConnectorID != bindings[right].ConnectorID {
			return bindings[left].ConnectorID < bindings[right].ConnectorID
		}
		if bindings[left].ConnectionName != bindings[right].ConnectionName {
			return bindings[left].ConnectionName < bindings[right].ConnectionName
		}
		return bindings[left].BindingName < bindings[right].BindingName
	})
	return bindings
}

func (analyzer *goAnalyzer) parseConnectorTriggerBinding(expression ast.Expr) (ConnectorTriggerBinding, bool) {
	call, ok := unwrappedExpression(expression).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		analyzer.addConnectorConfigurationDiagnostic("connector_trigger_binding_dynamic", "Connector Trigger binding must call a generated definition with one inline config literal", expression)
		return ConnectorTriggerBinding{}, false
	}
	configLiteral, ok := connectorCompositeLiteral(call.Args[0])
	if !ok {
		analyzer.addConnectorConfigurationDiagnostic("connector_trigger_binding_dynamic", "Connector Trigger binding config must be an inline literal", call.Args[0])
		return ConnectorTriggerBinding{}, false
	}
	config, ok := connectorTriggerBindingConfig(analyzer.typeInfo.Types[configLiteral].Type)
	if !ok {
		analyzer.addConnectorConfigurationDiagnostic("connector_trigger_binding_invalid", "Connector Trigger binding must use a generated binding config", configLiteral)
		return ConnectorTriggerBinding{}, false
	}
	fields, ok := analyzer.connectorCompositeFields(configLiteral, "Connector Trigger binding config")
	if !ok {
		return ConnectorTriggerBinding{}, false
	}
	connectionName, hasConnectionName := analyzer.staticString(fields[config.fieldNames["connectionName"]])
	bindingName, hasBindingName := analyzer.staticString(fields[config.fieldNames["bindingName"]])
	binding := ConnectorTriggerBinding{
		ConnectorID: config.connectorID, TriggerName: config.triggerName,
		ConnectionName: connectionName, BindingName: bindingName,
		ConfigurationUI: analyzer.parseConnectorConfigurationUI(fields[config.fieldNames["configurationUI"]]),
	}
	if !hasConnectionName || connectionName == "" || !hasBindingName || bindingName == "" {
		analyzer.addConnectorConfigurationDiagnostic("connector_trigger_binding_identity", "Connector Trigger binding requires compile-time connection and binding names", call)
	}
	module, found := analyzer.modules[config.packagePath]
	if !found || !officialConnectorModulePattern.MatchString(module.path) ||
		!connectorReleaseVersionPattern.MatchString(module.version) || module.replaced {
		analyzer.addConnectorConfigurationDiagnostic("connector_trigger_release_required", "Connector Trigger binding requires an exact official published module version", call)
	} else {
		binding.ModulePath = module.path
		binding.ModuleVersion = module.version
	}
	binding.ConfigurationEnabled = binding.ConnectionName != "" && binding.BindingName != "" && binding.ModulePath != "" && binding.ModuleVersion != ""
	return binding, true
}

func connectorTriggerBindingConfig(value types.Type) (goConnectorTriggerBindingConfig, bool) {
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	named, ok := value.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return goConnectorTriggerBindingConfig{}, false
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok {
		return goConnectorTriggerBindingConfig{}, false
	}
	config := goConnectorTriggerBindingConfig{packagePath: named.Obj().Pkg().Path(), fieldNames: make(map[string]string)}
	valid := true
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
			valid = valid && found && value == "triggerBinding" && field.Embedded() && packagePath == connectorSDKPackage && typeName == "TriggerBindingFactoryConfigMarker"
		case "connectorId":
			valid = valid && found && value != "" && config.connectorID == ""
			config.connectorID = value
		case "triggerName":
			valid = valid && found && value != "" && config.triggerName == ""
			config.triggerName = value
		default:
			valid = valid && !found && config.fieldNames[key] == ""
			config.fieldNames[key] = field.Name()
		}
	}
	return config, valid && config.connectorID != "" && config.triggerName != "" &&
		config.fieldNames["connectionName"] != "" && config.fieldNames["bindingName"] != ""
}
