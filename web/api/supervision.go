// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	supervisionDefaultPageSize = 50
	supervisionRPCConcurrency  = 8
	supervisionRPCTimeout      = 5 * time.Second
)

// SupervisionDefinition describes one Flow type's supervision contract.
type SupervisionDefinition struct {
	IndexedAttributes []SupervisionIndexedAttribute `json:"indexedAttributes"`
	Summary           SupervisionRPCView            `json:"summary"`
	Display           SupervisionRPCView            `json:"display"`
	Actions           []SupervisionAction           `json:"actions"`
}

// SupervisionIndexedAttribute describes one searchable Attribute.
type SupervisionIndexedAttribute struct {
	AttributeKey string `json:"attributeKey"`
	IndexKey     string `json:"indexKey"`
	IndexType    string `json:"indexType"`
	ValueType    string `json:"valueType"`
	Description  string `json:"description"`
}

// SupervisionRPCView describes Summary or Display fields.
type SupervisionRPCView struct {
	RPCName string                 `json:"rpcName"`
	Fields  []SupervisionViewField `json:"fields"`
}

// SupervisionViewField describes one ordered RPC output field.
type SupervisionViewField struct {
	AttributeKey string `json:"attributeKey"`
	ValueType    string `json:"valueType"`
	Editable     bool   `json:"editable"`
	Description  string `json:"description"`
}

// SupervisionAction describes one operator RPC.
type SupervisionAction struct {
	RPCName   string                     `json:"rpcName"`
	Label     string                     `json:"label"`
	Condition SupervisionActionCondition `json:"condition"`
	Input     SupervisionActionInput     `json:"input"`
}

// SupervisionActionCondition describes an Action's visibility predicate.
type SupervisionActionCondition struct {
	AttributeKey string        `json:"attributeKey"`
	Operator     string        `json:"operator"`
	Values       []interface{} `json:"values"`
}

// SupervisionActionInput describes a none or object RPC input.
type SupervisionActionInput struct {
	Kind   string                        `json:"kind"`
	Fields []SupervisionActionInputField `json:"fields"`
}

// SupervisionActionInputField describes one ordered Action input.
type SupervisionActionInputField struct {
	FieldName    string `json:"fieldName"`
	ValueType    string `json:"valueType"`
	Source       string `json:"source"`
	AttributeKey string `json:"attributeKey,omitempty"`
	Required     bool   `json:"required"`
	Description  string `json:"description"`
}

type supervisionHandler struct {
	client      dexpb.FlowServiceClient
	definitions map[string]SupervisionDefinition
}

type supervisionCatalogEntry struct {
	FlowType   string                `json:"flowType"`
	Definition SupervisionDefinition `json:"definition"`
}

type supervisionFilter struct {
	Field    string        `json:"field"`
	Operator string        `json:"operator"`
	Values   []interface{} `json:"values"`
}

type supervisionSearchRequest struct {
	FlowType      string              `json:"flowType"`
	Filters       []supervisionFilter `json:"filters"`
	PageSize      int32               `json:"pageSize"`
	NextPageToken string              `json:"nextPageToken"`
}

type supervisionSearchResponse struct {
	Flows         []supervisionFlow `json:"flows"`
	NextPageToken string            `json:"nextPageToken"`
}

type supervisionFlow struct {
	FlowID            string                 `json:"flowId"`
	FlowType          string                 `json:"flowType"`
	FlowStatus        string                 `json:"flowStatus"`
	FlowStatusCode    int32                  `json:"flowStatusCode"`
	StartTime         *string                `json:"startTime"`
	CloseTime         *string                `json:"closeTime"`
	IndexedAttributes map[string]interface{} `json:"indexedAttributes"`
	Summary           map[string]interface{} `json:"summary,omitempty"`
	SummaryError      string                 `json:"summaryError,omitempty"`
}

