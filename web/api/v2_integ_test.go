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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type v2TestClient struct {
	dexpb.FlowServiceClient
	mutex              sync.Mutex
	searchEntries      []*dexpb.SearchFlowsResponseEntry
	searchRequests     []*dexpb.SearchFlowsRequest
	summaryRequests    []*dexpb.GetFlowSummaryRequest
	attributeRequests  []*dexpb.GetAttributesRequest
	setRequests        []*dexpb.SetAttributesRequest
	rpcRequests        []*dexpb.InvokeRPCRequest
	startRequests      []*dexpb.StartFlowRequest
	loadBlobRequests   []*dexpb.LoadBlobsRequest
	blobs              map[string]*dexpb.Value
	currentCaseStatus  string
	currentGateRequest string
	flowStatus         dexpb.FlowStatus
	invokeRPCHandler   func(context.Context, *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error)
	startFlowHandler   func(context.Context, *dexpb.StartFlowRequest) (*dexpb.StartFlowResponse, error)
	serverEngine       dexpb.ServerEngine
	serverInfoErrors   []error
	serverInfoCalls    int
}

func (client *v2TestClient) GetServerInfo(
	context.Context,
	*emptypb.Empty,
	...grpc.CallOption,
) (*dexpb.ServerInfo, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.serverInfoCalls++
	if len(client.serverInfoErrors) > 0 {
		err := client.serverInfoErrors[0]
		client.serverInfoErrors = client.serverInfoErrors[1:]
		return nil, err
	}
	return &dexpb.ServerInfo{ServerEngine: client.serverEngine}, nil
}

func (client *v2TestClient) StartFlow(
	ctx context.Context,
	request *dexpb.StartFlowRequest,
	_ ...grpc.CallOption,
) (*dexpb.StartFlowResponse, error) {
	client.mutex.Lock()
	client.startRequests = append(client.startRequests, request)
	client.mutex.Unlock()
	if client.startFlowHandler != nil {
		return client.startFlowHandler(ctx, request)
	}
	return &dexpb.StartFlowResponse{RunId: "started-run"}, nil
}

func (client *v2TestClient) SearchFlows(
	_ context.Context,
	request *dexpb.SearchFlowsRequest,
	_ ...grpc.CallOption,
) (*dexpb.SearchFlowsResponse, error) {
	client.mutex.Lock()
	client.searchRequests = append(client.searchRequests, request)
	client.mutex.Unlock()
	if client.searchEntries != nil {
		return &dexpb.SearchFlowsResponse{FlowRuns: client.searchEntries}, nil
	}
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

func (client *v2TestClient) GetFlowSummary(
	_ context.Context,
	request *dexpb.GetFlowSummaryRequest,
	_ ...grpc.CallOption,
) (*dexpb.GetFlowSummaryResponse, error) {
	client.mutex.Lock()
	client.summaryRequests = append(client.summaryRequests, request)
	flowStatus := client.flowStatus
	client.mutex.Unlock()
	if flowStatus == dexpb.FlowStatus_FLOW_STATUS_UNSPECIFIED {
		flowStatus = dexpb.FlowStatus_FLOW_STATUS_RUNNING
	}
	return &dexpb.GetFlowSummaryResponse{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: request.GetFlowId(), RunId: "current-run"},
		FlowType:        "RefundFlow",
		FlowStatus:      flowStatus,
	}, nil
}

func (client *v2TestClient) GetAttributes(
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

func (client *v2TestClient) SetAttributes(
	_ context.Context,
	request *dexpb.SetAttributesRequest,
	_ ...grpc.CallOption,
) (*emptypb.Empty, error) {
	client.mutex.Lock()
	client.setRequests = append(client.setRequests, request)
	client.mutex.Unlock()
	return &emptypb.Empty{}, nil
}

func (client *v2TestClient) LoadBlobs(
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
		blobID := v2BlobID(entry.GetBlobValue())
		if hydrated, ok := blobs[blobID]; ok {
			values[blobID] = hydrated
		}
	}
	return &dexpb.LoadBlobsResponse{Values: values}, nil
}

