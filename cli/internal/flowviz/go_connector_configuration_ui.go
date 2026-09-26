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
)

func (analyzer *goAnalyzer) parseConnectorConfigurationUI(expression ast.Expr) ConnectorConfigurationUI {
	if expression == nil {
		return ConnectorConfigurationUI{Units: []ConnectorUIUnit{}}
	}
	literal, ok := connectorCompositeLiteral(expression)
	if !ok || !isNamedConnectorSDKType(analyzer, literal, "ConnectorConfigurationUI") {
		analyzer.addConnectorConfigurationDiagnostic("connector_configuration_ui", "Connector ConfigurationUI must be a static SDK literal", expression)
		return ConnectorConfigurationUI{Units: []ConnectorUIUnit{}}
	}
	fields, ok := analyzer.connectorCompositeFields(literal, "Connector ConfigurationUI")
	if !ok {
		return ConnectorConfigurationUI{Units: []ConnectorUIUnit{}}
	}
	unitsLiteral, ok := connectorCompositeLiteral(fields["Units"])
	if !ok {
		analyzer.addConnectorConfigurationDiagnostic("connector_configuration_ui", "Connector ConfigurationUI Units must be a static slice literal", expression)
		return ConnectorConfigurationUI{Units: []ConnectorUIUnit{}}
	}
	configuration := ConnectorConfigurationUI{Units: make([]ConnectorUIUnit, 0, len(unitsLiteral.Elts))}
	for _, element := range unitsLiteral.Elts {
		unit, valid := analyzer.parseConnectorUIUnit(element)
		if valid {
			configuration.Units = append(configuration.Units, unit)
		}
	}
	return configuration
}

func (analyzer *goAnalyzer) parseConnectorUIUnit(expression ast.Expr) (ConnectorUIUnit, bool) {
	literal, ok := connectorCompositeLiteral(expression)
	if !ok || !isNamedConnectorSDKType(analyzer, literal, "ConnectorUIUnit") {
		analyzer.addConnectorConfigurationDiagnostic("connector_configuration_ui", "Connector UI unit must be a static SDK literal", expression)
		return ConnectorUIUnit{}, false
	}
	fields, ok := analyzer.connectorCompositeFields(literal, "Connector UI unit")
	if !ok {
		return ConnectorUIUnit{}, false
	}
	unit := ConnectorUIUnit{}
	var static bool
	unit.ID, static = analyzer.staticString(fields["ID"])
	if !static {
		return analyzer.invalidConnectorUIUnit(expression)
	}
	unit.UnitID, static = analyzer.staticString(fields["UnitID"])
	if !static {
		return analyzer.invalidConnectorUIUnit(expression)
	}
	unit.Label, static = analyzer.staticString(fields["Label"])
	if !static {
		return analyzer.invalidConnectorUIUnit(expression)
	}
	if fields["Description"] != nil {
		unit.Description, static = analyzer.staticString(fields["Description"])
		if !static {
			return analyzer.invalidConnectorUIUnit(expression)
		}
	}
	if fields["Required"] != nil {
		value, valueStatic := analyzer.staticV2Scalar(fields["Required"])
		unit.Required, ok = value.(bool)
		if !valueStatic || !ok {
			return analyzer.invalidConnectorUIUnit(expression)
		}
	}
	bindingsLiteral, ok := connectorCompositeLiteral(fields["Bindings"])
	if !ok {
		return analyzer.invalidConnectorUIUnit(expression)
	}
	unit.Bindings = make([]ConnectorUIBinding, 0, len(bindingsLiteral.Elts))
	for _, element := range bindingsLiteral.Elts {
		binding, valid := analyzer.parseConnectorUIBinding(element)
		if !valid {
			return ConnectorUIUnit{}, false
		}
		unit.Bindings = append(unit.Bindings, binding)
	}
	return unit, true
}

func (analyzer *goAnalyzer) invalidConnectorUIUnit(expression ast.Expr) (ConnectorUIUnit, bool) {
	analyzer.addConnectorConfigurationDiagnostic("connector_configuration_ui", "Connector UI unit fields must be compile-time literals", expression)
	return ConnectorUIUnit{}, false
}

func (analyzer *goAnalyzer) parseConnectorUIBinding(expression ast.Expr) (ConnectorUIBinding, bool) {
	literal, ok := connectorCompositeLiteral(expression)
	if !ok || !isNamedConnectorSDKType(analyzer, literal, "ConnectorUIBinding") {
		analyzer.addConnectorConfigurationDiagnostic("connector_configuration_ui", "Connector UI binding must be a static SDK literal", expression)
		return ConnectorUIBinding{}, false
	}
	fields, ok := analyzer.connectorCompositeFields(literal, "Connector UI binding")
	if !ok {
		return ConnectorUIBinding{}, false
	}
	port, portStatic := analyzer.staticString(fields["Port"])
	jsonPointer, pointerStatic := analyzer.staticString(fields["JSONPointer"])
	if !portStatic || !pointerStatic {
		analyzer.addConnectorConfigurationDiagnostic("connector_configuration_ui", "Connector UI binding fields must be compile-time strings", expression)
		return ConnectorUIBinding{}, false
	}
	return ConnectorUIBinding{Port: port, JSONPointer: jsonPointer}, true
}

func isNamedConnectorSDKType(analyzer *goAnalyzer, expression ast.Expr, typeName string) bool {
	typeAndValue, ok := analyzer.typeInfo.Types[expression]
	if !ok || typeAndValue.Type == nil {
		return false
	}
	packagePath, actualTypeName := namedGoTypeIdentity(typeAndValue.Type)
	return packagePath == connectorSDKPackage && actualTypeName == typeName
}