type supervisionDisplayResponse struct {
	FlowID            string                 `json:"flowId"`
	FlowType          string                 `json:"flowType"`
	FlowStatus        string                 `json:"flowStatus"`
	FlowStatusCode    int32                  `json:"flowStatusCode"`
	IsActive          bool                   `json:"isActive"`
	Display           map[string]interface{} `json:"display"`
	AttributeSnapshot map[string]interface{} `json:"attributeSnapshot"`
	EligibleActions   []string               `json:"eligibleActions"`
}

type supervisionEditRequest struct {
	FlowType     string      `json:"flowType"`
	FlowID       string      `json:"flowId"`
	AttributeKey string      `json:"attributeKey"`
	Value        interface{} `json:"value"`
}

type supervisionActionRequest struct {
	FlowType          string                 `json:"flowType"`
	FlowID            string                 `json:"flowId"`
	RPCName           string                 `json:"rpcName"`
	Input             map[string]interface{} `json:"input"`
	AttributeSnapshot map[string]interface{} `json:"attributeSnapshot"`
}

func RegisterSupervisionHandlers(
	mux *http.ServeMux,
	client dexpb.FlowServiceClient,
	definitions map[string]SupervisionDefinition,
) {
	if mux == nil {
		panic("HTTP mux must not be nil")
	}
	if client == nil {
		panic("Dex FlowService client must not be nil")
	}
	handler := &supervisionHandler{client: client, definitions: definitions}
	mux.HandleFunc("GET /api/supervision/catalog", handler.catalog)
	mux.HandleFunc("POST /api/supervision/search", handler.search)
	mux.HandleFunc("GET /api/supervision/display", handler.display)
	mux.HandleFunc("PATCH /api/supervision/display", handler.editDisplay)
	mux.HandleFunc("POST /api/supervision/actions", handler.invokeAction)
}

func (h *supervisionHandler) catalog(response http.ResponseWriter, _ *http.Request) {
	flowTypes := make([]string, 0, len(h.definitions))
	for flowType := range h.definitions {
		flowTypes = append(flowTypes, flowType)
	}
	sort.Strings(flowTypes)
	entries := make([]supervisionCatalogEntry, 0, len(flowTypes))
	for _, flowType := range flowTypes {
		entries = append(entries, supervisionCatalogEntry{
			FlowType: flowType, Definition: h.definitions[flowType],
		})
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{
		"enabled": len(entries) > 0,
		"flows":   entries,
	})
}

func (h *supervisionHandler) search(response http.ResponseWriter, request *http.Request) {
	var body supervisionSearchRequest
	if err := decodeJSON(response, request, &body); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	definition, ok := h.definitions[body.FlowType]
	if !ok {
		WriteError(response, http.StatusBadRequest, "flowType has no valid Flow Definition Graph 2.0 contract", nil)
		return
	}
	if body.PageSize < 0 {
		WriteError(response, http.StatusBadRequest, "pageSize must be non-negative", nil)
		return
	}
	if body.PageSize == 0 || body.PageSize > supervisionDefaultPageSize {
		body.PageSize = supervisionDefaultPageSize
	}
	query, err := compileSupervisionQuery(body.FlowType, body.Filters, definition)
	if err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	result, err := h.client.SearchFlows(request.Context(), &dexpb.SearchFlowsRequest{
		Query:         engineWorkflowSearchQuery(query),
		PageSize:      body.PageSize,
		NextPageToken: body.NextPageToken,
	})
	if err != nil {
		writeGRPCError(response, err, "SearchFlows")
		return
	}
	flowRuns := append([]*dexpb.SearchFlowsResponseEntry(nil), result.GetFlowRuns()...)
	sort.SliceStable(flowRuns, func(left int, right int) bool {
		return flowRunStartTime(flowRuns[left]).After(flowRunStartTime(flowRuns[right]))
	})
	flows := make([]supervisionFlow, 0, len(flowRuns))
	seenFlowIDs := make(map[string]struct{}, len(result.GetFlowRuns()))
	for _, entry := range flowRuns {
		if _, seen := seenFlowIDs[entry.GetFlowId()]; seen {
			continue
		}
		seenFlowIDs[entry.GetFlowId()] = struct{}{}
		indexedValues := make(map[string]interface{}, len(definition.IndexedAttributes))
		physicalValues := keyValueMap(entry.GetIndexedAttributes())
		for _, attribute := range definition.IndexedAttributes {
			indexedValues[attribute.AttributeKey] = supervisionResponseValue(
				physicalValues[attribute.IndexKey],
				attribute.ValueType,
			)
		}
		flows = append(flows, supervisionFlow{
			FlowID: entry.GetFlowId(), FlowType: entry.GetFlowType(),
			FlowStatus: flowStatusLabel(entry.GetFlowStatus()), FlowStatusCode: int32(entry.GetFlowStatus()),
			StartTime: timestamp(entry.GetStartTime()), CloseTime: timestamp(entry.GetCloseTime()),
			IndexedAttributes: indexedValues,
		})
	}
	h.loadSummaries(request.Context(), definition.Summary, flows)
	writeJSON(response, http.StatusOK, supervisionSearchResponse{
		Flows: flows, NextPageToken: result.GetNextPageToken(),
	})
}

