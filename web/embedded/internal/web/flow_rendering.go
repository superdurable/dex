// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/superdurable/dex/web/embedded/internal/web/api"
)

const maxFlowDefinitionBytes = 8 << 20

var flowDefinitionGroupIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var v2ActionPermissionPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)

var v2IndexKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

type flowDefinitionCatalog struct {
	Configured         bool             `json:"configured"`
	Directory          string           `json:"directory,omitempty"`
	Source             string           `json:"source,omitempty"`
	DefinitionRevision string           `json:"definitionRevision,omitempty"`
	DefinitionCount    int              `json:"definitionCount"`
	Definitions        []flowDefinition `json:"definitions"`
	// SourceErrors reports a source that failed as a whole, such as an unreachable Dex Server.
	SourceErrors []string `json:"sourceErrors,omitempty"`
}

type flowDefinitionHandler struct {
	response      []byte
	v2Definitions map[string]api.V2Definition
}

type flowDefinition struct {
	ID             string            `json:"id"`
	File           string            `json:"file"`
	SchemaVersion  string            `json:"schemaVersion"`
	FlowName       string            `json:"flowName"`
	SourceLanguage string            `json:"sourceLanguage"`
	SourcePath     string            `json:"sourcePath"`
	Valid          bool              `json:"valid"`
	Graph          json.RawMessage   `json:"graph,omitempty"`
	V2             *api.V2Definition `json:"v2,omitempty"`
	// Source is directory, uploaded, or visualize.
	Source      string     `json:"source"`
	Digest      string     `json:"digest,omitempty"`
	GeneratedAt *time.Time `json:"generatedAt,omitempty"`
	UploadedAt  *time.Time `json:"uploadedAt,omitempty"`
	// Error is set when Dex Web rejected this definition; it then has no graph.
	Error string `json:"error,omitempty"`
}

type flowDefinitionHeader struct {
	SchemaVersion string `json:"schemaVersion"`
	Valid         bool   `json:"valid"`
	Source        struct {
		Language string `json:"language"`
		Path     string `json:"path"`
	} `json:"source"`
	Flow struct {
		Name string `json:"name"`
	} `json:"flow"`
	Groups      []json.RawMessage `json:"groups"`
	V2          *api.V2Definition `json:"v2"`
	Nodes       []json.RawMessage `json:"nodes"`
	Edges       []json.RawMessage `json:"edges"`
	Diagnostics []json.RawMessage `json:"diagnostics"`
}

// loadFlowDefinitions loads a directory and fails on the first invalid definition, as a strict check.
func loadFlowDefinitions(directory string) (*flowDefinitionHandler, error) {
	provider, err := NewDirectoryFlowDefinitionProvider(directory)
	if err != nil {
		return nil, err
	}
	snapshot, err := provider.Load(context.Background())
	if err != nil {
		return nil, err
	}
	if err := firstFlowDefinitionError(snapshot); err != nil {
		return nil, err
	}
	return &flowDefinitionHandler{response: snapshot.Response, v2Definitions: snapshot.V2Definitions}, nil
}

func firstFlowDefinitionError(snapshot *FlowDefinitionSnapshot) error {
	for _, definition := range snapshot.definitions {
		if definition.Error != "" {
			return invalidDefinitionSource(errors.New(definition.Error))
		}
	}
	return nil
}

