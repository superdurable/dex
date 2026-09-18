// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type supervisionTestClient struct {
	dexpb.FlowServiceClient
	mutex              sync.Mutex
	searchRequests     []*dexpb.SearchFlowsRequest
	summaryRequests    []*dexpb.GetFlowSummaryRequest
	attributeRequests  []*dexpb.GetAttributesRequest
	setRequests        []*dexpb.SetAttributesRequest
	rpcRequests        []*dexpb.InvokeRPCRequest
	loadBlobRequests   []*dexpb.LoadBlobsRequest
	blobs              map[string]*dexpb.Value
	currentCaseStatus  string
	currentGateRequest string
	invokeRPCHandler   func(context.Context, *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error)
}

func (client *supervisionTestClient) SearchFlows(
	_ context.Context,
	request *dexpb.SearchFlowsRequest,
	_ ...grpc.CallOption,
) (*dexpb.SearchFlowsResponse, error) {
	client.mutex.Lock()
	client.searchRequests = append(client.searchRequests, request)
	client.mutex.Unlock()
	return &dexpb.SearchFlowsResponse{FlowRuns: []*dexpb.SearchFlowsResponseEntry{
		{
			FlowId: "refund-1", RunId: "current-run", FlowType: "RefundFlow",
			FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
			StartTime:  timestamppb.New(time.Date(2026, time.September, 18, 12, 0, 0, 0, time.UTC)),
			IndexedAttributes: []*dexpb.KV{{
				Key: "case-status-index", Value: stringDexValue("awaiting-manager"),
			}},
		},
		{FlowId: "refund-1", RunId: "previous-run", FlowType: "RefundFlow"},
	}}, nil
}

func (client *supervisionTestClient) GetFlowSummary(
	_ context.Context,
	request *dexpb.GetFlowSummaryRequest,
	_ ...grpc.CallOption,
) (*dexpb.GetFlowSummaryResponse, error) {
	client.mutex.Lock()
	client.summaryRequests = append(client.summaryRequests, request)
	client.mutex.Unlock()
	return &dexpb.GetFlowSummaryResponse{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: request.GetFlowId(), RunId: "current-run"},
		FlowType:        "RefundFlow",
		FlowStatus:      dexpb.FlowStatus_FLOW_STATUS_RUNNING,
	}, nil
}

func (client *supervisionTestClient) GetAttributes(
	_ context.Context,
	request *dexpb.GetAttributesRequest,
	_ ...grpc.CallOption,
) (*dexpb.GetAttributesResponse, error) {
	client.mutex.Lock()
	client.attributeRequests = append(client.attributeRequests, request)
	client.mutex.Unlock()
	values := map[string]string{
		"case-status":      client.currentCaseStatus,
		"gate-request-key": client.currentGateRequest,
	}
	attributes := make([]*dexpb.KV, 0, len(request.GetKeys()))
	for _, key := range request.GetKeys() {
		attributes = append(attributes, &dexpb.KV{Key: key, Value: stringDexValue(values[key])})
	}
	return &dexpb.GetAttributesResponse{Attributes: attributes}, nil
}

func (client *supervisionTestClient) SetAttributes(
	_ context.Context,
	request *dexpb.SetAttributesRequest,
	_ ...grpc.CallOption,
) (*emptypb.Empty, error) {
	client.mutex.Lock()
	client.setRequests = append(client.setRequests, request)
	client.mutex.Unlock()
	return &emptypb.Empty{}, nil
}

func (client *supervisionTestClient) LoadBlobs(
	_ context.Context,
	request *dexpb.LoadBlobsRequest,
	_ ...grpc.CallOption,
) (*dexpb.LoadBlobsResponse, error) {
	client.mutex.Lock()
	client.loadBlobRequests = append(client.loadBlobRequests, request)
	blobs := client.blobs
	client.mutex.Unlock()
	values := make(map[string]*dexpb.Value, len(request.GetEntries()))
	for _, entry := range request.GetEntries() {
		blobID := supervisionBlobID(entry.GetBlobValue())
		if hydrated, ok := blobs[blobID]; ok {
			values[blobID] = hydrated
		}
	}
	return &dexpb.LoadBlobsResponse{Values: values}, nil
}