func (h *supervisionHandler) loadSummaries(
	ctx context.Context,
	view SupervisionRPCView,
	flows []supervisionFlow,
) {
	semaphore := make(chan struct{}, supervisionRPCConcurrency)
	var waitGroup sync.WaitGroup
	for index := range flows {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			callContext, cancelCall := context.WithTimeout(ctx, supervisionRPCTimeout)
			defer cancelCall()
			values, err := h.invokeView(callContext, flows[index].FlowID, view)
			if err != nil {
				flows[index].SummaryError = err.Error()
				return
			}
			flows[index].Summary = values
		}()
	}
	waitGroup.Wait()
}

func (h *supervisionHandler) display(response http.ResponseWriter, request *http.Request) {
	for parameter, values := range request.URL.Query() {
		if (parameter != "flowType" && parameter != "flowId") || len(values) != 1 {
			WriteError(response, http.StatusBadRequest, "display accepts only one flowType and flowId", nil)
			return
		}
	}
	flowType := request.URL.Query().Get("flowType")
	flowID := request.URL.Query().Get("flowId")
	definition, ok := h.definitions[flowType]
	if !ok || flowID == "" {
		WriteError(response, http.StatusBadRequest, "flowType and flowId are required", nil)
		return
	}
	summary, err := h.client.GetFlowSummary(request.Context(), &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	if err != nil {
		writeGRPCError(response, err, "GetFlowSummary")
		return
	}
	if summary.GetFlowType() != flowType {
		WriteError(response, http.StatusNotFound, "Flow does not match the requested flowType", nil)
		return
	}
	displayValues, err := h.invokeView(request.Context(), flowID, definition.Display)
	if err != nil {
		writeGRPCError(response, err, definition.Display.RPCName)
		return
	}
	snapshotKeys := supervisionSnapshotKeys(definition)
	snapshot := make(map[string]interface{}, len(snapshotKeys))
	if len(snapshotKeys) > 0 {
		attributes, attributeErr := h.client.GetAttributes(request.Context(), &dexpb.GetAttributesRequest{
			FlowId: flowID, Keys: snapshotKeys,
		})
		if attributeErr != nil {
			writeGRPCError(response, attributeErr, "GetAttributes")
			return
		}
		snapshot = keyValueMap(attributes.GetAttributes())
	}
	eligibleActions := make([]string, 0, len(definition.Actions))
	for _, action := range definition.Actions {
		if actionConditionMatches(action.Condition, snapshot[action.Condition.AttributeKey]) {
			eligibleActions = append(eligibleActions, action.RPCName)
		}
	}
	isActive := summary.GetFlowStatus() == dexpb.FlowStatus_FLOW_STATUS_RUNNING
	writeJSON(response, http.StatusOK, supervisionDisplayResponse{
		FlowID: flowID, FlowType: flowType,
		FlowStatus: flowStatusLabel(summary.GetFlowStatus()), FlowStatusCode: int32(summary.GetFlowStatus()),
		IsActive: isActive, Display: displayValues, AttributeSnapshot: supervisionResponseSnapshot(snapshot),
		EligibleActions: eligibleActions,
	})
}

func (h *supervisionHandler) editDisplay(response http.ResponseWriter, request *http.Request) {
	var body supervisionEditRequest
	if err := decodeJSON(response, request, &body); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	definition, ok := h.definitions[body.FlowType]
	if !ok || body.FlowID == "" || body.AttributeKey == "" {
		WriteError(response, http.StatusBadRequest, "flowType, flowId, and attributeKey are required", nil)
		return
	}
	field, ok := editableDisplayField(definition.Display.Fields, body.AttributeKey)
	if !ok {
		WriteError(response, http.StatusBadRequest, "attribute is not editable", nil)
		return
	}
	if err := h.requireActiveFlow(request.Context(), body.FlowID, body.FlowType); err != nil {
		writeGRPCError(response, err, "GetFlowSummary")
		return
	}
	value, err := encodeSupervisionValue(body.Value, field.ValueType)
	if err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	write := &dexpb.AttributeWrite{Key: body.AttributeKey, Value: value}
	if indexedAttribute, indexed := findIndexedAttribute(definition, body.AttributeKey); indexed {
		indexType, mapErr := supervisionIndexType(indexedAttribute.IndexType)
		if mapErr != nil {
			WriteError(response, http.StatusInternalServerError, mapErr.Error(), nil)
			return
		}
		write.IndexConfig = &dexpb.IndexConfig{
			Enable: true, Type: indexType, IndexKey: indexedAttribute.IndexKey,
		}
	}
	_, err = h.client.SetAttributes(request.Context(), &dexpb.SetAttributesRequest{
		FlowId: body.FlowID, Attributes: []*dexpb.AttributeWrite{write}, RequestId: uuid.NewString(),
	})
	if err != nil {
		writeGRPCError(response, err, "SetAttributes")
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{
		"attributeKey": body.AttributeKey, "value": body.Value,
	})
}

func (h *supervisionHandler) invokeAction(response http.ResponseWriter, request *http.Request) {
	var body supervisionActionRequest
	if err := decodeJSON(response, request, &body); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	definition, ok := h.definitions[body.FlowType]
	if !ok || body.FlowID == "" || body.RPCName == "" {
		WriteError(response, http.StatusBadRequest, "flowType, flowId, and rpcName are required", nil)
		return
	}
	action, ok := findSupervisionAction(definition.Actions, body.RPCName)
	if !ok {
		WriteError(response, http.StatusBadRequest, "rpcName is not a declared Action", nil)
		return
	}
	if err := h.requireActiveFlow(request.Context(), body.FlowID, body.FlowType); err != nil {
		writeGRPCError(response, err, "GetFlowSummary")
		return
	}
	conditionAttributes, err := h.client.GetAttributes(request.Context(), &dexpb.GetAttributesRequest{
		FlowId: body.FlowID, Keys: []string{action.Condition.AttributeKey},
	})
	if err != nil {
		writeGRPCError(response, err, "GetAttributes")
		return
	}
	currentValues := keyValueMap(conditionAttributes.GetAttributes())
	if !actionConditionMatches(action.Condition, currentValues[action.Condition.AttributeKey]) {
		WriteError(response, http.StatusConflict, "Action condition no longer matches the current Flow state", nil)
		return
	}
	input, err := buildActionInput(action, body.Input, body.AttributeSnapshot)
	if err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	result, err := h.client.InvokeRPC(request.Context(), &dexpb.InvokeRPCRequest{
		FlowId: body.FlowID, RpcName: action.RPCName, Input: input,
		TimeoutSeconds: int32(supervisionRPCTimeout.Seconds()), RequestId: uuid.NewString(),
	})
	if err != nil {
		writeGRPCError(response, err, action.RPCName)
		return
	}
	if dexValue(result.GetOutput()) != nil {
		WriteError(response, http.StatusBadGateway, "Action RPC returned a non-null value", nil)
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"invoked": true})
}