func (client *v2TestClient) InvokeRPC(
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

func TestV2FacadeUsesCurrentRunAndStructuredContract(t *testing.T) {
	client := &v2TestClient{
		currentCaseStatus: "awaiting-manager", currentGateRequest: "gate-1",
	}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{
		"RefundFlow": testV2Definition(),
	})

	searchResponse := performV2JSON(t, mux, http.MethodPost, "/api/v2/search", `{
		"flowType":"RefundFlow",
		"workQueuePermissions":["refund.message","refund.manage","refund.message"],
		"filters":[{"field":"case-status","operator":"in","values":["awaiting-manager","review"]}]
	}`)
	if searchResponse.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%q", searchResponse.Code, searchResponse.Body.String())
	}
	var searchResult v2SearchResponse
	decodeV2Response(t, searchResponse, &searchResult)
	if len(searchResult.Flows) != 1 || searchResult.Flows[0].FlowID != "refund-1" {
		t.Fatalf("search result = %+v", searchResult)
	}
	query := client.searchRequests[0].GetQuery()
	permissionUnion := "(DexWorkQueuePermissions = 'refund.manage' OR DexWorkQueuePermissions = 'refund.message')"
	if !strings.Contains(query, permissionUnion) || !strings.Contains(query, "`case-status-index`") || strings.Contains(query, "RunId") {
		t.Fatalf("compiled query = %q", query)
	}

	displayResponse := performV2JSON(
		t, mux, http.MethodGet,
		"/api/v2/display?flowType=RefundFlow&flowId=refund-1", "",
	)
	if displayResponse.Code != http.StatusOK {
		t.Fatalf("display status = %d body=%q", displayResponse.Code, displayResponse.Body.String())
	}
	var displayResult v2DisplayResponse
	decodeV2Response(t, displayResponse, &displayResult)
	if displayResult.AttributeSnapshot["case-status"] != "awaiting-manager" || len(displayResult.EligibleActions) != 2 {
		t.Fatalf("display result = %+v", displayResult)
	}

	editResponse := performV2JSON(
		t, mux, http.MethodPatch, "/api/v2/display",
		`{"flowType":"RefundFlow","flowId":"refund-1","attributeKey":"operator-note","value":"done"}`,
	)
	if editResponse.Code != http.StatusOK {
		t.Fatalf("edit status = %d body=%q", editResponse.Code, editResponse.Body.String())
	}
	indexedEditResponse := performV2JSON(
		t, mux, http.MethodPatch, "/api/v2/display",
		`{"flowType":"RefundFlow","flowId":"refund-1","attributeKey":"case-status","value":"review"}`,
	)
	if indexedEditResponse.Code != http.StatusOK {
		t.Fatalf("indexed edit status = %d body=%q", indexedEditResponse.Code, indexedEditResponse.Body.String())
	}

	actionResponse := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund",
		"input":{},"attributeSnapshot":{"gate-request-key":"gate-1"}
	}`)
	if actionResponse.Code != http.StatusOK {
		t.Fatalf("Action status = %d body=%q", actionResponse.Code, actionResponse.Body.String())
	}

	assertV2RequestsOmitRunID(t, client)
	if len(client.setRequests) != 2 || client.setRequests[0].GetAttributes()[0].GetKey() != "operator-note" {
		t.Fatalf("SetAttributes requests = %+v", client.setRequests)
	}
	if client.setRequests[0].GetActionPermissionMappings() != nil {
		t.Fatalf("unrelated edit included Action permission mappings: %+v", client.setRequests[0])
	}
	indexedWrite := client.setRequests[1].GetAttributes()[0]
	if indexedWrite.GetIndexConfig().GetIndexKey() != "case-status-index" ||
		indexedWrite.GetIndexConfig().GetType() != dexpb.IndexType_INDEX_TYPE_KEYWORD {
		t.Fatalf("indexed Attribute write = %+v", indexedWrite)
	}
	mappings := client.setRequests[1].GetActionPermissionMappings().GetMappings()
	if len(mappings) != 3 ||
		mappings[0].GetAttributeKey() != "case-status" ||
		mappings[0].GetRequiredPermission() != "refund.manage" ||
		mappings[1].GetAttributeKey() != "case-status" ||
		mappings[2].GetAttributeKey() != "message-priority" ||
		mappings[2].GetRequiredPermission() != "refund.message" ||
		mappings[2].GetEqualValues()[0].GetIntValue() != int64(9223372036854775807) {
		t.Fatalf("Action permission mappings = %+v", mappings)
	}
	lastRPC := client.rpcRequests[len(client.rpcRequests)-1]
	if lastRPC.GetRpcName() != "ApproveRefund" || lastRPC.GetInput().GetNullValue() != structpb.NullValue_NULL_VALUE {
		t.Fatalf("Action RPC request = %+v", lastRPC)
	}
}

func TestV2SearchFiltersByActiveStepTypes(t *testing.T) {
	cases := []struct {
		name      string
		filter    string
		wantQuery string
	}{{
		name:      "one Step type",
		filter:    `{"field":"activeStepTypes","operator":"eq","values":["AwaitApproval"]}`,
		wantQuery: "FlowType = 'RefundFlow' AND ActiveStepTypes = 'AwaitApproval'",
	}, {
		name:   "any of several Step types",
		filter: `{"field":"activeStepTypes","operator":"in","values":["AwaitApproval","Await'Manager"]}`,
		wantQuery: "FlowType = 'RefundFlow' AND " +
			"(ActiveStepTypes = 'AwaitApproval' OR ActiveStepTypes = 'Await''Manager')",
	}, {
		name:      "a backslash is escaped before the quote",
		filter:    `{"field":"activeStepTypes","operator":"eq","values":["Await\\'Manager\\"]}`,
		wantQuery: `FlowType = 'RefundFlow' AND ActiveStepTypes = 'Await\\''Manager\\'`,
	}}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := &v2TestClient{}
			mux := http.NewServeMux()
			RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": testV2Definition()})

			response := performV2JSON(t, mux, http.MethodPost, "/api/v2/search",
				`{"flowType":"RefundFlow","filters":[`+testCase.filter+`]}`)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
			}
			if query := client.searchRequests[0].GetQuery(); query != testCase.wantQuery {
				t.Fatalf("query = %q, want %q", query, testCase.wantQuery)
			}
		})
	}
}

func TestV2SearchRejectsActiveStepTypesOperatorsAKeywordCannotAnswer(t *testing.T) {
	for _, operator := range []string{"gte", "contains"} {
		t.Run(operator, func(t *testing.T) {
			client := &v2TestClient{}
			mux := http.NewServeMux()
			RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": testV2Definition()})

			response := performV2JSON(t, mux, http.MethodPost, "/api/v2/search", `{
				"flowType":"RefundFlow",
				"filters":[{"field":"activeStepTypes","operator":"`+operator+`","values":["AwaitApproval"]}]
			}`)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
			}
			if len(client.searchRequests) != 0 {
				t.Fatalf("SearchFlows requests = %+v", client.searchRequests)
			}
		})
	}
}

func TestV2SearchAcrossFlowTypesCompilesOneSortedQuery(t *testing.T) {
	client := &v2TestClient{}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{
		"OrderFlow": testV2Definition(), "RefundFlow": testV2Definition(),
	})

	response := performV2JSON(t, mux, http.MethodPost, "/api/v2/search", `{
		"flowTypes":["RefundFlow","OrderFlow","RefundFlow"],
		"filters":[
			{"field":"activeStepTypes","operator":"in","values":["AwaitApproval"]},
			{"field":"executionStatus","operator":"eq","values":["Running"]}
		]
	}`)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	if len(client.searchRequests) != 1 {
		t.Fatalf("SearchFlows requests = %d, want 1", len(client.searchRequests))
	}
	want := "(FlowType = 'OrderFlow' OR FlowType = 'RefundFlow') AND " +
		"(ActiveStepTypes = 'AwaitApproval') AND ExecutionStatus = 'Running'"
	if query := client.searchRequests[0].GetQuery(); query != want {
		t.Fatalf("query = %q, want %q", query, want)
	}
}

func TestV2SearchWithOneOfFlowTypesKeepsThePinnedQuery(t *testing.T) {
	client := &v2TestClient{}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": testV2Definition()})

	response := performV2JSON(t, mux, http.MethodPost, "/api/v2/search", `{
		"flowTypes":["RefundFlow"],
		"filters":[{"field":"case-status","operator":"eq","values":["review"]}]
	}`)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	want := "FlowType = 'RefundFlow' AND `case-status-index` = 'review'"
	if query := client.searchRequests[0].GetQuery(); query != want {
		t.Fatalf("query = %q, want %q", query, want)
	}
}

func TestV2SearchAcrossFlowTypesReadsEachRowWithItsOwnDefinition(t *testing.T) {
	client := &v2TestClient{
		searchEntries: []*dexpb.SearchFlowsResponseEntry{{
			FlowId: "refund-1", FlowType: "RefundFlow",
			IndexedAttributes: []*dexpb.KV{{Key: "keyword1", Value: stringDexValue("review")}},
		}, {
			FlowId: "order-1", FlowType: "OrderFlow",
			IndexedAttributes: []*dexpb.KV{{Key: "keyword1", Value: stringDexValue("west")}},
		}},
		invokeRPCHandler: func(_ context.Context, request *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
			return &dexpb.InvokeRPCResponse{Output: jsonDexValue(
				`{"summary-owner":"` + request.GetRpcName() + `"}`,
			)}, nil
		},
	}
	definitionFor := func(attributeKey string, summaryRPC string) V2Definition {
		return V2Definition{
			IndexedAttributes: []V2IndexedAttribute{{
				AttributeKey: attributeKey, IndexKey: "keyword1",
				IndexType: "keyword", ValueType: "string", Description: attributeKey,
			}},
			Summary: V2RPCView{RPCName: summaryRPC, Fields: []V2ViewField{{
				AttributeKey: "summary-owner", ValueType: "string", Description: "Summary owner",
			}}},
		}
	}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{
		"RefundFlow": definitionFor("case-status", "GetRefundSummary"),
		"OrderFlow":  definitionFor("warehouse", "GetOrderSummary"),
	})

	response := performV2JSON(t, mux, http.MethodPost, "/api/v2/search", `{"flowTypes":["RefundFlow","OrderFlow"]}`)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	var result v2SearchResponse
	decodeV2Response(t, response, &result)
	rows := make(map[string]v2Flow, len(result.Flows))
	for _, flow := range result.Flows {
		rows[flow.FlowID] = flow
	}
	refund, order := rows["refund-1"], rows["order-1"]
	if refund.FlowType != "RefundFlow" || refund.IndexedAttributes["case-status"] != "review" ||
		refund.Summary["summary-owner"] != "GetRefundSummary" {
		t.Fatalf("refund row = %+v", refund)
	}
	if order.FlowType != "OrderFlow" || order.IndexedAttributes["warehouse"] != "west" ||
		order.Summary["summary-owner"] != "GetOrderSummary" {
		t.Fatalf("order row = %+v", order)
	}
	if _, leaked := order.IndexedAttributes["case-status"]; leaked {
		t.Fatalf("order row carries the other Flow type's Attribute: %+v", order)
	}
}

func TestV2SearchRejectsAnUnanswerableFlowTypeScope(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantError string
	}{{
		name:      "flowType and flowTypes together",
		body:      `{"flowType":"RefundFlow","flowTypes":["OrderFlow"]}`,
		wantError: "send flowType or flowTypes, not both",
	}, {
		name:      "no Flow type",
		body:      `{"flowTypes":[]}`,
		wantError: `flowType "" has no valid`,
	}, {
		name:      "one Flow type without a contract",
		body:      `{"flowTypes":["RefundFlow","UnknownFlow"]}`,
		wantError: `flowType "UnknownFlow" has no valid`,
	}, {
		name: "an indexed Attribute filter across Flow types",
		body: `{
			"flowTypes":["RefundFlow","OrderFlow"],
			"filters":[{"field":"case-status","operator":"eq","values":["review"]}]
		}`,
		wantError: "accepts only built-in fields",
	}}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := &v2TestClient{}
			mux := http.NewServeMux()
			RegisterV2Handlers(mux, client, map[string]V2Definition{
				"OrderFlow": testV2Definition(), "RefundFlow": testV2Definition(),
			})

			response := performV2JSON(t, mux, http.MethodPost, "/api/v2/search", testCase.body)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
			}
			var failure errorResponse
			decodeV2Response(t, response, &failure)
			if !strings.Contains(failure.Error, testCase.wantError) {
				t.Fatalf("error = %q, want %q", failure.Error, testCase.wantError)
			}
			if len(client.searchRequests) != 0 {
				t.Fatalf("SearchFlows requests = %+v", client.searchRequests)
			}
		})
	}
}

func TestV2SearchRejectsInvalidWorkQueuePermission(t *testing.T) {
	client := &v2TestClient{}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": testV2Definition()})

	response := performV2JSON(t, mux, http.MethodPost, "/api/v2/search", `{
		"flowType":"RefundFlow",
		"workQueuePermissions":["refund.manage","Refund.Admin"],
		"filters":[]
	}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	if len(client.searchRequests) != 0 {
		t.Fatalf("SearchFlows requests = %+v", client.searchRequests)
	}
}