func flowDefinitionFromGraph(file string, graph []byte) (flowDefinition, error) {
	var header flowDefinitionHeader
	if err := json.Unmarshal(graph, &header); err != nil {
		return flowDefinition{}, fmt.Errorf("parse Flow Definition Graph %s: %w", file, err)
	}
	if header.SchemaVersion != "1.0" && header.SchemaVersion != "2.0" {
		return flowDefinition{}, fmt.Errorf("Flow Definition Graph %s has unsupported schemaVersion %q", file, header.SchemaVersion)
	}
	if header.Source.Language != "go" && header.Source.Language != "python" && header.Source.Language != "typescript" {
		return flowDefinition{}, fmt.Errorf("Flow Definition Graph %s has unsupported source language %q", file, header.Source.Language)
	}
	if header.Nodes == nil || header.Edges == nil || header.Diagnostics == nil {
		return flowDefinition{}, fmt.Errorf("Flow Definition Graph %s must contain nodes, edges, and diagnostics arrays", file)
	}
	if err := checkFlowDefinitionGraphLimits(header.Nodes, header.Edges); err != nil {
		return flowDefinition{}, fmt.Errorf("Flow Definition Graph %s is too large: %w", file, err)
	}
	if err := checkFlowDefinitionElements(header.Nodes, header.Edges, header.Diagnostics); err != nil {
		return flowDefinition{}, fmt.Errorf("Flow Definition Graph %s is malformed: %w", file, err)
	}
	if err := checkFlowDefinitionConnectorSteps(header.Nodes); err != nil {
		return flowDefinition{}, fmt.Errorf("Flow Definition Graph %s is too large: %w", file, err)
	}
	if header.SchemaVersion == "2.0" && (header.Groups == nil || header.V2 == nil) {
		return flowDefinition{}, fmt.Errorf("Flow Definition Graph 2.0 %s must contain groups and v2", file)
	}
	if header.SchemaVersion == "2.0" {
		if err := validateFlowDefinitionV2(file, graph, header); err != nil {
			return flowDefinition{}, err
		}
	}
	flowName := strings.TrimSpace(header.Flow.Name)
	if flowName == "" {
		flowName = strings.TrimSuffix(file, filepath.Ext(file))
	}
	return flowDefinition{
		ID:             file,
		File:           file,
		SchemaVersion:  header.SchemaVersion,
		FlowName:       flowName,
		SourceLanguage: header.Source.Language,
		SourcePath:     header.Source.Path,
		Valid:          header.Valid,
		Graph:          graph,
		V2:             header.V2,
	}, nil
}

func validateFlowDefinitionV2(file string, graph []byte, header flowDefinitionHeader) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(graph, &document); err != nil {
		return fmt.Errorf("parse Flow Definition Graph 2.0 %s: %w", file, err)
	}
	// Unknown top-level fields are ignored, so a newer analyzer can add fields without breaking older Dex Web.
	for _, field := range []string{"schemaVersion", "valid", "source", "flow", "nodes", "edges", "diagnostics", "groups", "v2"} {
		if _, exists := document[field]; !exists {
			return fmt.Errorf("Flow Definition Graph 2.0 %s is missing field %q", file, field)
		}
	}
	if generatedAt, exists := document["generatedAt"]; exists {
		if err := validateFlowDefinitionGeneratedAt(generatedAt); err != nil {
			return fmt.Errorf("Flow Definition Graph 2.0 %s has malformed generatedAt: %w", file, err)
		}
	}
	if err := validateFlowDefinitionObjectFields(document["source"], []string{"language", "path"}, []string{"language", "path"}); err != nil {
		return fmt.Errorf("Flow Definition Graph 2.0 %s has malformed source: %w", file, err)
	}
	if err := validateFlowDefinitionObjectFields(document["flow"], []string{"name", "startStepId", "span"}, []string{"name"}); err != nil {
		return fmt.Errorf("Flow Definition Graph 2.0 %s has malformed flow: %w", file, err)
	}
	type group struct {
		ID      string   `json:"id"`
		Label   string   `json:"label"`
		StepIDs []string `json:"stepIds"`
	}
	var groups []group
	if err := decodeStrictFlowDefinitionJSON(document["groups"], &groups); err != nil {
		return fmt.Errorf("Flow Definition Graph 2.0 %s has malformed groups: %w", file, err)
	}
	if len(groups) > maxFlowDefinitionGroups {
		return fmt.Errorf("Flow Definition Graph 2.0 %s is too large: it has %d groups; the limit is %d", file, len(groups), maxFlowDefinitionGroups)
	}
	groupIDs := make(map[string]bool, len(groups))
	stepIDs := make(map[string]bool)
	for _, group := range groups {
		if len(stepIDs)+len(group.StepIDs) > maxFlowDefinitionGroupedSteps {
			return fmt.Errorf("Flow Definition Graph 2.0 %s is too large: its groups name more than %d Steps", file, maxFlowDefinitionGroupedSteps)
		}
		if !flowDefinitionGroupIDPattern.MatchString(group.ID) || strings.TrimSpace(group.Label) == "" || len(group.StepIDs) == 0 {
			return fmt.Errorf("Flow Definition Graph 2.0 %s has an invalid group", file)
		}
		if groupIDs[group.ID] {
			return fmt.Errorf("Flow Definition Graph 2.0 %s repeats group %q", file, group.ID)
		}
		groupIDs[group.ID] = true
		for _, stepID := range group.StepIDs {
			if stepID == "" || stepIDs[stepID] {
				return fmt.Errorf("Flow Definition Graph 2.0 %s has an invalid or repeated grouped Step ID", file)
			}
			stepIDs[stepID] = true
		}
	}
	var definition api.V2Definition
	if err := decodeStrictFlowDefinitionJSON(document["v2"], &definition); err != nil {
		return fmt.Errorf("Flow Definition Graph 2.0 %s has malformed v2: %w", file, err)
	}
	if err := checkV2DefinitionItemCounts(definition); err != nil {
		return fmt.Errorf("Flow Definition Graph 2.0 %s is too large: %w", file, err)
	}
	if err := validateV2Definition(definition); err != nil {
		return fmt.Errorf("Flow Definition Graph 2.0 %s has invalid v2: %w", file, err)
	}
	rpcLocks, err := loadRPCLocks(header.Nodes, header.Edges)
	if err != nil {
		return fmt.Errorf("Flow Definition Graph 2.0 %s has invalid RPC locks: %w", file, err)
	}
	definition.RPCLocks = rpcLocks
	agentSession, err := loadAgentSession(header.Nodes, header.Source.Language)
	if err != nil {
		return fmt.Errorf("Flow Definition Graph 2.0 %s has invalid nodes: %w", file, err)
	}
	definition.AgentSession = agentSession
	if definition.Start != nil {
		if err := loadStartStepPhase(header.Nodes, definition.Start); err != nil {
			return fmt.Errorf("Flow Definition Graph 2.0 %s has invalid Start Step: %w", file, err)
		}
	}
	if header.V2 == nil {
		panic("validated v2 must be present")
	}
	*header.V2 = definition
	return nil
}