func (h *supervisionHandler) invokeView(
	ctx context.Context,
	flowID string,
	view SupervisionRPCView,
) (map[string]interface{}, error) {
	result, err := h.client.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId: flowID, RpcName: view.RPCName,
		Input:          &dexpb.Value{Kind: &dexpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE}},
		TimeoutSeconds: int32(supervisionRPCTimeout.Seconds()), RequestId: uuid.NewString(),
	})
	if err != nil {
		return nil, err
	}
	output, err := h.hydrateSupervisionValue(ctx, flowID, result.GetOutput())
	if err != nil {
		return nil, err
	}
	values, ok := supervisionDexValue(output).(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%s returned a non-object value", view.RPCName)
	}
	if err := validateViewOutput(view, values); err != nil {
		return nil, err
	}
	for _, field := range view.Fields {
		values[field.AttributeKey] = supervisionResponseValue(values[field.AttributeKey], field.ValueType)
	}
	return values, nil
}

// InvokeRPC leaves large outputs as blob IDs when lazy loading is enabled.
func (h *supervisionHandler) hydrateSupervisionValue(
	ctx context.Context,
	flowID string,
	value *dexpb.Value,
) (*dexpb.Value, error) {
	blobID := supervisionBlobID(value)
	if blobID == "" {
		return value, nil
	}
	result, err := h.client.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{
		Entries: []*dexpb.LoadBlobRequestEntry{{FlowId: flowID, BlobValue: value}},
	})
	if err != nil {
		return nil, err
	}
	hydrated, ok := result.GetValues()[blobID]
	if !ok {
		return nil, fmt.Errorf("value blob unavailable")
	}
	return hydrated, nil
}