func TestV2DynamicRevisionRejectsStaleRequests(t *testing.T) {
	client := &v2TestClient{currentCaseStatus: "awaiting-manager"}
	mux := http.NewServeMux()
	loader := V2DefinitionLoader(func(context.Context) (V2DefinitionSnapshot, error) {
		return V2DefinitionSnapshot{
			Definitions: map[string]V2Definition{"RefundFlow": testV2Definition()},
			Revision:    "sha256:current",
		}, nil
	})
	RegisterDynamicV2Handlers(mux, client, loader, V2HandlerConfig{})
	catalogRequest := httptest.NewRequest(http.MethodGet, "/api/v2/catalog", nil)
	catalogResponse := httptest.NewRecorder()
	mux.ServeHTTP(catalogResponse, catalogRequest)
	if catalogResponse.Code != http.StatusOK || catalogResponse.Header().Get("ETag") != `"sha256:current"` ||
		!strings.Contains(catalogResponse.Body.String(), `"definitionRevision":"sha256:current"`) {
		t.Fatalf("catalog response = %d headers=%v body=%q", catalogResponse.Code, catalogResponse.Header(), catalogResponse.Body.String())
	}

	staleRequest := httptest.NewRequest(http.MethodPost, "/api/v2/search", strings.NewReader(`{
		"flowType":"RefundFlow","workQueuePermissions":["refund.manage"],"filters":[]
	}`))
	staleResponse := httptest.NewRecorder()
	mux.ServeHTTP(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusConflict || !strings.Contains(staleResponse.Body.String(), "FLOW_DEFINITION_CHANGED") {
		t.Fatalf("stale response = %d %q", staleResponse.Code, staleResponse.Body.String())
	}
	for _, stale := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/v2/display?flowType=RefundFlow&flowId=refund-1"},
		{method: http.MethodPatch, path: "/api/v2/display", body: `{
			"flowType":"RefundFlow","flowId":"refund-1","attributeKey":"operator-note","value":"done"
		}`},
		{method: http.MethodPost, path: "/api/v2/actions", body: `{
			"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund","input":{},"attributeSnapshot":{}
		}`},
	} {
		request := httptest.NewRequest(stale.method, stale.path, strings.NewReader(stale.body))
		request.Header.Set(V2DefinitionRevisionHeader, "sha256:stale")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "FLOW_DEFINITION_CHANGED") {
			t.Fatalf("stale %s %s response = %d %q", stale.method, stale.path, response.Code, response.Body.String())
		}
	}

}

func TestV2DynamicLocalActionUsesSelectedPermission(t *testing.T) {
	client := &v2TestClient{currentCaseStatus: "awaiting-manager"}
	mux := http.NewServeMux()
	RegisterDynamicV2Handlers(mux, client, func(context.Context) (V2DefinitionSnapshot, error) {
		return V2DefinitionSnapshot{
			Definitions: map[string]V2Definition{"RefundFlow": testV2Definition()},
			Revision:    "sha256:current",
		}, nil
	}, V2HandlerConfig{})

	request := httptest.NewRequest(http.MethodPost, "/api/v2/actions", strings.NewReader(`{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund",
		"workQueuePermissions":["refund.message"],"input":{},"attributeSnapshot":{}
	}`))
	request.Header.Set(V2DefinitionRevisionHeader, "sha256:current")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("local denied Action status = %d body=%q", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v2/actions", strings.NewReader(`{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund",
		"workQueuePermissions":["refund.manage"],"input":{},"attributeSnapshot":{}
	}`))
	request.Header.Set(V2DefinitionRevisionHeader, "sha256:current")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("local allowed Action status = %d body=%q", response.Code, response.Body.String())
	}
}

func TestV2FacadeRejectsRunIDAndStaleActionState(t *testing.T) {
	client := &v2TestClient{currentCaseStatus: "resolved"}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{
		"RefundFlow": testV2Definition(),
	})
	displayWithRunID := performV2JSON(
		t, mux, http.MethodGet,
		"/api/v2/display?flowType=RefundFlow&flowId=refund-1&runId=run-1", "",
	)
	if displayWithRunID.Code != http.StatusBadRequest {
		t.Fatalf("display runId status = %d body=%q", displayWithRunID.Code, displayWithRunID.Body.String())
	}
	withRunID := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","runId":"run-1","rpcName":"ApproveRefund","input":{}
	}`)
	if withRunID.Code != http.StatusBadRequest {
		t.Fatalf("runId status = %d body=%q", withRunID.Code, withRunID.Body.String())
	}

	stale := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund","input":{}
	}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale Action status = %d body=%q", stale.Code, stale.Body.String())
	}
}

