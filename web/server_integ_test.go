// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	dexweb "github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type flowService struct {
	dexpb.UnimplementedFlowServiceServer
	waitStarted        chan struct{}
	waitCanceled       chan struct{}
	searchRequests     chan *dexpb.SearchFlowsRequest
	loadBlobsRequests  chan *dexpb.LoadBlobsRequest
	loadBlobsError     error
	stopRequests       chan *dexpb.StopFlowRequest
	timeTravelRequests chan *dexpb.ResetFlowRequest
}

func TestWebServerTimeTravelsToHistoryEvent(t *testing.T) {
	service := &flowService{timeTravelRequests: make(chan *dexpb.ResetFlowRequest, 1)}
	harness := newHarness(t, service)
	response := postJSON(
		t,
		harness.http.URL+"/api/flows/time-travel",
		`{"flowId":"checkout-1","runId":"run-1","timeTravelType":1,"historyEventId":42,"reason":"retry fixed code"}`,
	)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("time travel status = %d body=%q", response.StatusCode, readBody(t, response))
	}
	request := <-service.timeTravelRequests
	if request.GetFlowId() != "checkout-1" || request.GetRunId() != "run-1" {
		t.Fatalf("unexpected execution: %+v", request)
	}
	if request.GetResetType() != dexpb.FlowResetType_FLOW_RESET_TYPE_HISTORY_EVENT_ID || request.GetHistoryEventId() != 42 {
		t.Fatalf("unexpected time travel point: %+v", request)
	}
}

func TestWebServerBridgesDexAndServesSPA(t *testing.T) {
	harness := newHarness(t, &flowService{})

	searchResponse := postJSON(t, harness.http.URL+"/api/flows/search", `{"pageSize":10}`)
	defer searchResponse.Body.Close()
	if searchResponse.StatusCode != http.StatusOK {
		t.Fatalf("search status = %d", searchResponse.StatusCode)
	}
	var searchResult struct {
		Flows []struct {
			FlowID   string `json:"flowId"`
			FlowType string `json:"flowType"`
		} `json:"flows"`
	}
	decodeResponse(t, searchResponse, &searchResult)
	if len(searchResult.Flows) != 1 || searchResult.Flows[0].FlowID != "checkout-1" {
		t.Fatalf("unexpected search result: %+v", searchResult)
	}

	for _, path := range []string{"/", "/flows/checkout-1/run-1"} {
		response := get(t, harness.http.URL+path)
		body := readBody(t, response)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || !strings.Contains(body, `<div id="root"></div>`) {
			t.Fatalf("SPA %s: status=%d body=%q", path, response.StatusCode, body)
		}
	}

	missingAPI := get(t, harness.http.URL+"/api/unknown")
	missingBody := readBody(t, missingAPI)
	missingAPI.Body.Close()
	if missingAPI.StatusCode != http.StatusNotFound || !strings.Contains(missingBody, "API route not found") {
		t.Fatalf("unknown API: status=%d body=%q", missingAPI.StatusCode, missingBody)
	}
}

func TestWebServerMapsGRPCErrors(t *testing.T) {
	harness := newHarness(t, &flowService{})
	response := get(t, harness.http.URL+"/api/flows/summary?flowId=invalid")
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("summary status = %d", response.StatusCode)
	}
	var result struct {
		Error    string `json:"error"`
		GRPCCode int32  `json:"grpcCode"`
	}
	decodeResponse(t, response, &result)
	if result.Error != "invalid flow" || result.GRPCCode != int32(codes.InvalidArgument) {
		t.Fatalf("unexpected error response: %+v", result)
	}
}

func TestWebServerScopesSearchesToEngineWorkflows(t *testing.T) {
	service := &flowService{
		searchRequests: make(chan *dexpb.SearchFlowsRequest, 2),
	}
	harness := newHarness(t, service)

	testCases := []struct {
		name      string
		body      string
		wantQuery string
	}{
		{
			name:      "empty query",
			body:      `{"pageSize":10}`,
			wantQuery: `WorkflowType = "Engine"`,
		},
		{
			name:      "user query",
			body:      `{"query":"ExecutionStatus = \"Running\"","pageSize":10}`,
			wantQuery: `(ExecutionStatus = "Running") AND (WorkflowType = "Engine")`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			response := postJSON(t, harness.http.URL+"/api/flows/search", testCase.body)
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("search status = %d", response.StatusCode)
			}
			request := <-service.searchRequests
			if request.GetQuery() != testCase.wantQuery {
				t.Fatalf("query = %q, want %q", request.GetQuery(), testCase.wantQuery)
			}
		})
	}
}

