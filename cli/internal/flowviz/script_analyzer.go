// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

type scriptAnalysis struct {
	Graph       Graph                   `json:"graph"`
	Annotations []scriptStepAnnotations `json:"annotations"`
	Factories   []scriptFactoryUse      `json:"factories"`
}

type scriptStepAnnotations struct {
	StepID      string   `json:"stepId"`
	Directives  []string `json:"directives"`
	Span        *Span    `json:"span"`
	GroupID     string   `json:"groupId"`
	GroupLabel  string   `json:"groupLabel"`
	Explanation string   `json:"explanation"`
}

type scriptFactoryUse struct {
	StepID       string                 `json:"stepId"`
	ManifestPath string                 `json:"manifestPath"`
	FactoryName  string                 `json:"factoryName"`
	Fields       map[string]scriptValue `json:"fields"`
	Span         *Span                  `json:"span"`
}

type scriptValue struct {
	Literal   json.RawMessage `json:"literal"`
	Target    string          `json:"target"`
	Resource  string          `json:"resource"`
	Call      string          `json:"call"`
	Arguments []scriptValue   `json:"arguments"`
}

type connectorPackageIdentity struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Version   string `json:"version"`
}

type connectorPackageManifest struct {
	Schema      string                     `json:"$schema,omitempty"`
	ConnectorID string                     `json:"connectorId"`
	ReleaseTag  string                     `json:"releaseTag"`
	Package     connectorPackageIdentity   `json:"package"`
	Factories   []connectorManifestFactory `json:"factories"`
}

type connectorManifestFactory struct {
	Name            string                    `json:"name"`
	Kind            string                    `json:"kind"`
	OperationID     string                    `json:"operationId,omitempty"`
	TriggerName     string                    `json:"triggerName,omitempty"`
	Fields          map[string]string         `json:"fields"`
	Branches        []connectorManifestBranch `json:"branches,omitempty"`
	ConfigurationUI ConnectorConfigurationUI  `json:"configurationUI"`
}

type connectorManifestBranch struct {
	ID       string `json:"id"`
	Field    string `json:"field"`
	Optional bool   `json:"optional,omitempty"`
}

var scriptConnectorIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)
var scriptConnectorReleaseTagPattern = regexp.MustCompile(`^connectors/[a-z0-9][a-z0-9-]*(?:/[a-z0-9][a-z0-9-]*)*/v[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?$`)
var scriptConnectorPackageVersionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+.a-zA-Z0-9]*)$`)

func (analysis *scriptAnalysis) definition(schemaVersion string) *Graph {
	graph := &analysis.Graph
	graph.SchemaVersion = schemaVersion
	for _, use := range analysis.Factories {
		analysis.applyConnectorFactory(use)
	}
	if schemaVersion == SchemaVersionV2 {
		analysis.applyStepAnnotations()
	}
	return graph
}

func (analysis *scriptAnalysis) applyConnectorFactory(use scriptFactoryUse) {
	manifest, err := readConnectorPackageManifest(use.ManifestPath, analysis.Graph.Source.Language)
	if err != nil {
		analysis.Graph.AddDiagnostic("error", "connector_manifest", err.Error(), use.Span)
		return
	}
	var factory *connectorManifestFactory
	for index := range manifest.Factories {
		if manifest.Factories[index].Name == use.FactoryName {
			factory = &manifest.Factories[index]
			break
		}
	}
	if factory == nil {
		analysis.Graph.AddDiagnostic("error", "connector_factory", "Connector manifest does not export factory "+use.FactoryName, use.Span)
		return
	}
	connectionName := use.Fields[factory.Fields["connectionName"]].stringValue()
	configuration := factory.ConfigurationUI
	if field, found := use.Fields[factory.Fields["configurationUI"]]; found {
		if err := json.Unmarshal(field.Literal, &configuration); err != nil {
			analysis.Graph.AddDiagnostic("warning", "connector_configuration_ui", "Connector ConfigurationUI must be a static literal", use.Span)
		}
	}
	if configuration.Units == nil {
		configuration.Units = []ConnectorUIUnit{}
	}
	if err := validateScriptConnectorConfigurationUI(configuration); err != nil {
		analysis.Graph.AddDiagnostic("warning", "connector_configuration_ui", err.Error(), use.Span)
		configuration = ConnectorConfigurationUI{Units: []ConnectorUIUnit{}}
	}
	identity := map[string]any{
		"connectorId": manifest.ConnectorID, "releaseTag": manifest.ReleaseTag, "package": manifest.Package,
		"connectionName": connectionName, "configurationEnabled": connectionName != "", "configurationUI": configuration,
	}
	if connectionName == "" {
		analysis.Graph.AddDiagnostic("warning", "connector_connection_name_required", "Connector factory requires a non-empty static connection name for automatic configuration", use.Span)
	}
	if factory.Kind == "trigger" {
		if analysis.Graph.SchemaVersion != SchemaVersionV2 {
			return
		}
		bindingName := use.Fields[factory.Fields["bindingName"]].stringValue()
		if bindingName == "" {
			analysis.Graph.AddDiagnostic("warning", "connector_trigger_binding_identity", "Connector Trigger binding requires a static binding name", use.Span)
		}
		if analysis.Graph.V2 == nil {
			analysis.Graph.V2 = emptyScriptV2Definition()
		}
		analysis.Graph.V2.ConnectorTriggerBindings = append(analysis.Graph.V2.ConnectorTriggerBindings, ConnectorTriggerBinding{
			ConnectorID: manifest.ConnectorID, TriggerName: factory.TriggerName, ConnectionName: connectionName, BindingName: bindingName,
			ReleaseTag: manifest.ReleaseTag, Package: &manifest.Package, ConfigurationUI: configuration,
			ConfigurationEnabled: connectionName != "" && bindingName != "",
		})
		return
	}
	identity["operationId"] = factory.OperationID
	identity["operationKind"] = factory.Kind
	for index := range analysis.Graph.Nodes {
		node := &analysis.Graph.Nodes[index]
		if node.ID != use.StepID {
			continue
		}
		node.Metadata = map[string]any{"connectorFactory": true, "connectorOperationKind": factory.Kind, "connector": identity}
		var annotations struct {
			GroupID     string `json:"groupId"`
			GroupLabel  string `json:"groupLabel"`
			Explanation string `json:"explanation"`
		}
		if err := json.Unmarshal(use.Fields[factory.Fields["annotations"]].Literal, &annotations); err != nil {
			analysis.Graph.AddDiagnostic("error", "connector_factory_annotations", "Connector annotations must be a static object with groupId, groupLabel, and explanation", use.Span)
		}
		analysis.Annotations = append(analysis.Annotations, scriptStepAnnotations{
			StepID: node.ID, GroupID: annotations.GroupID, GroupLabel: annotations.GroupLabel,
			Explanation: annotations.Explanation, Span: use.Span,
		})
		break
	}
	for _, branch := range factory.Branches {
		value, found := use.Fields[branch.Field]
		if !found && branch.Optional {
			analysis.Graph.AddNode(Node{ID: "decision:" + use.StepID + ":optional:" + branch.ID, Kind: "decision", Name: "forceFail", ParentID: use.StepID, Condition: branch.ID, Phase: "execute", Decision: &DecisionDetails{Type: "forceFail"}})
			continue
		}
		target := value.Target
		if value.Call != "" && len(value.Arguments) == 1 {
			target = value.Arguments[0].Target
		}
		if !analysis.hasNode(target, "step") {
			analysis.Graph.AddDiagnostic("error", "connector_factory_target", fmt.Sprintf("Connector branch %q must target a registered static Step", branch.ID), use.Span)
			continue
		}
		analysis.Graph.AddEdge(Edge{Kind: "transition", From: use.StepID, To: target, Label: branch.ID, Span: use.Span, Metadata: map[string]any{"connectorBranch": true}})
	}
	if failure, found := use.Fields[factory.Fields["executeFailure"]]; found {
		target := failure.Target
		if len(failure.Arguments) == 1 {
			target = failure.Arguments[0].Target
		}
		if !analysis.hasNode(target, "step") {
			analysis.Graph.AddDiagnostic("error", "connector_factory_execute_failure", "Connector Execute failure must target a registered static Step", use.Span)
		} else {
			hasWaitFor := false
			for _, node := range analysis.Graph.Nodes {
				if node.ID == target {
					hasWaitFor = strings.Contains(node.Phase, "wait_for")
				}
			}
			analysis.Graph.AddEdge(Edge{Kind: "failure_transition", From: use.StepID, To: target, Label: "Execute failure", Metadata: map[string]any{"skipWaitFor": !hasWaitFor}})
		}
	}
	for _, field := range []string{"resultAttribute", "resultStream", "progressStream", "textStream"} {
		value, found := use.Fields[factory.Fields[field]]
		if !found {
			continue
		}
		kind := "stream"
		label := "Write"
		metadata := map[string]any{"phase": "execute"}
		if field == "resultAttribute" {
			kind, label = "attribute", "Set"
		} else {
			metadata["bestEffort"], metadata["repeatable"] = true, true
			metadata["role"], metadata["format"] = "progress", "structured"
			if field == "textStream" {
				metadata["format"] = "text"
			}
			if field == "resultStream" {
				metadata["role"] = "result"
			}
		}
		if !analysis.hasNode(value.Resource, kind) {
			analysis.Graph.AddDiagnostic("error", "connector_factory_resource", "Connector "+field+" must reference a declared "+kind, use.Span)
			continue
		}
		analysis.Graph.AddEdge(Edge{Kind: "resource_write", From: use.StepID, To: value.Resource, Label: label, Metadata: metadata})
	}
}

func (analysis *scriptAnalysis) applyStepAnnotations() {
	analysis.Graph.Groups = []StepGroup{}
	if analysis.Graph.V2 == nil {
		analysis.Graph.V2 = emptyScriptV2Definition()
	}
	for _, annotations := range analysis.Annotations {
		if len(annotations.Directives) > 0 || annotations.GroupID == "" {
			annotations = analysis.parseStepDirectives(annotations)
		}
		if !v2GroupIDPattern.MatchString(annotations.GroupID) || strings.TrimSpace(annotations.GroupLabel) == "" {
			analysis.Graph.AddDiagnostic("error", "v2_step_group", "Step "+annotations.StepID+" requires one dex:group with a kebab-case group-id and non-empty group-label", annotations.Span)
		} else {
			groupIndex := -1
			for index, group := range analysis.Graph.Groups {
				if group.ID == annotations.GroupID {
					groupIndex = index
					break
				}
			}
			if groupIndex < 0 {
				analysis.Graph.Groups = append(analysis.Graph.Groups, StepGroup{ID: annotations.GroupID, Label: annotations.GroupLabel, StepIDs: []string{annotations.StepID}})
			} else if analysis.Graph.Groups[groupIndex].Label != annotations.GroupLabel {
				analysis.Graph.AddDiagnostic("error", "v2_step_group", "group "+annotations.GroupID+" uses conflicting labels", annotations.Span)
			} else {
				analysis.Graph.Groups[groupIndex].StepIDs = append(analysis.Graph.Groups[groupIndex].StepIDs, annotations.StepID)
			}
		}
		if strings.TrimSpace(annotations.Explanation) == "" {
			analysis.Graph.AddDiagnostic("error", "v2_step_explanation", "Step "+annotations.StepID+" must declare exactly one non-empty dex:explanation directive", annotations.Span)
			continue
		}
		for index := range analysis.Graph.Nodes {
			node := &analysis.Graph.Nodes[index]
			if node.ID == annotations.StepID {
				if node.Metadata == nil {
					node.Metadata = map[string]any{}
				}
				node.Metadata["explanation"] = strings.TrimSpace(annotations.Explanation)
			}
		}
	}
}

func (analysis *scriptAnalysis) parseStepDirectives(annotations scriptStepAnnotations) scriptStepAnnotations {
	counts := map[string]int{}
	for _, text := range annotations.Directives {
		directive, err := parseV2Directive(text, nil)
		if err != nil {
			analysis.Graph.AddDiagnostic("error", "v2_directive", err.Error(), annotations.Span)
			continue
		}
		counts[directive.name]++
		required := []string{}
		switch directive.name {
		case "group":
			required = []string{"group-id", "group-label"}
		case "explanation":
			required = []string{"text"}
		default:
			analysis.Graph.AddDiagnostic("error", "v2_directive", "unsupported Step directive dex:"+directive.name, annotations.Span)
			continue
		}
		isValid := len(directive.arguments) == len(required)
		for _, name := range required {
			argument, found := directive.arguments[name]
			isValid = isValid && found && !argument.array
		}
		if !isValid {
			analysis.Graph.AddDiagnostic("error", "v2_directive", "dex:"+directive.name+" requires "+strings.Join(required, ", "), annotations.Span)
			continue
		}
		if directive.name == "group" {
			annotations.GroupID, annotations.GroupLabel = directive.arguments["group-id"].text, directive.arguments["group-label"].text
		} else {
			annotations.Explanation = directive.arguments["text"].text
		}
	}
	if counts["group"] != 1 {
		annotations.GroupID = ""
	}
	if counts["explanation"] != 1 {
		annotations.Explanation = ""
	}
	return annotations
}

func readConnectorPackageManifest(path string, language string) (connectorPackageManifest, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return connectorPackageManifest{}, fmt.Errorf("read Connector manifest: %w", err)
	}
	var manifest connectorPackageManifest
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("decode Connector manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return manifest, fmt.Errorf("Connector manifest must contain one JSON object")
	}
	expectedEcosystem := "pypi"
	if language == "typescript" {
		expectedEcosystem = "npm"
	}
	if !scriptConnectorIDPattern.MatchString(manifest.ConnectorID) || !scriptConnectorReleaseTagPattern.MatchString(manifest.ReleaseTag) ||
		manifest.Package.Ecosystem != expectedEcosystem || strings.TrimSpace(manifest.Package.Name) == "" || !scriptConnectorPackageVersionPattern.MatchString(manifest.Package.Version) || len(manifest.Factories) == 0 {
		return manifest, fmt.Errorf("Connector manifest requires an exact releaseTag and %s package identity", expectedEcosystem)
	}
	names := map[string]bool{}
	for _, factory := range manifest.Factories {
		if factory.Name == "" || names[factory.Name] || factory.Fields["connectionName"] == "" {
			return manifest, fmt.Errorf("Connector manifest factory names and connectionName fields must be unique and non-empty")
		}
		names[factory.Name] = true
		usedFields := map[string]bool{}
		for logicalName, fieldName := range factory.Fields {
			if !containsString([]string{"stepType", "connectionName", "annotations", "bindingName", "configurationUI", "resultAttribute", "resultStream", "progressStream", "textStream", "executeFailure"}, logicalName) || strings.TrimSpace(fieldName) == "" || usedFields[fieldName] {
				return manifest, fmt.Errorf("Connector manifest factory fields must map supported names to distinct non-empty fields")
			}
			usedFields[fieldName] = true
		}
		if err := validateScriptConnectorConfigurationUI(factory.ConfigurationUI); err != nil {
			return manifest, err
		}
		if factory.Kind == "trigger" {
			if factory.TriggerName == "" || factory.Fields["bindingName"] == "" {
				return manifest, fmt.Errorf("Connector Trigger factory requires triggerName and bindingName")
			}
			continue
		}
		if (factory.Kind != "query" && factory.Kind != "mutation") || factory.OperationID == "" || factory.Fields["stepType"] == "" || factory.Fields["annotations"] == "" || len(factory.Branches) == 0 {
			return manifest, fmt.Errorf("Connector Step factory requires kind, operationId, stepType, annotations, and branches")
		}
		branchIDs := map[string]bool{}
		for _, branch := range factory.Branches {
			if branch.ID == "" || branch.Field == "" || branchIDs[branch.ID] {
				return manifest, fmt.Errorf("Connector factory branches require unique IDs and non-empty fields")
			}
			branchIDs[branch.ID] = true
		}
	}
	return manifest, nil
}

func validateScriptConnectorConfigurationUI(configuration ConnectorConfigurationUI) error {
	unitIDs := map[string]bool{}
	for _, unit := range configuration.Units {
		if unit.ID == "" || unit.UnitID == "" || strings.TrimSpace(unit.Label) == "" || unitIDs[unit.ID] {
			return fmt.Errorf("Connector ConfigurationUI units require unique IDs, unitId, and label")
		}
		unitIDs[unit.ID] = true
		ports := map[string]bool{}
		for _, binding := range unit.Bindings {
			if binding.Port == "" || ports[binding.Port] || (binding.JSONPointer != "" && !strings.HasPrefix(binding.JSONPointer, "/")) {
				return fmt.Errorf("Connector ConfigurationUI bindings require unique ports and JSON pointers")
			}
			ports[binding.Port] = true
		}
	}
	return nil
}

func emptyScriptV2Definition() *V2Definition {
	return &V2Definition{IndexedAttributes: []IndexedAttribute{}, Summary: RPCView{RPCName: "GetDexSummary", Fields: []ViewField{}}, Display: RPCView{RPCName: "GetDexDisplay", Fields: []ViewField{}}, Actions: []Action{}}
}

func (analysis *scriptAnalysis) hasNode(id string, kind string) bool {
	for _, node := range analysis.Graph.Nodes {
		if node.ID == id && node.Kind == kind {
			return true
		}
	}
	return false
}

func (value scriptValue) stringValue() string {
	var text string
	if err := json.Unmarshal(value.Literal, &text); err != nil {
		return ""
	}
	return text
}