func supervisionBlobID(value *dexpb.Value) string {
	if value == nil {
		return ""
	}
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_InternalBlobIdForStringValue:
		return kind.InternalBlobIdForStringValue
	case *dexpb.Value_InternalBlobIdForObjValue:
		return kind.InternalBlobIdForObjValue
	default:
		return ""
	}
}

func supervisionDexValue(value *dexpb.Value) interface{} {
	object, ok := value.GetKind().(*dexpb.Value_ObjValue)
	if !ok || object.ObjValue.GetEncoding() != "json" {
		return dexValue(value)
	}
	decoder := json.NewDecoder(strings.NewReader(string(object.ObjValue.GetPayload())))
	decoder.UseNumber()
	var decoded interface{}
	if err := decoder.Decode(&decoded); err != nil {
		return dexValue(value)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return dexValue(value)
	}
	return decoded
}

func (h *supervisionHandler) requireActiveFlow(ctx context.Context, flowID string, flowType string) error {
	summary, err := h.client.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	if err != nil {
		return err
	}
	if summary.GetFlowType() != flowType {
		return status.Error(codes.NotFound, "Flow does not match the requested flowType")
	}
	if summary.GetFlowStatus() != dexpb.FlowStatus_FLOW_STATUS_RUNNING {
		return status.Error(codes.FailedPrecondition, "Flow is not active")
	}
	return nil
}

func compileSupervisionQuery(
	flowType string,
	filters []supervisionFilter,
	definition SupervisionDefinition,
) (string, error) {
	conditions := []string{"FlowType = " + quoteVisibilityString(flowType)}
	for _, filter := range filters {
		if len(filter.Values) == 0 {
			return "", fmt.Errorf("filter %q must contain at least one value", filter.Field)
		}
		physicalKey, valueType, indexType, ok := supervisionFilterField(filter.Field, definition)
		if !ok {
			return "", fmt.Errorf("unknown supervision filter field %q", filter.Field)
		}
		if !isSupervisionIndexKey(physicalKey) {
			return "", fmt.Errorf("filter %q maps to an invalid index key", filter.Field)
		}
		condition, err := compileSupervisionFilter(physicalKey, valueType, indexType, filter)
		if err != nil {
			return "", err
		}
		conditions = append(conditions, condition)
	}
	return strings.Join(conditions, " AND "), nil
}