func TestWebServerLoadsBlobs(t *testing.T) {
	service := &flowService{
		loadBlobsRequests: make(chan *dexpb.LoadBlobsRequest, 1),
	}
	harness := newHarness(t, service)

	blobResponse := postJSON(t, harness.http.URL+"/api/blobs/load", `{
        "values": [
          {"id":"string-id","kind":"string"},
          {"id":"object-id","kind":"object"},
          {"id":"string-id","kind":"string"}
        ]
      }`)
	defer blobResponse.Body.Close()
	if blobResponse.StatusCode != http.StatusOK {
		t.Fatalf("blob status = %d", blobResponse.StatusCode)
	}
	var blobResult struct {
		Values map[string]interface{} `json:"values"`
	}
	decodeResponse(t, blobResponse, &blobResult)
	if blobResult.Values["string:string-id"] != "loaded string" {
		t.Fatalf("unexpected string blob: %+v", blobResult.Values)
	}
	object, ok := blobResult.Values["object:object-id"].(map[string]interface{})
	if !ok || object["answer"] != float64(42) {
		t.Fatalf("unexpected object blob: %+v", blobResult.Values)
	}
	loadRequest := <-service.loadBlobsRequests
	if len(loadRequest.GetValues()) != 2 ||
		loadRequest.GetValues()[0].GetInternalBlobIdForStringValue() != "string-id" ||
		loadRequest.GetValues()[1].GetInternalBlobIdForObjValue() != "object-id" {
		t.Fatalf("unexpected LoadBlobs request: %+v", loadRequest)
	}

	errorHarness := newHarness(t, &flowService{
		loadBlobsError: status.Error(codes.Unavailable, "blob store offline"),
	})
	errorResponse := postJSON(
		t,
		errorHarness.http.URL+"/api/blobs/load",
		`{"values":[{"id":"string-id","kind":"string"}]}`,
	)
	defer errorResponse.Body.Close()
	if errorResponse.StatusCode != http.StatusBadGateway {
		t.Fatalf("blob error status = %d", errorResponse.StatusCode)
	}
	var mappedError struct {
		Error    string `json:"error"`
		GRPCCode int32  `json:"grpcCode"`
	}
	decodeResponse(t, errorResponse, &mappedError)
	if mappedError.Error != "Value blob unavailable" || mappedError.GRPCCode != int32(codes.Unavailable) {
		t.Fatalf("unexpected blob error response: %+v", mappedError)
	}
}

func TestWaitRequestPropagatesCancellation(t *testing.T) {
	service := &flowService{
		waitStarted:  make(chan struct{}),
		waitCanceled: make(chan struct{}),
	}
	harness := newHarness(t, service)
	requestCtx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodGet,
		harness.http.URL+"/api/flows/wait?flowId=flow&runId=run&nextInternalEventId=1",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	requestFinished := make(chan error, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(request)
		if response != nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				requestErr = errors.Join(requestErr, closeErr)
			}
		}
		requestFinished <- requestErr
	}()
	select {
	case <-service.waitStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("gRPC wait did not start")
	}
	cancel()
	select {
	case <-service.waitCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("gRPC wait context was not canceled")
	}
	select {
	case requestErr := <-requestFinished:
		if requestErr == nil {
			t.Fatal("canceled HTTP request unexpectedly succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled HTTP request did not return")
	}
}

