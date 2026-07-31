// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const maxRequestBytes = 1 << 20

type handler struct {
	client dexpb.FlowServiceClient
}

func RegisterHandlers(mux *http.ServeMux, client dexpb.FlowServiceClient) {
	if mux == nil {
		panic("HTTP mux must not be nil")
	}
	if client == nil {
		panic("Dex FlowService client must not be nil")
	}
	handler := &handler{client: client}
	mux.HandleFunc("POST /api/flows/search", handler.searchFlows)
	mux.HandleFunc("GET /api/flows/summary", handler.getFlowSummary)
	mux.HandleFunc("GET /api/flows/history", handler.getHistoryEvents)
	mux.HandleFunc("GET /api/flows/state", handler.getFlowState)
	mux.HandleFunc("GET /api/flows/wait", handler.waitForHistoryEvent)
	mux.HandleFunc("POST /api/flows/reset", handler.resetFlow)
	mux.HandleFunc("GET /healthz", health)
}

func (h *handler) searchFlows(response http.ResponseWriter, request *http.Request) {
	var body searchFlowsRequest
	if err := decodeJSON(response, request, &body); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if body.PageSize < 0 {
		WriteError(response, http.StatusBadRequest, "pageSize must be non-negative", nil)
		return
	}
	if body.PageSize == 0 {
		body.PageSize = 50
	}
	result, err := h.client.SearchFlows(request.Context(), &dexpb.SearchFlowsRequest{
		Query:         body.Query,
		PageSize:      body.PageSize,
		NextPageToken: body.NextPageToken,
	})
	if err != nil {
		writeGRPCError(response, err, "SearchFlows")
		return
	}
	flows := make([]flowExecution, 0, len(result.GetFlowRuns()))
	for _, entry := range result.GetFlowRuns() {
		flows = append(flows, mapSearchEntry(entry))
	}
	writeJSON(response, http.StatusOK, searchFlowsResponse{
		Flows:         flows,
		NextPageToken: result.GetNextPageToken(),
	})
}

func (h *handler) getFlowSummary(response http.ResponseWriter, request *http.Request) {
	flowID := request.URL.Query().Get("flowId")
	if flowID == "" {
		WriteError(response, http.StatusBadRequest, "flowId is required", nil)
		return
	}
	result, err := h.client.GetFlowSummary(request.Context(), &dexpb.GetFlowSummaryRequest{
		FlowId: flowID,
		RunId:  request.URL.Query().Get("runId"),
	})
	if err != nil {
		writeGRPCError(response, err, "GetFlowSummary")
		return
	}
	writeJSON(response, http.StatusOK, mapSummary(result))
}