func TestV2FacadeBuildsObjectActionFromUserAndAttributeInputs(t *testing.T) {
	client := &v2TestClient{
		currentCaseStatus: "awaiting-manager", currentGateRequest: "gate-current",
	}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{
		"RefundFlow": testV2Definition(),
	})
	response := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
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

	forged := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"RejectRefund",
		"input":{"reason":"duplicate","gateRequestKey":"forged"},
		"attributeSnapshot":{"gate-request-key":"gate-snapshot"}
	}`)
	if forged.Code != http.StatusBadRequest {
		t.Fatalf("forged hidden input status = %d body=%q", forged.Code, forged.Body.String())
	}
}

func TestV2SummaryLoadingBoundsConcurrencyAndIsolatesFailures(t *testing.T) {
	started := make(chan struct{}, 16)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	client := &v2TestClient{}
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
			return nil, status.Error(codes.Unauthenticated, "read credential file /private/secret-token: no such file")
		}
		return &dexpb.InvokeRPCResponse{Output: jsonDexValue(`{"charge-reference":"ok"}`)}, nil
	}
	handler := &v2Handler{client: client}
	flows := make([]v2Flow, 10)
	for index := range flows {
		flows[index].FlowID = "refund-" + string(rune('a'+index))
		flows[index].FlowType = "RefundFlow"
	}
	flows[9].FlowID = "failure"
	done := make(chan struct{})
	go func() {
		handler.loadSummaries(context.Background(), map[string]V2Definition{"RefundFlow": testV2Definition()}, flows)
		close(done)
	}()
	for index := 0; index < v2RPCConcurrency; index++ {
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
	if maximum.Load() != v2RPCConcurrency {
		t.Fatalf("maximum summary concurrency = %d", maximum.Load())
	}
	if strings.Contains(flows[9].SummaryError, "/private/secret-token") {
		t.Fatalf("Summary leaked a credential path: %s", flows[9].SummaryError)
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

func TestV2HydratesBlobBackedViewOutput(t *testing.T) {
	client := &v2TestClient{
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
	RegisterV2Handlers(mux, client, map[string]V2Definition{
		"RefundFlow": testV2Definition(),
	})

	searchResponse := performV2JSON(t, mux, http.MethodPost, "/api/v2/search", `{
		"flowType":"RefundFlow"
	}`)
	if searchResponse.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%q", searchResponse.Code, searchResponse.Body.String())
	}
	var searchResult v2SearchResponse
	decodeV2Response(t, searchResponse, &searchResult)
	if len(searchResult.Flows) != 1 || searchResult.Flows[0].SummaryError != "" {
		t.Fatalf("search result = %+v", searchResult)
	}
	if _, exists := searchResult.Flows[0].Summary["charge-reference"]; !exists {
		t.Fatalf("summary omitted charge-reference: %+v", searchResult.Flows[0].Summary)
	}

	displayResponse := performV2JSON(
		t, mux, http.MethodGet,
		"/api/v2/display?flowType=RefundFlow&flowId=refund-1", "",
	)
	if displayResponse.Code != http.StatusOK {
		t.Fatalf("display status = %d body=%q", displayResponse.Code, displayResponse.Body.String())
	}
	var displayResult v2DisplayResponse
	decodeV2Response(t, displayResponse, &displayResult)
	if displayResult.Display["operator-note"] != "reviewed" {
		t.Fatalf("display result = %+v", displayResult)
	}
	if len(client.loadBlobRequests) == 0 {
		t.Fatal("LoadBlobs was not called for blob-backed view output")
	}
}

func TestV2MissingViewBlobSurfacesRowError(t *testing.T) {
	client := &v2TestClient{}
	client.invokeRPCHandler = func(_ context.Context, _ *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		return &dexpb.InvokeRPCResponse{Output: blobObjDexValue("missing-blob")}, nil
	}
	handler := &v2Handler{client: client}
	flows := []v2Flow{{FlowID: "refund-1", FlowType: "RefundFlow"}}
	handler.loadSummaries(context.Background(), map[string]V2Definition{"RefundFlow": testV2Definition()}, flows)
	if flows[0].SummaryError == "" {
		t.Fatalf("missing blob did not surface a row error: %+v", flows[0])
	}
}

func TestV2ViewOutputContract(t *testing.T) {
	view := testV2Definition().Summary
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

func TestV2Int64InputPreservesPrecision(t *testing.T) {
	const maximumInt64 = "9223372036854775807"
	encoded, err := encodeV2Value(maximumInt64, "int64")
	if err != nil {
		t.Fatal(err)
	}
	if encoded.GetIntValue() != int64(9223372036854775807) {
		t.Fatalf("encoded int64 = %d", encoded.GetIntValue())
	}

	action := V2Action{Input: V2ActionInput{
		Kind: "object",
		Fields: []V2ActionInputField{{
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
	condition := V2ActionCondition{
		AttributeKey: "count", Operator: "in", Values: []interface{}{json.Number(maximumInt64)},
	}
	if !actionConditionMatches(condition, int64(9223372036854775807)) {
		t.Fatal("maximum int64 Action condition did not match")
	}
	decoded := v2DexValue(jsonDexValue(`{"count":9223372036854775807}`)).(map[string]interface{})
	if number, ok := decoded["count"].(json.Number); !ok || number.String() != maximumInt64 {
		t.Fatalf("decoded int64 = %#v", decoded["count"])
	}
	if responseValue := v2ResponseValue(decoded["count"], "int64"); responseValue != maximumInt64 {
		t.Fatalf("int64 response value = %#v", responseValue)
	}
}

func TestV2StartFlowUsesDefinitionSchemaAndServerRouting(t *testing.T) {
	client := &v2TestClient{}
	definition := testV2Definition()
	definition.Start = testV2StartDefinition()
	mux := http.NewServeMux()
	RegisterDynamicV2Handlers(mux, client, func(context.Context) (V2DefinitionSnapshot, error) {
		return V2DefinitionSnapshot{
			Definitions: map[string]V2Definition{"RefundFlow": definition},
			Revision:    "sha256:start",
		}, nil
	}, V2HandlerConfig{
		WorkerHealthChecker: healthyV2WorkerHealthChecker,
	})
	requestBody := `{
		"flowType":"RefundFlow","flowId":"refund-new","workerTargetPull":true,
		"input":{"count":18446744073709551615,"optional":null,"labels":["a","b"]}
	}`
	staleRequest := httptest.NewRequest(http.MethodPost, "/api/v2/start", strings.NewReader(requestBody))
	staleRequest.Header.Set(V2DefinitionRevisionHeader, "sha256:stale")
	staleResponse := httptest.NewRecorder()
	mux.ServeHTTP(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusConflict || len(client.startRequests) != 0 {
		t.Fatalf("stale response = %d %q", staleResponse.Code, staleResponse.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v2/start", strings.NewReader(requestBody))
	request.Header.Set(V2DefinitionRevisionHeader, "sha256:start")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	if len(client.startRequests) != 1 {
		t.Fatalf("StartFlow requests = %d", len(client.startRequests))
	}
	started := client.startRequests[0]
	if started.GetFlowId() != "refund-new" || started.GetFlowType() != "RefundFlow" ||
		started.GetStartStepType() != "StartRefund" || started.GetRequestId() == "" {
		t.Fatalf("StartFlow request = %+v", started)
	}
	workerTarget := started.GetFlowStartOptions().GetFlowConfigOverride().GetWorkerTarget()
	if !workerTarget.GetIsPullMode() {
		t.Fatalf("Worker target = %+v", workerTarget)
	}
	if payload := string(started.GetStepInput().GetObjValue().GetPayload()); payload !=
		`{"count":18446744073709551615,"optional":null,"labels":["a","b"]}` {
		t.Fatalf("Start input = %s", payload)
	}
}

func TestV2StartFlowRejectsBrowserRoutingOverrideAndInvalidInput(t *testing.T) {
	client := &v2TestClient{}
	definition := testV2Definition()
	definition.Start = testV2StartDefinition()
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": definition})
	for _, body := range []string{
		`{"flowType":"RefundFlow","flowId":"","workerTargetPull":true,"input":{"count":1,"labels":[]}}`,
		`{"flowType":"RefundFlow","flowId":"new","workerTargetPull":true,"isHeadlessAddress":true,"input":{"count":1,"labels":[]}}`,
		`{"flowType":"RefundFlow","flowId":"new","workerTargetPull":true,"input":{"count":1,"labels":[],"extra":true}}`,
		`{"flowType":"RefundFlow","flowId":"new","workerTargetPull":true,"input":{"count":18446744073709551616,"labels":[]}}`,
		`{"flowType":"RefundFlow","flowId":"new","workerTargetAddress":"worker:9000","input":{"count":1,"labels":[]}}`,
	} {
		response := performV2JSON(t, mux, http.MethodPost, "/api/v2/start", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d body=%q request=%s", response.Code, response.Body.String(), body)
		}
	}
	if len(client.startRequests) != 0 {
		t.Fatalf("StartFlow requests = %+v", client.startRequests)
	}
}

func TestV2StartFlowWorkerTargetSelection(t *testing.T) {
	const addressNotSupported = "workerTargetAddress is not supported"
	for _, testCase := range []struct {
		name         string
		serverEngine dexpb.ServerEngine
		workerTarget string
		wantStatus   int
		wantError    string
	}{
		{name: "address", workerTarget: `"workerTargetAddress":"worker:9000",`, wantStatus: http.StatusBadRequest, wantError: addressNotSupported},
		{name: "pull", workerTarget: `"workerTargetPull":true,`, wantStatus: http.StatusOK},
		{name: "both", workerTarget: `"workerTargetAddress":"worker:9000","workerTargetPull":true,`, wantStatus: http.StatusBadRequest, wantError: addressNotSupported},
		{name: "both with empty address", workerTarget: `"workerTargetAddress":"","workerTargetPull":true,`, wantStatus: http.StatusBadRequest, wantError: addressNotSupported},
		{name: "neither", wantStatus: http.StatusOK},
		{name: "neither on an unspecified engine", serverEngine: dexpb.ServerEngine_SERVER_ENGINE_UNSPECIFIED, wantStatus: http.StatusOK},
		{name: "explicit false", workerTarget: `"workerTargetPull":false,`, wantStatus: http.StatusBadRequest, wantError: "workerTargetPull must be true"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := &v2TestClient{serverEngine: testCase.serverEngine}
			definition := testV2Definition()
			definition.Start = testV2StartDefinition()
			healthCalls := 0
			mux := http.NewServeMux()
			RegisterDynamicV2Handlers(mux, client, func(context.Context) (V2DefinitionSnapshot, error) {
				return V2DefinitionSnapshot{Definitions: map[string]V2Definition{"RefundFlow": definition}}, nil
			}, V2HandlerConfig{
				WorkerHealthChecker: func(context.Context, string) error {
					healthCalls++
					return nil
				},
			})
			catalog := performV2JSON(t, mux, http.MethodGet, "/api/v2/catalog", "")
			if catalog.Code != http.StatusOK ||
				strings.Contains(catalog.Body.String(), `"startFlowWorkerTargetPull"`) ||
				!strings.Contains(catalog.Body.String(), fmt.Sprintf(`"serverEngine":%q`, serverEngineName(testCase.serverEngine))) {
				t.Fatalf("catalog = %d %q", catalog.Code, catalog.Body.String())
			}
			body := `{"flowType":"RefundFlow","flowId":"new",` + testCase.workerTarget + `"input":{"count":1,"labels":[]}}`
			response := performV2JSON(t, mux, http.MethodPost, "/api/v2/start", body)
			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
			}
			if testCase.wantError != "" && !strings.Contains(response.Body.String(), testCase.wantError) {
				t.Fatalf("target error = %q", response.Body.String())
			}
			if healthCalls != 0 {
				t.Fatalf("Worker health calls = %d", healthCalls)
			}
			if testCase.wantStatus != http.StatusOK {
				if len(client.startRequests) != 0 {
					t.Fatalf("StartFlow requests = %d", len(client.startRequests))
				}
				return
			}
			if len(client.startRequests) != 1 {
				t.Fatalf("StartFlow requests = %d", len(client.startRequests))
			}
			target := client.startRequests[0].GetFlowStartOptions().GetFlowConfigOverride().GetWorkerTarget()
			if !target.GetIsPullMode() {
				t.Fatalf("Worker target = %+v", target)
			}
		})
	}
}

func TestV2StartFlowDefaultsToPullOnServerZD(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		workerTarget string
		wantStatus   int
	}{
		{name: "omitted target", wantStatus: http.StatusOK},
		{name: "explicit pull", workerTarget: `"workerTargetPull":true,`, wantStatus: http.StatusOK},
		{name: "explicit false", workerTarget: `"workerTargetPull":false,`, wantStatus: http.StatusBadRequest},
		{name: "explicit address", workerTarget: `"workerTargetAddress":"worker:9000","bypassWorkerHealthCheck":true,`, wantStatus: http.StatusBadRequest},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := &v2TestClient{serverEngine: dexpb.ServerEngine_SERVER_ENGINE_ZD}
			definition := testV2Definition()
			definition.Start = testV2StartDefinition()
			mux := http.NewServeMux()
			RegisterDynamicV2Handlers(mux, client, func(context.Context) (V2DefinitionSnapshot, error) {
				return V2DefinitionSnapshot{Definitions: map[string]V2Definition{"RefundFlow": definition}}, nil
			}, V2HandlerConfig{WorkerHealthChecker: healthyV2WorkerHealthChecker})
			catalog := performV2JSON(t, mux, http.MethodGet, "/api/v2/catalog", "")
			if catalog.Code != http.StatusOK ||
				!strings.Contains(catalog.Body.String(), `"serverEngine":"zd"`) ||
				strings.Contains(catalog.Body.String(), `"startFlowWorkerTargetPull"`) {
				t.Fatalf("catalog = %d %q", catalog.Code, catalog.Body.String())
			}
			body := `{"flowType":"RefundFlow","flowId":"new",` + testCase.workerTarget + `"input":{"count":1,"labels":[]}}`
			response := performV2JSON(t, mux, http.MethodPost, "/api/v2/start", body)
			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
			}
			if testCase.wantStatus != http.StatusOK {
				return
			}
			target := client.startRequests[0].GetFlowStartOptions().GetFlowConfigOverride().GetWorkerTarget()
			if !target.GetIsPullMode() {
				t.Fatalf("Worker target = %+v", target)
			}
			if client.serverInfoCalls != 1 {
				t.Fatalf("GetServerInfo calls = %d", client.serverInfoCalls)
			}
		})
	}
}

func TestV2CatalogServerEngineSurvivesUnavailableFlowService(t *testing.T) {
	client := &v2TestClient{
		serverEngine:     dexpb.ServerEngine_SERVER_ENGINE_TEMPORAL,
		serverInfoErrors: []error{status.Error(codes.Unavailable, "FlowService is starting")},
	}
	mux := http.NewServeMux()
	RegisterDynamicV2Handlers(mux, client, func(context.Context) (V2DefinitionSnapshot, error) {
		return V2DefinitionSnapshot{Definitions: map[string]V2Definition{"RefundFlow": testV2Definition()}}, nil
	}, V2HandlerConfig{})
	for _, wantEngine := range []string{"unspecified", "temporal", "temporal"} {
		catalog := performV2JSON(t, mux, http.MethodGet, "/api/v2/catalog", "")
		if catalog.Code != http.StatusOK || !strings.Contains(catalog.Body.String(), `"serverEngine":"`+wantEngine+`"`) {
			t.Fatalf("catalog = %d %q, want engine %s", catalog.Code, catalog.Body.String(), wantEngine)
		}
	}
	if client.serverInfoCalls != 2 {
		t.Fatalf("GetServerInfo calls = %d, want a retry after failure and a cached success", client.serverInfoCalls)
	}

	legacyClient := &v2TestClient{serverInfoErrors: []error{status.Error(codes.Unimplemented, "unknown method")}}
	legacyMux := http.NewServeMux()
	RegisterDynamicV2Handlers(legacyMux, legacyClient, func(context.Context) (V2DefinitionSnapshot, error) {
		return V2DefinitionSnapshot{Definitions: map[string]V2Definition{"RefundFlow": testV2Definition()}}, nil
	}, V2HandlerConfig{})
	for range 2 {
		catalog := performV2JSON(t, legacyMux, http.MethodGet, "/api/v2/catalog", "")
		if catalog.Code != http.StatusOK || !strings.Contains(catalog.Body.String(), `"serverEngine":"unspecified"`) {
			t.Fatalf("legacy catalog = %d %q", catalog.Code, catalog.Body.String())
		}
	}
	if legacyClient.serverInfoCalls != 1 {
		t.Fatalf("legacy GetServerInfo calls = %d", legacyClient.serverInfoCalls)
	}
}

type blockingServerInfoClient struct {
	dexpb.FlowServiceClient
	lookups chan struct{}
	release chan struct{}
}

func (client *blockingServerInfoClient) GetServerInfo(
	context.Context,
	*emptypb.Empty,
	...grpc.CallOption,
) (*dexpb.ServerInfo, error) {
	client.lookups <- struct{}{}
	<-client.release
	return &dexpb.ServerInfo{ServerEngine: dexpb.ServerEngine_SERVER_ENGINE_ZD}, nil
}

func TestServerEngineResolverDoesNotSerializeLookups(t *testing.T) {
	client := &blockingServerInfoClient{lookups: make(chan struct{}, 2), release: make(chan struct{})}
	releaseLookups := sync.OnceFunc(func() { close(client.release) })
	defer releaseLookups()
	resolver := NewServerEngineResolver(client)
	engines := make(chan dexpb.ServerEngine, 2)
	for range 2 {
		go func() {
			engines <- resolver.ResolveServerEngine(context.Background())
		}()
	}
	for range 2 {
		select {
		case <-client.lookups:
		case <-time.After(5 * time.Second):
			t.Fatal("a GetServerInfo lookup waited for another lookup to finish")
		}
	}
	releaseLookups()
	for range 2 {
		if engine := <-engines; engine != dexpb.ServerEngine_SERVER_ENGINE_ZD {
			t.Fatalf("server engine = %s", engine)
		}
	}
}

func TestV2StartFlowHandlesScalarConflict(t *testing.T) {
	client := &v2TestClient{}
	client.startFlowHandler = func(context.Context, *dexpb.StartFlowRequest) (*dexpb.StartFlowResponse, error) {
		return nil, status.Error(codes.AlreadyExists, "Flow already exists")
	}
	definition := testV2Definition()
	definition.Start = &V2StartDefinition{
		StepType: "StartCount",
		Input:    V2StartInputSchema{Kind: "integer", Minimum: "-9223372036854775808", Maximum: "9223372036854775807"},
	}
	loader := func(context.Context) (V2DefinitionSnapshot, error) {
		return V2DefinitionSnapshot{Definitions: map[string]V2Definition{"RefundFlow": definition}}, nil
	}
	localMux := http.NewServeMux()
	RegisterDynamicV2Handlers(localMux, client, loader, V2HandlerConfig{
		WorkerHealthChecker: healthyV2WorkerHealthChecker,
	})
	conflict := performV2JSON(t, localMux, http.MethodPost, "/api/v2/start",
		`{"flowType":"RefundFlow","flowId":"new","workerTargetPull":true,"input":9223372036854775807}`)
	if conflict.Code != http.StatusConflict || client.startRequests[0].GetStepInput().GetIntValue() != int64(9223372036854775807) {
		t.Fatalf("conflict = %d %q request=%+v", conflict.Code, conflict.Body.String(), client.startRequests[0])
	}

}

func TestV2WorkerHealthReportsUnreachableAddressWithoutStartingFlows(t *testing.T) {
	client := &v2TestClient{}
	definition := testV2Definition()
	definition.Start = testV2StartDefinition()
	checkedAddresses := make([]string, 0, 1)
	checker := func(_ context.Context, address string) error {
		checkedAddresses = append(checkedAddresses, address)
		return errors.New("connection refused")
	}
	mux := http.NewServeMux()
	RegisterDynamicV2Handlers(mux, client, func(context.Context) (V2DefinitionSnapshot, error) {
		return V2DefinitionSnapshot{Definitions: map[string]V2Definition{"RefundFlow": definition}}, nil
	}, V2HandlerConfig{
		WorkerHealthChecker: checker,
	})

	health := performV2JSON(t, mux, http.MethodPost, "/api/v2/worker-health",
		`{"workerTargetAddress":" worker:9000 "}`)
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"healthy":false`) ||
		!strings.Contains(health.Body.String(), "connection refused") {
		t.Fatalf("health = %d %q", health.Code, health.Body.String())
	}

	bypassed := performV2JSON(t, mux, http.MethodPost, "/api/v2/start",
		`{"flowType":"RefundFlow","flowId":"new","workerTargetAddress":"worker:9000","bypassWorkerHealthCheck":true,"input":{"count":1,"labels":[]}}`)
	if bypassed.Code != http.StatusBadRequest || len(client.startRequests) != 0 {
		t.Fatalf("address start = %d %q requests=%d", bypassed.Code, bypassed.Body.String(), len(client.startRequests))
	}
	if len(checkedAddresses) != 1 || checkedAddresses[0] != "worker:9000" {
		t.Fatalf("checked addresses = %#v", checkedAddresses)
	}
}