func loadStartStepPhase(nodes []json.RawMessage, start *api.V2StartDefinition) error {
	matched := false
	for _, raw := range nodes {
		var node struct {
			Kind  string `json:"kind"`
			Name  string `json:"name"`
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			return fmt.Errorf("malformed Step node: %w", err)
		}
		if node.Kind != "step" || node.Name != start.StepType {
			continue
		}
		if matched {
			return fmt.Errorf("repeated Start Step node %q", start.StepType)
		}
		matched = true
		switch node.Phase {
		case "execute":
			start.SkipWaitFor = true
		case "wait_for+execute":
			start.SkipWaitFor = false
		default:
			return fmt.Errorf("Start Step %q has unsupported phase %q", start.StepType, node.Phase)
		}
	}
	if !matched {
		return fmt.Errorf("Start Step %q is absent from graph nodes", start.StepType)
	}
	return nil
}

// loadRPCLocks reads each RPC's resource_lock edges into the lock keys InvokeRPC sends.
func loadRPCLocks(rawNodes []json.RawMessage, rawEdges []json.RawMessage) (map[string]api.V2RPCLocks, error) {
	type graphNode struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Resource *struct {
			Map bool `json:"map"`
		} `json:"resource"`
	}
	type graphEdge struct {
		Kind     string `json:"kind"`
		From     string `json:"from"`
		To       string `json:"to"`
		Metadata struct {
			RPCName string `json:"rpcName"`
		} `json:"metadata"`
	}
	nodes := make(map[string]graphNode, len(rawNodes))
	for _, raw := range rawNodes {
		var node graphNode
		if err := json.Unmarshal(raw, &node); err != nil {
			return nil, fmt.Errorf("malformed node: %w", err)
		}
		nodes[node.ID] = node
	}
	keysByRPC := make(map[string]map[string]bool)
	locks := make(map[string]api.V2RPCLocks)
	for _, raw := range rawEdges {
		var edge graphEdge
		if err := json.Unmarshal(raw, &edge); err != nil {
			return nil, fmt.Errorf("malformed edge: %w", err)
		}
		owner, isOwnerKnown := nodes[edge.From]
		if edge.Kind != "resource_lock" || !isOwnerKnown || owner.Kind != "rpc" {
			continue
		}
		// Go graphs record the protocol name, which RPCOptions.Name can make differ from the method name.
		rpcName := edge.Metadata.RPCName
		if rpcName == "" {
			rpcName = owner.Name
		}
		if keysByRPC[rpcName] == nil {
			keysByRPC[rpcName] = make(map[string]bool)
		}
		target, isTargetKnown := nodes[edge.To]
		switch {
		case !isTargetKnown || target.Kind != "attribute" || target.Name == "":
			markRPCLockUnresolved(locks, rpcName, fmt.Sprintf("its lock names %q, which is not an Attribute in the Flow Definition Graph", edge.To))
		case target.Resource != nil && target.Resource.Map:
			markRPCLockUnresolved(locks, rpcName, fmt.Sprintf(
				"it locks an instance of AttributeMap %q, and the Flow Definition Graph does not record which instance; invoke it with an SDK Client", target.Name))
		default:
			keysByRPC[rpcName][target.Name] = true
		}
	}
	for rpcName, keys := range keysByRPC {
		rpcLocks := locks[rpcName]
		for key := range keys {
			rpcLocks.AttributeKeys = append(rpcLocks.AttributeKeys, key)
		}
		sort.Strings(rpcLocks.AttributeKeys)
		locks[rpcName] = rpcLocks
	}
	return locks, nil
}

