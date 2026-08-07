// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
	mux.HandleFunc("POST /api/flows/stop", handler.stopFlow)
	mux.HandleFunc("POST /api/blobs/load", handler.loadBlobs)
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

func (h *handler) stopFlow(response http.ResponseWriter, request *http.Request) {
	var body stopFlowRequest
	if err := decodeJSON(response, request, &body); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if body.FlowID == "" || body.StopType == 0 {
		WriteError(response, http.StatusBadRequest, "flowId and stopType are required", nil)
		return
	}
	stopType := dexpb.StopType(body.StopType)
	switch stopType {
	case dexpb.StopType_STOP_TYPE_CANCEL,
		dexpb.StopType_STOP_TYPE_TERMINATE,
		dexpb.StopType_STOP_TYPE_FAIL:
	default:
		WriteError(response, http.StatusBadRequest, "stopType must be cancel, terminate, or fail", nil)
		return
	}
	if stopType != dexpb.StopType_STOP_TYPE_CANCEL && strings.TrimSpace(body.Reason) == "" {
		WriteError(response, http.StatusBadRequest, "reason is required for terminate and fail", nil)
		return
	}
	_, err := h.client.StopFlow(request.Context(), &dexpb.StopFlowRequest{
		FlowId:   body.FlowID,
		RunId:    body.RunID,
		Reason:   body.Reason,
		StopType: stopType,
	})
	if err != nil {
		writeGRPCError(response, err, "StopFlow")
		return
	}
	writeJSON(response, http.StatusOK, map[string]struct{}{})
}

func (h *handler) loadBlobs(response http.ResponseWriter, request *http.Request) {
	var body loadBlobsRequest
	if err := decodeJSON(response, request, &body); err != nil {
		WriteError(response, http.StatusBadRequest, err.Error(), nil)
		return
	}
	values := make([]*dexpb.Value, 0, len(body.Values))
	seen := make(map[string]struct{}, len(body.Values))
	for _, reference := range body.Values {
		cacheKey := blobCacheKey(reference)
		if reference.ID == "" || cacheKey == "" {
			WriteError(response, http.StatusBadRequest, "blob id and kind are required", nil)
			return
		}
		if _, exists := seen[cacheKey]; exists {
			continue
		}
		seen[cacheKey] = struct{}{}
		value := &dexpb.Value{}
		switch reference.Kind {
		case "string":
			value.Kind = &dexpb.Value_InternalBlobIdForStringValue{InternalBlobIdForStringValue: reference.ID}
		case "object":
			value.Kind = &dexpb.Value_InternalBlobIdForObjValue{InternalBlobIdForObjValue: reference.ID}
		default:
			WriteError(response, http.StatusBadRequest, "blob kind must be string or object", nil)
			return
		}
		values = append(values, value)
	}
	result, err := h.client.LoadBlobs(request.Context(), &dexpb.LoadBlobsRequest{Values: values})
	if err != nil {
		writeGRPCError(
			response,
			status.Error(status.Code(err), "Value blob unavailable"),
			"LoadBlobs",
		)
		return
	}
	mapped := make(map[string]interface{}, len(result.GetValues()))
	for _, reference := range body.Values {
		value, exists := result.GetValues()[reference.ID]
		if exists {
			mapped[blobCacheKey(reference)] = dexValue(value)
		}
	}
	writeJSON(response, http.StatusOK, loadBlobsResponse{Values: mapped})
}

func blobCacheKey(reference blobReference) string {
	if reference.Kind != "string" && reference.Kind != "object" {
		return ""
	}
	return reference.Kind + ":" + reference.ID
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