func TestV2SummaryCallsUseFiveSecondTimeout(t *testing.T) {
	client := &v2TestClient{}
	client.invokeRPCHandler = func(ctx context.Context, _ *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("summary RPC has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > v2RPCTimeout {
			t.Fatalf("summary RPC timeout = %v", remaining)
		}
		return &dexpb.InvokeRPCResponse{Output: jsonDexValue(`{"charge-reference":"ok"}`)}, nil
	}
	flows := []v2Flow{{FlowID: "refund-1", FlowType: "RefundFlow"}}
	handler := &v2Handler{client: client}
	handler.loadSummaries(context.Background(), map[string]V2Definition{"RefundFlow": testV2Definition()}, flows)
	if flows[0].Summary["charge-reference"] != "ok" {
		t.Fatalf("summary = %+v", flows[0])
	}
}

func TestV2ActionReturnsRPCOutput(t *testing.T) {
	boolValue := func(value bool) *dexpb.Value {
		return &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: value}}
	}
	tests := []struct {
		name   string
		output *dexpb.Value
		want   string
	}{
		{name: "no output", output: nil, want: `{"invoked":true,"output":null}`},
		{name: "null", output: &dexpb.Value{
			Kind: &dexpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE},
		}, want: `{"invoked":true,"output":null}`},
		{name: "false stays a value", output: boolValue(false), want: `{"invoked":true,"output":false}`},
		{name: "true", output: boolValue(true), want: `{"invoked":true,"output":true}`},
		{name: "string", output: stringDexValue("done"), want: `{"invoked":true,"output":"done"}`},
		{name: "int64 keeps precision", output: &dexpb.Value{
			Kind: &dexpb.Value_IntValue{IntValue: 9223372036854775807},
		}, want: `{"invoked":true,"output":"9223372036854775807"}`},
		{name: "double", output: &dexpb.Value{
			Kind: &dexpb.Value_DoubleValue{DoubleValue: 2},
		}, want: `{"invoked":true,"output":2}`},
		{name: "object keeps number digits", output: jsonDexValue(`{"count":9007199254740993}`),
			want: `{"invoked":true,"output":{"count":9007199254740993}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &v2TestClient{currentCaseStatus: "awaiting-manager"}
			client.invokeRPCHandler = func(context.Context, *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
				return &dexpb.InvokeRPCResponse{Output: test.output}, nil
			}
			mux := http.NewServeMux()
			RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": testV2Definition()})

			response := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
				"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund","input":{}
			}`)
			if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != test.want {
				t.Fatalf("Action status = %d body=%q, want %q", response.Code, response.Body.String(), test.want)
			}
			if len(client.rpcRequests) != 1 {
				t.Fatalf("RPC requests = %+v", client.rpcRequests)
			}
		})
	}
}