func (client *supervisionTestClient) InvokeRPC(
	ctx context.Context,
	request *dexpb.InvokeRPCRequest,
	_ ...grpc.CallOption,
) (*dexpb.InvokeRPCResponse, error) {
	client.mutex.Lock()
	client.rpcRequests = append(client.rpcRequests, request)
	client.mutex.Unlock()
	if client.invokeRPCHandler != nil {
		return client.invokeRPCHandler(ctx, request)
	}
	switch request.GetRpcName() {
	case "GetDexSummary":
		return &dexpb.InvokeRPCResponse{Output: jsonDexValue(`{"charge-reference":"charge-1"}`)}, nil
	case "GetDexDisplay":
		return &dexpb.InvokeRPCResponse{Output: jsonDexValue(
			`{"operator-note":"reviewed","case-status":"awaiting-manager"}`,
		)}, nil
	case "ApproveRefund", "RejectRefund":
		return &dexpb.InvokeRPCResponse{Output: &dexpb.Value{
			Kind: &dexpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE},
		}}, nil
	default:
		return nil, nil
	}
}

func TestSupervisionFacadeUsesCurrentRunAndStructuredContract(t *testing.T) {
	client := &supervisionTestClient{
		currentCaseStatus: "awaiting-manager", currentGateRequest: "gate-1",
	}
	mux := http.NewServeMux()
	RegisterSupervisionHandlers(mux, client, map[string]SupervisionDefinition{
		"RefundFlow": testSupervisionDefinition(),
	})

	searchResponse := performSupervisionJSON(t, mux, http.MethodPost, "/api/supervision/search", `{
		"flowType":"RefundFlow",
		"filters":[{"field":"case-status","operator":"in","values":["awaiting-manager","review"]}]
	}`)
	if searchResponse.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%q", searchResponse.Code, searchResponse.Body.String())
	}
	var searchResult supervisionSearchResponse
	decodeSupervisionResponse(t, searchResponse, &searchResult)
	if len(searchResult.Flows) != 1 || searchResult.Flows[0].FlowID != "refund-1" {
		t.Fatalf("search result = %+v", searchResult)
	}
	query := client.searchRequests[0].GetQuery()
	if !strings.Contains(query, "`case-status-index`") || !strings.Contains(query, " OR ") || strings.Contains(query, "RunId") {
		t.Fatalf("compiled query = %q", query)
	}

	displayResponse := performSupervisionJSON(
		t, mux, http.MethodGet,
		"/api/supervision/display?flowType=RefundFlow&flowId=refund-1", "",
	)
	if displayResponse.Code != http.StatusOK {
		t.Fatalf("display status = %d body=%q", displayResponse.Code, displayResponse.Body.String())
	}
	var displayResult supervisionDisplayResponse
	decodeSupervisionResponse(t, displayResponse, &displayResult)
	if displayResult.AttributeSnapshot["case-status"] != "awaiting-manager" || len(displayResult.EligibleActions) != 2 {
		t.Fatalf("display result = %+v", displayResult)
	}

	editResponse := performSupervisionJSON(
		t, mux, http.MethodPatch, "/api/supervision/display",
		`{"flowType":"RefundFlow","flowId":"refund-1","attributeKey":"operator-note","value":"done"}`,
	)
	if editResponse.Code != http.StatusOK {
		t.Fatalf("edit status = %d body=%q", editResponse.Code, editResponse.Body.String())
	}
	indexedEditResponse := performSupervisionJSON(
		t, mux, http.MethodPatch, "/api/supervision/display",
		`{"flowType":"RefundFlow","flowId":"refund-1","attributeKey":"case-status","value":"review"}`,
	)
	if indexedEditResponse.Code != http.StatusOK {
		t.Fatalf("indexed edit status = %d body=%q", indexedEditResponse.Code, indexedEditResponse.Body.String())
	}

	actionResponse := performSupervisionJSON(t, mux, http.MethodPost, "/api/supervision/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund",
		"input":{},"attributeSnapshot":{"gate-request-key":"gate-1"}
	}`)
	if actionResponse.Code != http.StatusOK {
		t.Fatalf("Action status = %d body=%q", actionResponse.Code, actionResponse.Body.String())
	}

	assertSupervisionRequestsOmitRunID(t, client)
	if len(client.setRequests) != 2 || client.setRequests[0].GetAttributes()[0].GetKey() != "operator-note" {
		t.Fatalf("SetAttributes requests = %+v", client.setRequests)
	}
	indexedWrite := client.setRequests[1].GetAttributes()[0]
	if indexedWrite.GetIndexConfig().GetIndexKey() != "case-status-index" ||
		indexedWrite.GetIndexConfig().GetType() != dexpb.IndexType_INDEX_TYPE_KEYWORD {
		t.Fatalf("indexed Attribute write = %+v", indexedWrite)
	}
	lastRPC := client.rpcRequests[len(client.rpcRequests)-1]
	if lastRPC.GetRpcName() != "ApproveRefund" || lastRPC.GetInput().GetNullValue() != structpb.NullValue_NULL_VALUE {
		t.Fatalf("Action RPC request = %+v", lastRPC)
	}
}

func TestSupervisionFacadeRejectsRunIDAndStaleActionState(t *testing.T) {
	client := &supervisionTestClient{currentCaseStatus: "resolved"}
	mux := http.NewServeMux()
	RegisterSupervisionHandlers(mux, client, map[string]SupervisionDefinition{
		"RefundFlow": testSupervisionDefinition(),
	})
	displayWithRunID := performSupervisionJSON(
		t, mux, http.MethodGet,
		"/api/supervision/display?flowType=RefundFlow&flowId=refund-1&runId=run-1", "",
	)
	if displayWithRunID.Code != http.StatusBadRequest {
		t.Fatalf("display runId status = %d body=%q", displayWithRunID.Code, displayWithRunID.Body.String())
	}
	withRunID := performSupervisionJSON(t, mux, http.MethodPost, "/api/supervision/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","runId":"run-1","rpcName":"ApproveRefund","input":{}
	}`)
	if withRunID.Code != http.StatusBadRequest {
		t.Fatalf("runId status = %d body=%q", withRunID.Code, withRunID.Body.String())
	}

	stale := performSupervisionJSON(t, mux, http.MethodPost, "/api/supervision/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund","input":{}
	}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale Action status = %d body=%q", stale.Code, stale.Body.String())
	}
}