func supervisionFilterField(
	field string,
	definition SupervisionDefinition,
) (string, string, string, bool) {
	switch field {
	case "flowId":
		return "WorkflowId", "string", "keyword", true
	case "executionStatus":
		return "ExecutionStatus", "string", "keyword", true
	case "startTime":
		return "StartTime", "datetime", "datetime", true
	case "closeTime":
		return "CloseTime", "datetime", "datetime", true
	default:
		for _, attribute := range definition.IndexedAttributes {
			if attribute.AttributeKey == field {
				return attribute.IndexKey, attribute.ValueType, attribute.IndexType, true
			}
		}
		return "", "", "", false
	}
}

func compileSupervisionFilter(
	physicalKey string,
	valueType string,
	indexType string,
	filter supervisionFilter,
) (string, error) {
	fieldExpression := quoteVisibilityField(physicalKey)
	operator := strings.ToLower(filter.Operator)
	if operator == "" {
		operator = "eq"
	}
	if operator == "in" || len(filter.Values) > 1 {
		if operator != "in" && operator != "eq" {
			return "", fmt.Errorf("filter %q does not support multiple values with %q", filter.Field, operator)
		}
		parts := make([]string, 0, len(filter.Values))
		for _, value := range filter.Values {
			literal, err := visibilityLiteral(value, valueType)
			if err != nil {
				return "", fmt.Errorf("filter %q: %w", filter.Field, err)
			}
			parts = append(parts, fieldExpression+" = "+literal)
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}
	literal, err := visibilityLiteral(filter.Values[0], valueType)
	if err != nil {
		return "", fmt.Errorf("filter %q: %w", filter.Field, err)
	}
	switch operator {
	case "eq":
		return fieldExpression + " = " + literal, nil
	case "gt", "gte", "lt", "lte":
		if indexType != "int" && indexType != "double" && indexType != "datetime" {
			return "", fmt.Errorf("filter %q does not support ordered comparison", filter.Field)
		}
		operators := map[string]string{"gt": ">", "gte": ">=", "lt": "<", "lte": "<="}
		return fieldExpression + " " + operators[operator] + " " + literal, nil
	case "contains":
		if indexType != "fulltext" {
			return "", fmt.Errorf("contains requires a fulltext index")
		}
		value, ok := filter.Values[0].(string)
		if !ok {
			return "", fmt.Errorf("contains requires a string value")
		}
		return fieldExpression + " = " + quoteVisibilityString(value), nil
	default:
		return "", fmt.Errorf("unsupported filter operator %q", filter.Operator)
	}
}

func visibilityLiteral(value interface{}, valueType string) (string, error) {
	switch valueType {
	case "string", "datetime":
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("value must be a string")
		}
		if valueType == "datetime" {
			if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
				return "", fmt.Errorf("value must be RFC3339 datetime")
			}
		}
		return quoteVisibilityString(text), nil
	case "int64":
		integer, ok := supervisionRequestInt64(value)
		if !ok {
			return "", fmt.Errorf("value must be an int64")
		}
		return strconv.FormatInt(integer, 10), nil
	case "double":
		number, ok := jsonNumberFloat64(value)
		if !ok {
			return "", fmt.Errorf("value must be a finite number")
		}
		return strconv.FormatFloat(number, 'g', -1, 64), nil
	case "bool":
		boolean, ok := value.(bool)
		if !ok {
			return "", fmt.Errorf("value must be a boolean")
		}
		return strconv.FormatBool(boolean), nil
	case "string-array":
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("value must be a string")
		}
		return quoteVisibilityString(text), nil
	default:
		return "", fmt.Errorf("unsupported value type %q", valueType)
	}
}

func quoteVisibilityString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func quoteVisibilityField(value string) string {
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		return "`" + value + "`"
	}
	return value
}

func isSupervisionIndexKey(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		isLetter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		isLaterCharacter := index > 0 && (character >= '0' && character <= '9' || character == '-' || character == '.')
		if isLetter || character == '_' || isLaterCharacter {
			continue
		}
		return false
	}
	return true
}

func flowRunStartTime(entry *dexpb.SearchFlowsResponseEntry) time.Time {
	if entry == nil || entry.GetStartTime() == nil {
		return time.Time{}
	}
	return entry.GetStartTime().AsTime()
}