func TestV2ActionSendsTheRPCsDeclaredLocks(t *testing.T) {
	definition := testV2Definition()
	definition.RPCLocks = map[string]V2RPCLocks{
		"ApproveRefund": {AttributeKeys: []string{"case-status", "gate-request-key"}},
	}
	client := &v2TestClient{currentCaseStatus: "awaiting-manager"}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": definition})

	response := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund","input":{}
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("Action status = %d body=%s", response.Code, response.Body.String())
	}
	if len(client.rpcRequests) != 1 {
		t.Fatalf("RPC requests = %+v", client.rpcRequests)
	}
	request := client.rpcRequests[0]
	if got := strings.Join(request.GetLockAttributeKeys(), ","); got != "case-status,gate-request-key" {
		t.Fatalf("lock keys = %q", got)
	}
	if !request.GetIsTransactional() || request.GetRequestId() == "" {
		t.Fatalf("request = %+v", request)
	}
}

func TestV2ActionWithoutLocksStaysNonTransactional(t *testing.T) {
	client := &v2TestClient{currentCaseStatus: "awaiting-manager"}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": testV2Definition()})

	response := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund","input":{}
	}`)
	if response.Code != http.StatusOK || len(client.rpcRequests) != 1 {
		t.Fatalf("Action status = %d requests=%+v", response.Code, client.rpcRequests)
	}
	if request := client.rpcRequests[0]; len(request.GetLockAttributeKeys()) != 0 || request.GetIsTransactional() {
		t.Fatalf("request = %+v", request)
	}
}

func TestV2ActionRefusesAnRPCWhoseLocksAreUnresolved(t *testing.T) {
	definition := testV2Definition()
	definition.RPCLocks = map[string]V2RPCLocks{
		"ApproveRefund": {
			AttributeKeys:    []string{"case-status"},
			UnresolvedReason: `it locks an instance of AttributeMap "progress"`,
		},
	}
	client := &v2TestClient{currentCaseStatus: "awaiting-manager"}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": definition})

	response := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund","input":{}
	}`)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Action status = %d body=%s", response.Code, response.Body.String())
	}
	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "rpc_locks_unresolved" || !strings.Contains(body.Error, `AttributeMap "progress"`) {
		t.Fatalf("body = %+v", body)
	}
	for _, request := range client.rpcRequests {
		if request.GetRpcName() == "ApproveRefund" {
			t.Fatalf("Action invoked its RPC without its locks: %+v", request)
		}
	}
}