func TestSupervisionFacadeBuildsObjectActionFromUserAndAttributeInputs(t *testing.T) {
	client := &supervisionTestClient{
		currentCaseStatus: "awaiting-manager", currentGateRequest: "gate-current",
	}
	mux := http.NewServeMux()
	RegisterSupervisionHandlers(mux, client, map[string]SupervisionDefinition{
		"RefundFlow": testSupervisionDefinition(),
	})
	response := performSupervisionJSON(t, mux, http.MethodPost, "/api/supervision/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"RejectRefund",
		"input":{"reason":"duplicate"},"attributeSnapshot":{"gate-request-key":"gate-snapshot"}
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("object Action status = %d body=%q", response.Code, response.Body.String())
	}
	lastRPC := client.rpcRequests[len(client.rpcRequests)-1]
	var input map[string]interface{}
	if err := json.Unmarshal(lastRPC.GetInput().GetObjValue().GetPayload(), &input); err != nil {
		t.Fatal(err)
	}
	if input["reason"] != "duplicate" || input["gateRequestKey"] != "gate-snapshot" {
		t.Fatalf("object Action input = %+v", input)
	}

	forged := performSupervisionJSON(t, mux, http.MethodPost, "/api/supervision/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"RejectRefund",
		"input":{"reason":"duplicate","gateRequestKey":"forged"},
		"attributeSnapshot":{"gate-request-key":"gate-snapshot"}
	}`)
	if forged.Code != http.StatusBadRequest {
		t.Fatalf("forged hidden input status = %d body=%q", forged.Code, forged.Body.String())
	}
}

func TestSupervisionSummaryLoadingBoundsConcurrencyAndIsolatesFailures(t *testing.T) {
	started := make(chan struct{}, 16)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	client := &supervisionTestClient{}
	client.invokeRPCHandler = func(ctx context.Context, request *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if request.GetFlowId() == "failure" {
			return nil, status.Error(codes.Internal, "row failed")
		}
		return &dexpb.InvokeRPCResponse{Output: jsonDexValue(`{"charge-reference":"ok"}`)}, nil
	}
	handler := &supervisionHandler{client: client}
	flows := make([]supervisionFlow, 10)
	for index := range flows {
		flows[index].FlowID = "refund-" + string(rune('a'+index))
	}
	flows[9].FlowID = "failure"
	done := make(chan struct{})
	go func() {
		handler.loadSummaries(context.Background(), testSupervisionDefinition().Summary, flows)
		close(done)
	}()
	for index := 0; index < supervisionRPCConcurrency; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("summary calls did not reach the concurrency limit")
		}
	}
	select {
	case <-started:
		t.Fatal("summary concurrency exceeded its limit")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("summary loading did not finish")
	}
	if maximum.Load() != supervisionRPCConcurrency {
		t.Fatalf("maximum summary concurrency = %d", maximum.Load())
	}
	if flows[9].SummaryError == "" {
		t.Fatalf("failed row did not retain its error: %+v", flows[9])
	}
	for index := 0; index < len(flows)-1; index++ {
		if flows[index].Summary["charge-reference"] != "ok" {
			t.Fatalf("summary row %d = %+v", index, flows[index])
		}
	}
}

func TestSupervisionHydratesBlobBackedViewOutput(t *testing.T) {
	client := &supervisionTestClient{
		currentCaseStatus: "awaiting-manager", currentGateRequest: "gate-1",
		blobs: map[string]*dexpb.Value{
			"summary-blob": jsonDexValue(`{"charge-reference":null}`),
			"display-blob": jsonDexValue(`{"operator-note":"reviewed","case-status":"awaiting-manager"}`),
		},
	}
	client.invokeRPCHandler = func(_ context.Context, request *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		switch request.GetRpcName() {
		case "GetDexSummary":
			return &dexpb.InvokeRPCResponse{Output: blobObjDexValue("summary-blob")}, nil
		case "GetDexDisplay":
			return &dexpb.InvokeRPCResponse{Output: blobObjDexValue("display-blob")}, nil
		default:
			return &dexpb.InvokeRPCResponse{Output: &dexpb.Value{
				Kind: &dexpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE},
			}}, nil
		}
	}
	mux := http.NewServeMux()
	RegisterSupervisionHandlers(mux, client, map[string]SupervisionDefinition{
		"RefundFlow": testSupervisionDefinition(),
	})

	searchResponse := performSupervisionJSON(t, mux, http.MethodPost, "/api/supervision/search", `{
		"flowType":"RefundFlow"
	}`)
	if searchResponse.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%q", searchResponse.Code, searchResponse.Body.String())
	}
	var searchResult supervisionSearchResponse
	decodeSupervisionResponse(t, searchResponse, &searchResult)
	if len(searchResult.Flows) != 1 || searchResult.Flows[0].SummaryError != "" {
		t.Fatalf("search result = %+v", searchResult)
	}
	if _, exists := searchResult.Flows[0].Summary["charge-reference"]; !exists {
		t.Fatalf("summary omitted charge-reference: %+v", searchResult.Flows[0].Summary)
	}

	displayResponse := performSupervisionJSON(
		t, mux, http.MethodGet,
		"/api/supervision/display?flowType=RefundFlow&flowId=refund-1", "",
	)
	if displayResponse.Code != http.StatusOK {
		t.Fatalf("display status = %d body=%q", displayResponse.Code, displayResponse.Body.String())
	}
	var displayResult supervisionDisplayResponse
	decodeSupervisionResponse(t, displayResponse, &displayResult)
	if displayResult.Display["operator-note"] != "reviewed" {
		t.Fatalf("display result = %+v", displayResult)
	}
	if len(client.loadBlobRequests) == 0 {
		t.Fatal("LoadBlobs was not called for blob-backed view output")
	}
}

func TestSupervisionMissingViewBlobSurfacesRowError(t *testing.T) {
	client := &supervisionTestClient{}
	client.invokeRPCHandler = func(_ context.Context, _ *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		return &dexpb.InvokeRPCResponse{Output: blobObjDexValue("missing-blob")}, nil
	}
	handler := &supervisionHandler{client: client}
	flows := []supervisionFlow{{FlowID: "refund-1"}}
	handler.loadSummaries(context.Background(), testSupervisionDefinition().Summary, flows)
	if flows[0].SummaryError == "" {
		t.Fatalf("missing blob did not surface a row error: %+v", flows[0])
	}
}

func TestSupervisionViewOutputContract(t *testing.T) {
	view := testSupervisionDefinition().Summary
	if err := validateViewOutput(view, map[string]interface{}{"charge-reference": nil}); err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]interface{}{
		{},
		{"charge-reference": "ok", "unexpected": true},
		{"charge-reference": 42.5},
	} {
		if err := validateViewOutput(view, values); err == nil {
			t.Fatalf("invalid view output was accepted: %+v", values)
		}
	}
}

func TestSupervisionInt64InputPreservesPrecision(t *testing.T) {
	const maximumInt64 = "9223372036854775807"
	encoded, err := encodeSupervisionValue(maximumInt64, "int64")
	if err != nil {
		t.Fatal(err)
	}
	if encoded.GetIntValue() != int64(9223372036854775807) {
		t.Fatalf("encoded int64 = %d", encoded.GetIntValue())
	}

	action := SupervisionAction{Input: SupervisionActionInput{
		Kind: "object",
		Fields: []SupervisionActionInputField{{
			FieldName: "count", ValueType: "int64", Source: "user", Required: true,
		}},
	}}
	input, err := buildActionInput(action, map[string]interface{}{"count": maximumInt64}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if payload := string(input.GetObjValue().GetPayload()); payload != `{"count":9223372036854775807}` {
		t.Fatalf("Action input = %s", payload)
	}
	condition := SupervisionActionCondition{
		AttributeKey: "count", Operator: "in", Values: []interface{}{json.Number(maximumInt64)},
	}
	if !actionConditionMatches(condition, int64(9223372036854775807)) {
		t.Fatal("maximum int64 Action condition did not match")
	}
	decoded := supervisionDexValue(jsonDexValue(`{"count":9223372036854775807}`)).(map[string]interface{})
	if number, ok := decoded["count"].(json.Number); !ok || number.String() != maximumInt64 {
		t.Fatalf("decoded int64 = %#v", decoded["count"])
	}
	if responseValue := supervisionResponseValue(decoded["count"], "int64"); responseValue != maximumInt64 {
		t.Fatalf("int64 response value = %#v", responseValue)
	}
}

func TestSupervisionSummaryCallsUseFiveSecondTimeout(t *testing.T) {
	client := &supervisionTestClient{}
	client.invokeRPCHandler = func(ctx context.Context, _ *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("summary RPC has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > supervisionRPCTimeout {
			t.Fatalf("summary RPC timeout = %v", remaining)
		}
		return &dexpb.InvokeRPCResponse{Output: jsonDexValue(`{"charge-reference":"ok"}`)}, nil
	}
	flows := []supervisionFlow{{FlowID: "refund-1"}}
	handler := &supervisionHandler{client: client}
	handler.loadSummaries(context.Background(), testSupervisionDefinition().Summary, flows)
	if flows[0].Summary["charge-reference"] != "ok" {
		t.Fatalf("summary = %+v", flows[0])
	}
}

func testSupervisionDefinition() SupervisionDefinition {
	return SupervisionDefinition{
		IndexedAttributes: []SupervisionIndexedAttribute{{
			AttributeKey: "case-status", IndexKey: "case-status-index",
			IndexType: "keyword", ValueType: "string", Description: "Case status",
		}},
		Summary: SupervisionRPCView{RPCName: "GetDexSummary", Fields: []SupervisionViewField{{
			AttributeKey: "charge-reference", ValueType: "string", Description: "Charge reference",
		}}},
		Display: SupervisionRPCView{RPCName: "GetDexDisplay", Fields: []SupervisionViewField{{
			AttributeKey: "operator-note", ValueType: "string", Editable: true, Description: "Operator note",
		}, {
			AttributeKey: "case-status", ValueType: "string", Editable: true, Description: "Case status",
		}}},
		Actions: []SupervisionAction{{
			RPCName: "ApproveRefund", Label: "Approve",
			Condition: SupervisionActionCondition{
				AttributeKey: "case-status", Operator: "in", Values: []interface{}{"awaiting-manager"},
			},
			Input: SupervisionActionInput{Kind: "none"},
		}, {
			RPCName: "RejectRefund", Label: "Reject",
			Condition: SupervisionActionCondition{
				AttributeKey: "case-status", Operator: "in", Values: []interface{}{"awaiting-manager"},
			},
			Input: SupervisionActionInput{Kind: "object", Fields: []SupervisionActionInputField{{
				FieldName: "reason", ValueType: "string", Source: "user", Required: true,
				Description: "Rejection reason",
			}, {
				FieldName: "gateRequestKey", ValueType: "string", Source: "attribute",
				AttributeKey: "gate-request-key", Required: true, Description: "Approval gate",
			}}},
		}},
	}
}

func assertSupervisionRequestsOmitRunID(t *testing.T, client *supervisionTestClient) {
	t.Helper()
	for _, request := range client.summaryRequests {
		if request.GetRunId() != "" {
			t.Fatalf("GetFlowSummary included Run ID: %+v", request)
		}
	}
	for _, request := range client.attributeRequests {
		if request.GetRunId() != "" {
			t.Fatalf("GetAttributes included Run ID: %+v", request)
		}
	}
	for _, request := range client.setRequests {
		if request.GetRunId() != "" {
			t.Fatalf("SetAttributes included Run ID: %+v", request)
		}
	}
	for _, request := range client.rpcRequests {
		if request.GetRunId() != "" {
			t.Fatalf("InvokeRPC included Run ID: %+v", request)
		}
	}
}

func performSupervisionJSON(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeSupervisionResponse(t *testing.T, response *httptest.ResponseRecorder, target interface{}) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func stringDexValue(value string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value}}
}

func jsonDexValue(value string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
		Encoding: "json", Payload: []byte(value),
	}}}
}

func blobObjDexValue(blobID string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForObjValue{InternalBlobIdForObjValue: blobID}}
}