func TestWebServerStopsFlow(t *testing.T) {
	service := &flowService{
		stopRequests: make(chan *dexpb.StopFlowRequest, 1),
	}
	harness := newHarness(t, service)

	response := postJSON(t, harness.http.URL+"/api/flows/stop", `{
		"flowId":"checkout-1",
		"runId":"run-1",
		"stopType":3,
		"reason":"operator fail"
	}`)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d", response.StatusCode)
	}
	stopRequest := <-service.stopRequests
	if stopRequest.GetFlowId() != "checkout-1" ||
		stopRequest.GetRunId() != "run-1" ||
		stopRequest.GetStopType() != dexpb.StopType_STOP_TYPE_FAIL ||
		stopRequest.GetReason() != "operator fail" {
		t.Fatalf("unexpected StopFlow request: %+v", stopRequest)
	}

	missingReason := postJSON(t, harness.http.URL+"/api/flows/stop", `{
		"flowId":"checkout-1",
		"runId":"run-1",
		"stopType":2,
		"reason":" "
	}`)
	defer missingReason.Body.Close()
	if missingReason.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing reason status = %d", missingReason.StatusCode)
	}
}

func (s *flowService) SearchFlows(
	_ context.Context,
	request *dexpb.SearchFlowsRequest,
) (*dexpb.SearchFlowsResponse, error) {
	if s.searchRequests != nil {
		s.searchRequests <- request
	}
	return &dexpb.SearchFlowsResponse{
		FlowRuns: []*dexpb.SearchFlowsResponseEntry{{
			FlowId:   "checkout-1",
			RunId:    "run-1",
			FlowType: "checkout",
		}},
	}, nil
}

func (s *flowService) GetFlowSummary(
	context.Context,
	*dexpb.GetFlowSummaryRequest,
) (*dexpb.GetFlowSummaryResponse, error) {
	return nil, status.Error(codes.InvalidArgument, "invalid flow")
}

func (s *flowService) LoadBlobs(
	_ context.Context,
	request *dexpb.LoadBlobsRequest,
) (*dexpb.LoadBlobsResponse, error) {
	if s.loadBlobsError != nil {
		return nil, s.loadBlobsError
	}
	s.loadBlobsRequests <- request
	return &dexpb.LoadBlobsResponse{Values: map[string]*dexpb.Value{
		"string-id": {Kind: &dexpb.Value_StringValue{StringValue: "loaded string"}},
		"object-id": {Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: "json",
			Payload:  []byte(`{"answer":42}`),
		}}},
	}}, nil
}

func (s *flowService) WaitForHistoryEvent(
	ctx context.Context,
	request *dexpb.WaitForHistoryEventRequest,
) (*dexpb.WaitForHistoryEventResponse, error) {
	close(s.waitStarted)
	<-ctx.Done()
	close(s.waitCanceled)
	return nil, ctx.Err()
}

func (s *flowService) StopFlow(
	_ context.Context,
	request *dexpb.StopFlowRequest,
) (*emptypb.Empty, error) {
	s.stopRequests <- request
	return &emptypb.Empty{}, nil
}

func (s *flowService) ResetFlow(
	_ context.Context,
	request *dexpb.ResetFlowRequest,
) (*dexpb.ResetFlowResponse, error) {
	s.timeTravelRequests <- request
	return &dexpb.ResetFlowResponse{RunId: "run-2"}, nil
}

type harness struct {
	http *httptest.Server
}

func newHarness(t *testing.T, service dexpb.FlowServiceServer) *harness {
	t.Helper()
	grpcListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	dexpb.RegisterFlowServiceServer(grpcServer, service)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- grpcServer.Serve(grpcListener)
	}()
	connection, err := grpc.NewClient(
		grpcListener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		grpcServer.Stop()
		t.Fatal(err)
	}
	webServer := dexweb.NewServer(
		&dexweb.Config{BindAddress: "127.0.0.1", Port: dexweb.DefaultPort},
		dexpb.NewFlowServiceClient(connection),
		assets.Files,
	)
	httpServer := httptest.NewServer(webServer.Handler())
	t.Cleanup(func() {
		httpServer.Close()
		if err := connection.Close(); err != nil {
			t.Errorf("close gRPC connection: %v", err)
		}
		grpcServer.Stop()
		if err := <-serveErrors; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("gRPC server: %v", err)
		}
	})
	return &harness{http: httpServer}
}

func postJSON(t *testing.T, url string, body string) *http.Response {
	t.Helper()
	response, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, value interface{}) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(value); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