func TestV2ActionAnswersALockConflictWithConflict(t *testing.T) {
	definition := testV2Definition()
	definition.RPCLocks = map[string]V2RPCLocks{"ApproveRefund": {AttributeKeys: []string{"case-status"}}}
	client := &v2TestClient{currentCaseStatus: "awaiting-manager"}
	client.invokeRPCHandler = func(context.Context, *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		return nil, status.Error(codes.Aborted, "attribute lock is held: case-status")
	}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": definition})

	response := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund","input":{}
	}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("Action status = %d body=%s", response.Code, response.Body.String())
	}
	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "rpc_lock_conflict" || body.GRPCCode == nil || *body.GRPCCode != int32(codes.Aborted) {
		t.Fatalf("body = %+v", body)
	}
}

func TestV2ViewsSendTheirRPCsDeclaredLocks(t *testing.T) {
	definition := testV2Definition()
	definition.RPCLocks = map[string]V2RPCLocks{"GetDexSummary": {AttributeKeys: []string{"charge-reference"}}}
	client := &v2TestClient{}
	flows := []v2Flow{{FlowID: "refund-1", FlowType: "RefundFlow"}}
	handler := &v2Handler{client: client}
	handler.loadSummaries(context.Background(), map[string]V2Definition{"RefundFlow": definition}, flows)
	if flows[0].SummaryError != "" || len(client.rpcRequests) != 1 {
		t.Fatalf("flows = %+v requests = %+v", flows, client.rpcRequests)
	}
	if request := client.rpcRequests[0]; strings.Join(request.GetLockAttributeKeys(), ",") != "charge-reference" || !request.GetIsTransactional() {
		t.Fatalf("summary request = %+v", request)
	}

	definition.RPCLocks = map[string]V2RPCLocks{"GetDexSummary": {UnresolvedReason: "unreadable lock"}}
	flows = []v2Flow{{FlowID: "refund-1", FlowType: "RefundFlow"}}
	handler.loadSummaries(context.Background(), map[string]V2Definition{"RefundFlow": definition}, flows)
	if !strings.Contains(flows[0].SummaryError, "unreadable lock") || len(client.rpcRequests) != 1 {
		t.Fatalf("flows = %+v requests = %+v", flows, client.rpcRequests)
	}
}

func TestV2ActionOutputReadsBlobAndNeverFailsAfterTheRPCRan(t *testing.T) {
	client := &v2TestClient{
		currentCaseStatus: "awaiting-manager",
		blobs:             map[string]*dexpb.Value{"stored": jsonDexValue(`{"accepted":true}`)},
	}
	blobID := "stored"
	client.invokeRPCHandler = func(context.Context, *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		return &dexpb.InvokeRPCResponse{Output: blobObjDexValue(blobID)}, nil
	}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": testV2Definition()})
	body := `{"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund","input":{}}`

	hydrated := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", body)
	want := `{"invoked":true,"output":{"accepted":true}}`
	if hydrated.Code != http.StatusOK || strings.TrimSpace(hydrated.Body.String()) != want {
		t.Fatalf("hydrated Action status = %d body=%q", hydrated.Code, hydrated.Body.String())
	}

	blobID = "missing"
	unavailable := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", body)
	want = `{"invoked":true,"output":{"__dexBlobReference":{"id":"missing","kind":"object"}}}`
	if unavailable.Code != http.StatusOK || strings.TrimSpace(unavailable.Body.String()) != want {
		t.Fatalf("unavailable blob Action status = %d body=%q", unavailable.Code, unavailable.Body.String())
	}
}

func TestV2ActionConditionIsStillEnforced(t *testing.T) {
	client := &v2TestClient{currentCaseStatus: "resolved"}
	client.invokeRPCHandler = func(context.Context, *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		return &dexpb.InvokeRPCResponse{Output: stringDexValue("ran")}, nil
	}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": testV2DefinitionWithConditionlessAction()})
	body := `{"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ApproveRefund","input":{}}`

	stale := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", body)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale Action status = %d body=%q", stale.Code, stale.Body.String())
	}
	if len(client.rpcRequests) != 0 {
		t.Fatalf("stale Action invoked its RPC: %+v", client.rpcRequests)
	}
	if len(client.attributeRequests) != 1 || len(client.attributeRequests[0].GetKeys()) != 1 ||
		client.attributeRequests[0].GetKeys()[0] != "case-status" {
		t.Fatalf("condition reads = %+v", client.attributeRequests)
	}

	client.currentCaseStatus = "awaiting-manager"
	current := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", body)
	if current.Code != http.StatusOK {
		t.Fatalf("current Action status = %d body=%q", current.Code, current.Body.String())
	}
}