func (h *handler) getHistoryEvents(response http.ResponseWriter, request *http.Request) {
	flowID, runID, ok := flowRunIDs(response, request)
	if !ok {
		return
	}
	startEventID, err := parseNonNegativeInt64(request, "startInternalEventId", 0)
	if err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	pageSize, err := parseNonNegativeInt64(request, "estimatePageSize", 100)
	if err != nil || pageSize > int64(^uint32(0)>>1) {
		if err == nil {
			err = fmt.Errorf("estimatePageSize is too large")
		}
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	pageToken, err := base64.StdEncoding.DecodeString(request.URL.Query().Get("nextPageToken"))
	if err != nil {
		WriteError(response, http.StatusBadRequest, "nextPageToken must be base64", nil)
		return
	}
	result, err := h.client.GetHistoryEvents(request.Context(), &dexpb.GetHistoryEventsRequest{
		FlowId:               flowID,
		RunId:                runID,
		StartInternalEventId: startEventID,
		EstimatePageSize:     int32(pageSize),
		NextPageToken:        pageToken,
	})
	if err != nil {
		writeGRPCError(response, err, "GetHistoryEvents")
		return
	}
	events := make([]historyEvent, 0, len(result.GetEvents()))
	for _, event := range result.GetEvents() {
		mapped, mapErr := mapHistoryEvent(event)
		if mapErr != nil {
			WriteError(response, http.StatusBadGateway, mapErr.Error(), nil)
			return
		}
		events = append(events, mapped)
	}
	writeJSON(response, http.StatusOK, historyPage{
		Events:              events,
		NextPageToken:       base64.StdEncoding.EncodeToString(result.GetNextPageToken()),
		NextInternalEventID: result.GetNextInternalEventId(),
	})
}

func (h *handler) getFlowState(response http.ResponseWriter, request *http.Request) {
	flowID, runID, ok := flowRunIDs(response, request)
	if !ok {
		return
	}
	result, err := h.client.GetFlowState(request.Context(), &dexpb.GetFlowStateRequest{
		FlowId: flowID,
		RunId:  runID,
	})
	if err != nil {
		writeGRPCError(response, err, "GetFlowState")
		return
	}
	mapped, err := mapFlowState(result)
	if err != nil {
		WriteError(response, http.StatusBadGateway, err.Error(), nil)
		return
	}
	writeJSON(response, http.StatusOK, mapped)
}

func (h *handler) waitForHistoryEvent(response http.ResponseWriter, request *http.Request) {
	flowID, runID, ok := flowRunIDs(response, request)
	if !ok {
		return
	}
	nextEventID, err := parseNonNegativeInt64(request, "nextInternalEventId", 0)
	if err != nil || nextEventID == 0 {
		WriteError(
			response,
			http.StatusBadRequest,
			"flowId, runId, and positive nextInternalEventId are required",
			nil,
		)
		return
	}
	result, err := h.client.WaitForHistoryEvent(
		request.Context(),
		&dexpb.WaitForHistoryEventRequest{
			FlowId:              flowID,
			RunId:               runID,
			NextInternalEventId: nextEventID,
		},
	)
	if err != nil {
		writeGRPCError(response, err, "WaitForHistoryEvent")
		return
	}
	mapped, err := protoMap(result)
	if err != nil {
		WriteError(response, http.StatusBadGateway, err.Error(), nil)
		return
	}
	writeJSON(response, http.StatusOK, mapped)
}

func (h *handler) resetFlow(response http.ResponseWriter, request *http.Request) {
	var body resetFlowRequest
	if err := decodeJSON(response, request, &body); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if body.FlowID == "" || body.RunID == "" || body.ResetType == 0 || strings.TrimSpace(body.Reason) == "" {
		WriteError(
			response,
			http.StatusBadRequest,
			"flowId, runId, resetType, and reason are required",
			nil,
		)
		return
	}
	result, err := h.client.ResetFlow(request.Context(), &dexpb.ResetFlowRequest{
		FlowId:           body.FlowID,
		RunId:            body.RunID,
		ResetType:        dexpb.FlowResetType(body.ResetType),
		HistoryEventId:   body.HistoryEventID,
		Reason:           body.Reason,
		StepType:         body.StepType,
		StepExecutionId:  body.StepExecutionID,
		HistoryEventTime: body.HistoryEventTime,
	})
	if err != nil {
		writeGRPCError(response, err, "ResetFlow")
		return
	}
	writeJSON(response, http.StatusOK, resetFlowResponse{RunID: result.GetRunId()})
}

func health(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func flowRunIDs(
	response http.ResponseWriter,
	request *http.Request,
) (string, string, bool) {
	flowID := request.URL.Query().Get("flowId")
	runID := request.URL.Query().Get("runId")
	if flowID == "" || runID == "" {
		WriteError(response, http.StatusBadRequest, "flowId and runId are required", nil)
		return "", "", false
	}
	return flowID, runID, true
}

func decodeJSON(response http.ResponseWriter, request *http.Request, value interface{}) error {
	request.Body = http.MaxBytesReader(response, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid JSON body: trailing data")
	}
	return nil
}

func parseNonNegativeInt64(request *http.Request, name string, fallback int64) (int64, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return parsed, nil
}

func writeGRPCError(response http.ResponseWriter, err error, operation string) {
	statusError := status.Convert(err)
	httpStatus := http.StatusBadGateway
	switch statusError.Code() {
	case codes.InvalidArgument:
		httpStatus = http.StatusBadRequest
	case codes.NotFound:
		httpStatus = http.StatusNotFound
	case codes.FailedPrecondition:
		httpStatus = http.StatusConflict
	case codes.DeadlineExceeded:
		httpStatus = http.StatusRequestTimeout
	case codes.Canceled:
		httpStatus = 499
	}
	message := statusError.Message()
	if message == "" {
		message = operation + " failed"
	}
	grpcCode := int32(statusError.Code())
	WriteError(response, httpStatus, message, &grpcCode)
}

func WriteError(response http.ResponseWriter, statusCode int, message string, grpcCode *int32) {
	writeJSON(response, statusCode, errorResponse{Error: message, GRPCCode: grpcCode})
}

func writeJSON(response http.ResponseWriter, statusCode int, value interface{}) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(statusCode)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		panic(fmt.Sprintf("encode HTTP response: %v", err))
	}
}