func validateViewOutput(view SupervisionRPCView, values map[string]interface{}) error {
	if len(values) != len(view.Fields) {
		return fmt.Errorf("%s returned %d fields; contract declares %d", view.RPCName, len(values), len(view.Fields))
	}
	for _, field := range view.Fields {
		value, exists := values[field.AttributeKey]
		if !exists {
			return fmt.Errorf("%s omitted declared field %q", view.RPCName, field.AttributeKey)
		}
		if value != nil && !isSupervisionValueType(value, field.ValueType) {
			return fmt.Errorf("%s field %q does not match %s", view.RPCName, field.AttributeKey, field.ValueType)
		}
	}
	return nil
}

func supervisionResponseValue(value interface{}, valueType string) interface{} {
	if value == nil || valueType != "int64" {
		return value
	}
	integer, ok := jsonNumberInt64(value)
	if !ok {
		return value
	}
	return strconv.FormatInt(integer, 10)
}

func supervisionResponseSnapshot(snapshot map[string]interface{}) map[string]interface{} {
	response := make(map[string]interface{}, len(snapshot))
	for key, value := range snapshot {
		if integer, ok := value.(int64); ok {
			response[key] = strconv.FormatInt(integer, 10)
			continue
		}
		response[key] = value
	}
	return response
}

func isSupervisionValueType(value interface{}, valueType string) bool {
	switch valueType {
	case "string", "datetime":
		text, ok := value.(string)
		if !ok {
			return false
		}
		if valueType == "datetime" {
			_, err := time.Parse(time.RFC3339Nano, text)
			return err == nil
		}
		return true
	case "int64":
		_, ok := jsonNumberInt64(value)
		return ok
	case "double":
		_, ok := jsonNumberFloat64(value)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "string-array":
		values, ok := value.([]interface{})
		if !ok {
			return false
		}
		for _, item := range values {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	case "object", "attribute-map":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "json":
		return true
	default:
		return false
	}
}

func encodeSupervisionValue(value interface{}, valueType string) (*dexpb.Value, error) {
	if valueType == "int64" {
		integer, ok := supervisionRequestInt64(value)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", valueType)
		}
		return &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: integer}}, nil
	}
	if !isSupervisionValueType(value, valueType) {
		return nil, fmt.Errorf("value does not match %s", valueType)
	}
	switch valueType {
	case "string", "datetime":
		return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value.(string)}}, nil
	case "double":
		number, _ := jsonNumberFloat64(value)
		return &dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: number}}, nil
	case "bool":
		return &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: value.(bool)}}, nil
	default:
		payload, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: "json", Payload: payload,
		}}}, nil
	}
}

func buildActionInput(
	action SupervisionAction,
	userInput map[string]interface{},
	attributeSnapshot map[string]interface{},
) (*dexpb.Value, error) {
	if action.Input.Kind == "none" {
		if len(userInput) != 0 || len(action.Input.Fields) != 0 {
			return nil, fmt.Errorf("no-input Action must not receive input")
		}
		return &dexpb.Value{Kind: &dexpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE}}, nil
	}
	input := make(map[string]interface{}, len(action.Input.Fields))
	userFields := make(map[string]struct{}, len(action.Input.Fields))
	for _, field := range action.Input.Fields {
		var value interface{}
		var exists bool
		if field.Source == "attribute" {
			value, exists = attributeSnapshot[field.AttributeKey]
		} else {
			userFields[field.FieldName] = struct{}{}
			value, exists = userInput[field.FieldName]
		}
		if !exists || value == nil {
			if field.Required {
				return nil, fmt.Errorf("Action input %q is required", field.FieldName)
			}
			input[field.FieldName] = nil
			continue
		}
		if field.ValueType == "int64" {
			integer, validInteger := supervisionRequestInt64(value)
			if !validInteger {
				return nil, fmt.Errorf("Action input %q does not match %s", field.FieldName, field.ValueType)
			}
			input[field.FieldName] = integer
			continue
		}
		if !isSupervisionValueType(value, field.ValueType) {
			return nil, fmt.Errorf("Action input %q does not match %s", field.FieldName, field.ValueType)
		}
		input[field.FieldName] = value
	}
	for fieldName := range userInput {
		if _, ok := userFields[fieldName]; !ok {
			return nil, fmt.Errorf("unknown Action input %q", fieldName)
		}
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
		Encoding: "json", Payload: payload,
	}}}, nil
}

