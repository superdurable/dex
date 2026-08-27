// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package api

import (
	"context"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

type ApiService interface {
	StartFlow(context.Context, *dexpb.StartFlowRequest) (*dexpb.StartFlowResponse, error)
	PublishToChannel(context.Context, *dexpb.PublishToChannelRequest) (*emptypb.Empty, error)
	WriteStream(context.Context, *dexpb.WriteStreamRequest) (*emptypb.Empty, error)
	ReadStream(context.Context, *dexpb.ReadStreamRequest) (*dexpb.ReadStreamResponse, error)
	StopFlow(context.Context, *dexpb.StopFlowRequest) (*emptypb.Empty, error)
	GetAttributes(context.Context, *dexpb.GetAttributesRequest) (*dexpb.GetAttributesResponse, error)
	SetAttributes(context.Context, *dexpb.SetAttributesRequest) (*emptypb.Empty, error)
	LoadBlobs(context.Context, *dexpb.LoadBlobsRequest) (*dexpb.LoadBlobsResponse, error)
	WaitForFlow(context.Context, *dexpb.WaitForFlowRequest) (*dexpb.FlowResult, error)
	SearchFlows(context.Context, *dexpb.SearchFlowsRequest) (*dexpb.SearchFlowsResponse, error)
	SyncAttributeIndexes(context.Context, *dexpb.SyncAttributeIndexRequest) (*dexpb.SyncAttributeIndexResponse, error)
	GetFlowSummary(context.Context, *dexpb.GetFlowSummaryRequest) (*dexpb.GetFlowSummaryResponse, error)
	GetHistoryEvents(context.Context, *dexpb.GetHistoryEventsRequest) (*dexpb.GetHistoryEventsResponse, error)
	WaitForHistoryEvent(context.Context, *dexpb.WaitForHistoryEventRequest) (*dexpb.WaitForHistoryEventResponse, error)
	GetFlowState(context.Context, *dexpb.GetFlowStateRequest) (*dexpb.GetFlowStateResponse, error)
	ResetFlow(context.Context, *dexpb.ResetFlowRequest) (*dexpb.ResetFlowResponse, error)
	InvokeRPC(context.Context, *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error)
	SkipTimer(context.Context, *dexpb.SkipTimerRequest) (*emptypb.Empty, error)
	UpdateFlowConfig(context.Context, *dexpb.UpdateFlowConfigRequest) (*emptypb.Empty, error)
	WaitForStepCompletion(
		context.Context,
		*dexpb.WaitForStepCompletionRequest,
	) (*dexpb.WaitForStepCompletionResponse, error)
	WaitForAttribute(context.Context, *dexpb.WaitForAttributeRequest) (*emptypb.Empty, error)
	TriggerContinueAsNew(context.Context, *dexpb.TriggerContinueAsNewRequest) (*emptypb.Empty, error)
	HealthCheck(context.Context, *emptypb.Empty) (*dexpb.HealthInfo, error)
	DumpFlowForContinueAsNew(
		context.Context,
		*dexpb.ContinueAsNewDumpRequest,
	) (*dexpb.ContinueAsNewDumpResponse, error)
	Close()
}