func TestV2ActionWithoutConditionLeavesTheCheckToItsRPC(t *testing.T) {
	client := &v2TestClient{currentCaseStatus: "resolved"}
	client.invokeRPCHandler = func(_ context.Context, request *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		if request.GetRpcName() == "GetDexDisplay" {
			return &dexpb.InvokeRPCResponse{Output: jsonDexValue(`{"operator-note":"n","case-status":"resolved"}`)}, nil
		}
		return &dexpb.InvokeRPCResponse{Output: &dexpb.Value{Kind: &dexpb.Value_BoolValue{}}}, nil
	}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": testV2DefinitionWithConditionlessAction()})
	displayPath := "/api/v2/display?flowType=RefundFlow&flowId=refund-1"

	display := performV2JSON(t, mux, http.MethodGet, displayPath, "")
	if display.Code != http.StatusOK {
		t.Fatalf("display status = %d body=%q", display.Code, display.Body.String())
	}
	var active v2DisplayResponse
	decodeV2Response(t, display, &active)
	if len(active.EligibleActions) != 1 || active.EligibleActions[0] != "ConfirmCase" {
		t.Fatalf("eligible Actions = %+v", active.EligibleActions)
	}
	for _, request := range client.attributeRequests {
		for _, key := range request.GetKeys() {
			if key == "" {
				t.Fatalf("display read an empty Attribute key: %+v", request)
			}
		}
	}

	client.attributeRequests = nil
	client.rpcRequests = nil
	action := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ConfirmCase",
		"input":{"callId":"call-1"},"attributeSnapshot":{}
	}`)
	if action.Code != http.StatusOK || strings.TrimSpace(action.Body.String()) != `{"invoked":true,"output":false}` {
		t.Fatalf("Action status = %d body=%q", action.Code, action.Body.String())
	}
	if len(client.attributeRequests) != 0 {
		t.Fatalf("conditionless Action read Attributes: %+v", client.attributeRequests)
	}
	var input map[string]interface{}
	if err := json.Unmarshal(client.rpcRequests[0].GetInput().GetObjValue().GetPayload(), &input); err != nil {
		t.Fatal(err)
	}
	if input["callId"] != "call-1" {
		t.Fatalf("Action input = %+v", input)
	}

	client.flowStatus = dexpb.FlowStatus_FLOW_STATUS_COMPLETED
	client.rpcRequests = nil
	display = performV2JSON(t, mux, http.MethodGet, displayPath, "")
	var finished v2DisplayResponse
	decodeV2Response(t, display, &finished)
	if len(finished.EligibleActions) != 0 {
		t.Fatalf("finished Flow eligible Actions = %+v", finished.EligibleActions)
	}
	inactive := performV2JSON(t, mux, http.MethodPost, "/api/v2/actions", `{
		"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ConfirmCase","input":{"callId":"call-1"}
	}`)
	if inactive.Code == http.StatusOK {
		t.Fatalf("finished Flow Action body=%q", inactive.Body.String())
	}
	for _, request := range client.rpcRequests {
		if request.GetRpcName() == "ConfirmCase" {
			t.Fatal("finished Flow invoked the Action RPC")
		}
	}
}

func TestV2ActionWithoutConditionKeepsPermissionCheck(t *testing.T) {
	client := &v2TestClient{}
	mux := http.NewServeMux()
	RegisterDynamicV2Handlers(mux, client, func(context.Context) (V2DefinitionSnapshot, error) {
		return V2DefinitionSnapshot{
			Definitions: map[string]V2Definition{"RefundFlow": testV2DefinitionWithConditionlessAction()},
			Revision:    "sha256:current",
		}, nil
	}, V2HandlerConfig{})
	invoke := func(permission string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v2/actions", strings.NewReader(`{
			"flowType":"RefundFlow","flowId":"refund-1","rpcName":"ConfirmCase",
			"workQueuePermissions":["`+permission+`"],"input":{"callId":"call-1"}
		}`))
		request.Header.Set(V2DefinitionRevisionHeader, "sha256:current")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}

	if denied := invoke("refund.message"); denied.Code != http.StatusForbidden {
		t.Fatalf("denied Action status = %d body=%q", denied.Code, denied.Body.String())
	}
	if len(client.rpcRequests) != 0 {
		t.Fatalf("denied Action invoked its RPC: %+v", client.rpcRequests)
	}
	if allowed := invoke("refund.manage"); allowed.Code != http.StatusOK {
		t.Fatalf("allowed Action status = %d body=%q", allowed.Code, allowed.Body.String())
	}
}

func TestV2ConditionlessActionStaysOutOfPermissionMappings(t *testing.T) {
	client := &v2TestClient{currentCaseStatus: "awaiting-manager"}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": testV2DefinitionWithConditionlessAction()})

	edit := performV2JSON(
		t, mux, http.MethodPatch, "/api/v2/display",
		`{"flowType":"RefundFlow","flowId":"refund-1","attributeKey":"case-status","value":"review"}`,
	)
	if edit.Code != http.StatusOK {
		t.Fatalf("edit status = %d body=%q", edit.Code, edit.Body.String())
	}
	mappings := client.setRequests[0].GetActionPermissionMappings().GetMappings()
	if len(mappings) != len(testV2Definition().Actions) {
		t.Fatalf("permission mappings = %+v", mappings)
	}
	for _, mapping := range mappings {
		if mapping.GetAttributeKey() == "" {
			t.Fatalf("permission mapping without an Attribute: %+v", mappings)
		}
	}

	catalog := performV2JSON(t, mux, http.MethodGet, "/api/v2/catalog", "")
	if catalog.Code != http.StatusOK || strings.Contains(catalog.Body.String(), `"condition":null`) {
		t.Fatalf("catalog status = %d body=%q", catalog.Code, catalog.Body.String())
	}
}

func testV2Definition() V2Definition {
	return V2Definition{
		IndexedAttributes: []V2IndexedAttribute{{
			AttributeKey: "case-status", IndexKey: "case-status-index",
			IndexType: "keyword", ValueType: "string", Description: "Case status",
		}},
		Summary: V2RPCView{RPCName: "GetDexSummary", Fields: []V2ViewField{{
			AttributeKey: "charge-reference", ValueType: "string", Description: "Charge reference",
		}}},
		Display: V2RPCView{RPCName: "GetDexDisplay", Fields: []V2ViewField{{
			AttributeKey: "operator-note", ValueType: "string", Editable: true, Description: "Operator note",
		}, {
			AttributeKey: "case-status", ValueType: "string", Editable: true, Description: "Case status",
		}}},
		Actions: []V2Action{{
			RPCName: "ApproveRefund", Label: "Approve", RequiredPermission: "refund.manage",
			Condition: &V2ActionCondition{
				AttributeKey: "case-status", Operator: "in", Values: []interface{}{"awaiting-manager"},
			},
			Input: V2ActionInput{Kind: "none"},
		}, {
			RPCName: "RejectRefund", Label: "Reject", RequiredPermission: "refund.manage",
			Condition: &V2ActionCondition{
				AttributeKey: "case-status", Operator: "in", Values: []interface{}{"awaiting-manager"},
			},
			Input: V2ActionInput{Kind: "object", Fields: []V2ActionInputField{{
				FieldName: "reason", ValueType: "string", Source: "user", Capture: "qr-code", Required: true,
				Description: "Rejection reason",
			}, {
				FieldName: "gateRequestKey", ValueType: "string", Source: "attribute",
				AttributeKey: "gate-request-key", Required: true, Description: "Approval gate",
			}}},
		}, {
			RPCName: "NotifyCustomer", Label: "Notify", RequiredPermission: "refund.message",
			Condition: &V2ActionCondition{
				AttributeKey: "message-priority", Operator: "in",
				Values: []interface{}{json.Number("9223372036854775807")},
			},
			Input: V2ActionInput{Kind: "none"},
		}},
	}
}

func testV2DefinitionWithConditionlessAction() V2Definition {
	definition := testV2Definition()
	definition.Actions = append(definition.Actions, V2Action{
		RPCName: "ConfirmCase", Label: "Confirm", RequiredPermission: "refund.manage",
		Input: V2ActionInput{Kind: "object", Fields: []V2ActionInputField{{
			FieldName: "callId", ValueType: "string", Source: "user", Required: true, Description: "Call",
		}}},
	})
	return definition
}

func testV2StartDefinition() *V2StartDefinition {
	return &V2StartDefinition{
		StepType: "StartRefund",
		Input: V2StartInputSchema{Kind: "object", Fields: []V2StartInputField{
			{
				Name: "count", Required: true,
				Schema: V2StartInputSchema{Kind: "integer", Minimum: "0", Maximum: "18446744073709551615"},
			},
			{Name: "optional", Schema: V2StartInputSchema{Kind: "string", Nullable: true}},
			{
				Name: "labels", Required: true,
				Schema: V2StartInputSchema{Kind: "array", Nullable: true, Items: &V2StartInputSchema{Kind: "string"}},
			},
		}},
	}
}

func healthyV2WorkerHealthChecker(context.Context, string) error {
	return nil
}

func assertV2RequestsOmitRunID(t *testing.T, client *v2TestClient) {
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

func performV2JSON(
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

func decodeV2Response(t *testing.T, response *httptest.ResponseRecorder, target interface{}) {
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

func TestV2ViewWithoutFieldsInvokesNoRPC(t *testing.T) {
	client := &v2TestClient{
		searchEntries: []*dexpb.SearchFlowsResponseEntry{{
			FlowId: "session-1", FlowType: "RefundFlow", FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
		}},
	}
	client.invokeRPCHandler = func(_ context.Context, request *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		if request.GetRpcName() == "GetDexSummary" || request.GetRpcName() == "GetDexDisplay" {
			return nil, status.Error(codes.DeadlineExceeded, "no Worker registers this RPC")
		}
		return &dexpb.InvokeRPCResponse{Output: &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: true}}}, nil
	}
	definition := V2Definition{
		IndexedAttributes: []V2IndexedAttribute{},
		Summary:           V2RPCView{RPCName: "GetDexSummary", Fields: []V2ViewField{}},
		Display:           V2RPCView{RPCName: "GetDexDisplay", Fields: []V2ViewField{}},
		Actions: []V2Action{{
			RPCName: "resolve_interrupt", Label: "Resolve interrupt", RequiredPermission: "agent.operate",
			Input: V2ActionInput{Kind: "object", Fields: []V2ActionInputField{{
				FieldName: "interrupt_id", ValueType: "string", Source: "user", Required: true, Description: "Interrupt ID",
			}}},
		}},
	}
	mux := http.NewServeMux()
	RegisterV2Handlers(mux, client, map[string]V2Definition{"RefundFlow": definition})

	search := performV2JSON(t, mux, http.MethodPost, "/api/v2/search", `{"flowType":"RefundFlow"}`)
	var searched v2SearchResponse
	decodeV2Response(t, search, &searched)
	if search.Code != http.StatusOK || len(searched.Flows) != 1 || searched.Flows[0].SummaryError != "" {
		t.Fatalf("search status = %d body=%q", search.Code, search.Body.String())
	}
	if len(searched.Flows[0].Summary) != 0 {
		t.Fatalf("summary = %+v", searched.Flows[0].Summary)
	}

	display := performV2JSON(t, mux, http.MethodGet, "/api/v2/display?flowType=RefundFlow&flowId=session-1", "")
	if display.Code != http.StatusOK {
		t.Fatalf("display status = %d body=%q", display.Code, display.Body.String())
	}
	var displayed v2DisplayResponse
	decodeV2Response(t, display, &displayed)
	if len(displayed.Display) != 0 || len(displayed.EligibleActions) != 1 {
		t.Fatalf("display = %+v", displayed)
	}
	for _, request := range client.rpcRequests {
		t.Fatalf("a view without fields invoked RPC %q", request.GetRpcName())
	}
}