func markRPCLockUnresolved(locks map[string]api.V2RPCLocks, rpcName string, reason string) {
	rpcLocks := locks[rpcName]
	if rpcLocks.UnresolvedReason == "" {
		rpcLocks.UnresolvedReason = reason
	}
	locks[rpcName] = rpcLocks
}

func validateFlowDefinitionGeneratedAt(data []byte) error {
	var generatedAt string
	if err := json.Unmarshal(data, &generatedAt); err != nil {
		return fmt.Errorf("must be a string: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, generatedAt); err != nil {
		return fmt.Errorf("must be an RFC 3339 timestamp: %w", err)
	}
	return nil
}

func decodeStrictFlowDefinitionJSON(data []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	return decoder.Decode(target)
}

func validateFlowDefinitionObjectFields(data []byte, allowed []string, required []string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	allowedFields := make(map[string]bool, len(allowed))
	for _, field := range allowed {
		allowedFields[field] = true
	}
	for field := range object {
		if !allowedFields[field] {
			return fmt.Errorf("unknown field %q", field)
		}
	}
	for _, field := range required {
		if _, exists := object[field]; !exists {
			return fmt.Errorf("missing field %q", field)
		}
	}
	return nil
}

func validateV2Definition(definition api.V2Definition) error {
	if definition.Start != nil {
		if err := api.ValidateV2StartDefinition(*definition.Start); err != nil {
			return fmt.Errorf("Start: %w", err)
		}
	}
	indexedKeys := make(map[string]bool, len(definition.IndexedAttributes))
	indexKeys := make(map[string]bool, len(definition.IndexedAttributes))
	for _, attribute := range definition.IndexedAttributes {
		if attribute.AttributeKey == "" || !v2IndexKeyPattern.MatchString(attribute.IndexKey) ||
			!validV2IndexType(attribute.IndexType, attribute.ValueType) {
			return fmt.Errorf("indexed Attribute %q has invalid metadata", attribute.AttributeKey)
		}
		if indexedKeys[attribute.AttributeKey] || indexKeys[attribute.IndexKey] {
			return fmt.Errorf("indexed Attribute or index key is repeated")
		}
		indexedKeys[attribute.AttributeKey] = true
		indexKeys[attribute.IndexKey] = true
	}
	if definition.Summary.RPCName != "GetDexSummary" || definition.Display.RPCName != "GetDexDisplay" {
		return fmt.Errorf("Summary and Display must use their fixed RPC names")
	}
	if err := validateV2View(definition.Summary, false, indexedKeys); err != nil {
		return fmt.Errorf("Summary: %w", err)
	}
	if err := validateV2View(definition.Display, true, nil); err != nil {
		return fmt.Errorf("Display: %w", err)
	}
	actionNames := make(map[string]bool, len(definition.Actions))
	for _, action := range definition.Actions {
		if action.RPCName == "" || strings.TrimSpace(action.Label) == "" || actionNames[action.RPCName] {
			return fmt.Errorf("Action RPC name and label must be non-empty and unique")
		}
		actionNames[action.RPCName] = true
		if !v2ActionPermissionPattern.MatchString(action.RequiredPermission) {
			return fmt.Errorf("Action %q has an invalid required permission %q", action.RPCName, action.RequiredPermission)
		}
		if action.Condition != nil {
			if action.Condition.AttributeKey == "" || action.Condition.Operator != "in" || len(action.Condition.Values) == 0 {
				return fmt.Errorf("Action %q has an invalid condition", action.RPCName)
			}
			for _, value := range action.Condition.Values {
				switch value.(type) {
				case string, json.Number, bool:
				default:
					return fmt.Errorf("Action %q condition values must be JSON scalars", action.RPCName)
				}
			}
		}
		if err := validateV2ActionInput(action.Input); err != nil {
			return fmt.Errorf("Action %q: %w", action.RPCName, err)
		}
	}
	return nil
}

func validV2IndexType(indexType string, valueType string) bool {
	compatible := map[string]string{
		"keyword": "string", "fulltext": "string", "keyword-array": "string-array",
		"int": "int64", "double": "double", "bool": "bool", "datetime": "datetime",
	}
	return compatible[indexType] == valueType
}

func validateV2View(
	view api.V2RPCView,
	canEdit bool,
	forbiddenKeys map[string]bool,
) error {
	seen := make(map[string]bool, len(view.Fields))
	claimedUISlots := make(map[string]string, len(view.Fields))
	for _, field := range view.Fields {
		if field.AttributeKey == "" || !validV2ViewType(field.ValueType) || seen[field.AttributeKey] {
			return fmt.Errorf("field %q has invalid metadata", field.AttributeKey)
		}
		if field.Editable && (!canEdit || !validV2EditableType(field.ValueType)) {
			return fmt.Errorf("field %q cannot be editable", field.AttributeKey)
		}
		if forbiddenKeys[field.AttributeKey] {
			return fmt.Errorf("field %q duplicates an indexed Attribute", field.AttributeKey)
		}
		if err := claimV2UISlot(field, claimedUISlots); err != nil {
			return err
		}
		seen[field.AttributeKey] = true
	}
	return nil
}

// UI slots a field may claim, mapped to whether only one field per view may claim each.
//
// Repeated here rather than imported from the analyzer because a Flow Definition Graph can reach the
// server from anywhere, so the server cannot assume the analyzer wrote it.
var v2ViewUISlots = map[string]bool{
	"title":          true,
	"subtitle":       true,
	"status":         true,
	"recommendation": true,
	"reason":         false,
}

func claimV2UISlot(field api.V2ViewField, claimedUISlots map[string]string) error {
	if field.UISlot == "" {
		return nil
	}
	unique, known := v2ViewUISlots[field.UISlot]
	if !known {
		return fmt.Errorf("field %q claims unknown UI slot %q", field.AttributeKey, field.UISlot)
	}
	if !unique {
		return nil
	}
	if holder, taken := claimedUISlots[field.UISlot]; taken {
		return fmt.Errorf("UI slot %q is claimed by both %q and %q", field.UISlot, holder, field.AttributeKey)
	}
	claimedUISlots[field.UISlot] = field.AttributeKey
	return nil
}

func validV2ViewType(valueType string) bool {
	switch valueType {
	case "string", "string-array", "int64", "double", "bool", "datetime", "json", "object", "array", "attribute-map":
		return true
	default:
		return false
	}
}

func validV2EditableType(valueType string) bool {
	switch valueType {
	case "string", "int64", "double", "bool", "datetime":
		return true
	default:
		return false
	}
}

func validateV2ActionInput(input api.V2ActionInput) error {
	if input.Kind == "none" {
		if len(input.Fields) != 0 {
			return fmt.Errorf("none input must not declare fields")
		}
		return nil
	}
	if input.Kind != "object" || len(input.Fields) == 0 {
		return fmt.Errorf("input kind must be none or a non-empty object")
	}
	seen := make(map[string]bool, len(input.Fields))
	for _, field := range input.Fields {
		if field.FieldName == "" || !validV2EditableType(field.ValueType) || seen[field.FieldName] {
			return fmt.Errorf("input field %q has invalid metadata", field.FieldName)
		}
		if field.Source == "attribute" && field.AttributeKey == "" {
			return fmt.Errorf("attribute input field %q requires attributeKey", field.FieldName)
		}
		if field.Source == "user" && field.AttributeKey != "" {
			return fmt.Errorf("user input field %q forbids attributeKey", field.FieldName)
		}
		if field.Source != "user" && field.Source != "attribute" {
			return fmt.Errorf("input field %q has invalid source", field.FieldName)
		}
		if field.Capture != "" && (field.Capture != "qr-code" || field.Source != "user" || field.ValueType != "string") {
			return fmt.Errorf("input field %q has invalid capture", field.FieldName)
		}
		seen[field.FieldName] = true
	}
	return nil
}

func (h *flowDefinitionHandler) V2Definitions() map[string]api.V2Definition {
	definitions := make(map[string]api.V2Definition, len(h.v2Definitions))
	for flowType, definition := range h.v2Definitions {
		definitions[flowType] = definition
	}
	return definitions
}

func (h *flowDefinitionHandler) ServeHTTP(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	if _, err := response.Write(h.response); err != nil {
		panic(fmt.Sprintf("write Flow Definition Graph catalog: %v", err))
	}
}