func actionConditionMatches(condition SupervisionActionCondition, currentValue interface{}) bool {
	if condition.Operator != "in" {
		return false
	}
	currentJSON, err := json.Marshal(currentValue)
	if err != nil {
		return false
	}
	for _, expected := range condition.Values {
		expectedJSON, marshalErr := json.Marshal(expected)
		if marshalErr == nil && string(currentJSON) == string(expectedJSON) {
			return true
		}
	}
	return false
}

func supervisionSnapshotKeys(definition SupervisionDefinition) []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for _, action := range definition.Actions {
		for _, key := range []string{action.Condition.AttributeKey} {
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				keys = append(keys, key)
			}
		}
		for _, field := range action.Input.Fields {
			if field.Source == "attribute" {
				if _, exists := seen[field.AttributeKey]; !exists {
					seen[field.AttributeKey] = struct{}{}
					keys = append(keys, field.AttributeKey)
				}
			}
		}
	}
	return keys
}

func keyValueMap(values []*dexpb.KV) map[string]interface{} {
	mapped := make(map[string]interface{}, len(values))
	for _, value := range values {
		mapped[value.GetKey()] = dexValue(value.GetValue())
	}
	return mapped
}

func editableDisplayField(fields []SupervisionViewField, attributeKey string) (SupervisionViewField, bool) {
	for _, field := range fields {
		if field.AttributeKey == attributeKey && field.Editable {
			return field, true
		}
	}
	return SupervisionViewField{}, false
}

func findIndexedAttribute(
	definition SupervisionDefinition,
	attributeKey string,
) (SupervisionIndexedAttribute, bool) {
	for _, attribute := range definition.IndexedAttributes {
		if attribute.AttributeKey == attributeKey {
			return attribute, true
		}
	}
	return SupervisionIndexedAttribute{}, false
}

func findSupervisionAction(actions []SupervisionAction, rpcName string) (SupervisionAction, bool) {
	for _, action := range actions {
		if action.RPCName == rpcName {
			return action, true
		}
	}
	return SupervisionAction{}, false
}

func supervisionIndexType(indexType string) (dexpb.IndexType, error) {
	switch indexType {
	case "keyword":
		return dexpb.IndexType_INDEX_TYPE_KEYWORD, nil
	case "fulltext":
		return dexpb.IndexType_INDEX_TYPE_TEXT, nil
	case "keyword-array":
		return dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY, nil
	case "int":
		return dexpb.IndexType_INDEX_TYPE_INT, nil
	case "double":
		return dexpb.IndexType_INDEX_TYPE_DOUBLE, nil
	case "bool":
		return dexpb.IndexType_INDEX_TYPE_BOOL, nil
	case "datetime":
		return dexpb.IndexType_INDEX_TYPE_DATETIME, nil
	default:
		return dexpb.IndexType_INDEX_TYPE_UNSPECIFIED, fmt.Errorf("unsupported index type %q", indexType)
	}
}

func jsonNumberInt64(value interface{}) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int32:
		return int64(number), true
	case int64:
		return number, true
	case json.Number:
		integer, err := number.Int64()
		return integer, err == nil
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number ||
			number < math.MinInt64 || number > math.MaxInt64 {
			return 0, false
		}
		return int64(number), true
	default:
		return 0, false
	}
}

func supervisionRequestInt64(value interface{}) (int64, bool) {
	if text, ok := value.(string); ok {
		integer, err := strconv.ParseInt(text, 10, 64)
		return integer, err == nil
	}
	return jsonNumberInt64(value)
}

func jsonNumberFloat64(value interface{}) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case int:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case float32:
		number = float64(typed)
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}
