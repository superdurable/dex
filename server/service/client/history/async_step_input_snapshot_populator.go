// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package history

import (
	"context"
	"fmt"
	"time"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/blobstore"
	"google.golang.org/protobuf/proto"
)

// AsyncStepInputSnapshotPopulator enriches local activity events from blob storage.
type AsyncStepInputSnapshotPopulator struct {
	cfg    *config.BlobStoreConfig
	client uclient.UnifiedClient
	store  blobstore.BlobStore
}

// NewAsyncStepInputSnapshotPopulator creates a post-processor for semantic history.
func NewAsyncStepInputSnapshotPopulator(
	cfg *config.BlobStoreConfig,
	client uclient.UnifiedClient,
	store blobstore.BlobStore,
) *AsyncStepInputSnapshotPopulator {
	if cfg == nil || client == nil {
		panic("async step input snapshot populator requires config and client")
	}
	if cfg.EffectiveEnabled() && store == nil {
		panic("async step input snapshot populator requires a blob store when enabled")
	}
	return &AsyncStepInputSnapshotPopulator{
		cfg:    cfg,
		client: client,
		store:  store,
	}
}

// Populate fills missing local activity inputs or marks them unavailable.
func (p *AsyncStepInputSnapshotPopulator) Populate(
	ctx context.Context,
	flowID string,
	runID string,
	events []*dexpb.FlowHistoryEvent,
) error {
	if !hasMissingStepEventInput(events) {
		return nil
	}
	if !p.cfg.EffectiveEnabled() {
		markMissingStepEventInputsUnavailable(events)
		return nil
	}
	description, err := p.client.DescribeWorkflowExecution(ctx, flowID, runID, nil)
	if err != nil {
		return err
	}
	for _, event := range events {
		stepExecutionID, method := missingStepEventInputKey(event)
		if stepExecutionID == "" {
			continue
		}
		input, found, loadErr := p.load(
			ctx,
			description.StartTime,
			flowID,
			runID,
			stepExecutionID,
			method,
		)
		if loadErr != nil {
			if blobstore.IsObjectUnavailable(loadErr) {
				markStepEventInputUnavailable(event)
				continue
			}
			return loadErr
		}
		if !found {
			markStepEventInputUnavailable(event)
			continue
		}
		if err := applyStoredStepEventInput(event, input, method); err != nil {
			return err
		}
	}
	return nil
}

func (p *AsyncStepInputSnapshotPopulator) load(
	ctx context.Context,
	runStarted time.Time,
	flowID string,
	runID string,
	stepExecutionID string,
	method string,
) (*dexpb.InternalAsyncStepInputSnapshot, bool, error) {
	data, found, err := p.store.ReadStepEventInput(
		ctx,
		runStarted,
		flowID,
		runID,
		stepExecutionID,
		method,
	)
	if err != nil || !found {
		return nil, found, err
	}
	input := &dexpb.InternalAsyncStepInputSnapshot{}
	if err := proto.Unmarshal(data, input); err != nil {
		return nil, false, fmt.Errorf("unmarshal step event input: %w", err)
	}
	return input, true, nil
}

func hasMissingStepEventInput(events []*dexpb.FlowHistoryEvent) bool {
	for _, event := range events {
		stepExecutionID, _ := missingStepEventInputKey(event)
		if stepExecutionID != "" {
			return true
		}
	}
	return false
}

func missingStepEventInputKey(event *dexpb.FlowHistoryEvent) (string, string) {
	switch payload := event.GetPayload().(type) {
	case *dexpb.FlowHistoryEvent_StepWaitForCompleted:
		if payload.StepWaitForCompleted.GetInput() == nil {
			return payload.StepWaitForCompleted.GetContext().GetStepExecutionId(), blobstore.StepEventInputMethodWaitFor
		}
	case *dexpb.FlowHistoryEvent_StepWaitForFailed:
		if payload.StepWaitForFailed.GetInput() == nil {
			return payload.StepWaitForFailed.GetContext().GetStepExecutionId(), blobstore.StepEventInputMethodWaitFor
		}
	case *dexpb.FlowHistoryEvent_StepExecuteCompleted:
		if payload.StepExecuteCompleted.GetInput() == nil {
			return payload.StepExecuteCompleted.GetContext().GetStepExecutionId(), blobstore.StepEventInputMethodExecute
		}
	case *dexpb.FlowHistoryEvent_StepExecuteFailed:
		if payload.StepExecuteFailed.GetInput() == nil {
			return payload.StepExecuteFailed.GetContext().GetStepExecutionId(), blobstore.StepEventInputMethodExecute
		}
	}
	return "", ""
}

func markMissingStepEventInputsUnavailable(events []*dexpb.FlowHistoryEvent) {
	for _, event := range events {
		stepExecutionID, _ := missingStepEventInputKey(event)
		if stepExecutionID != "" {
			markStepEventInputUnavailable(event)
		}
	}
}

func markStepEventInputUnavailable(event *dexpb.FlowHistoryEvent) {
	input := &dexpb.StepMethodEventInput{Unavailable: true}
	switch payload := event.GetPayload().(type) {
	case *dexpb.FlowHistoryEvent_StepWaitForCompleted:
		payload.StepWaitForCompleted.Input = input
	case *dexpb.FlowHistoryEvent_StepWaitForFailed:
		payload.StepWaitForFailed.Input = input
	case *dexpb.FlowHistoryEvent_StepExecuteCompleted:
		payload.StepExecuteCompleted.Input = input
	case *dexpb.FlowHistoryEvent_StepExecuteFailed:
		payload.StepExecuteFailed.Input = input
	}
}

func applyStoredStepEventInput(
	event *dexpb.FlowHistoryEvent,
	input *dexpb.InternalAsyncStepInputSnapshot,
	method string,
) error {
	switch payload := event.GetPayload().(type) {
	case *dexpb.FlowHistoryEvent_StepWaitForCompleted:
		if method != blobstore.StepEventInputMethodWaitFor || input.GetWaitForRequest() == nil {
			return fmt.Errorf("stored input does not match WaitFor event %d", event.GetEventId())
		}
		applyWaitForSnapshot(payload.StepWaitForCompleted, input)
	case *dexpb.FlowHistoryEvent_StepWaitForFailed:
		if method != blobstore.StepEventInputMethodWaitFor || input.GetWaitForRequest() == nil {
			return fmt.Errorf("stored input does not match WaitFor event %d", event.GetEventId())
		}
		applyWaitForSnapshot(payload.StepWaitForFailed, input)
	case *dexpb.FlowHistoryEvent_StepExecuteCompleted:
		if method != blobstore.StepEventInputMethodExecute || input.GetExecuteRequest() == nil {
			return fmt.Errorf("stored input does not match Execute event %d", event.GetEventId())
		}
		applyExecuteSnapshot(payload.StepExecuteCompleted, input)
	case *dexpb.FlowHistoryEvent_StepExecuteFailed:
		if method != blobstore.StepEventInputMethodExecute || input.GetExecuteRequest() == nil {
			return fmt.Errorf("stored input does not match Execute event %d", event.GetEventId())
		}
		applyExecuteSnapshot(payload.StepExecuteFailed, input)
	}
	return nil
}

type stepEventWithContext interface {
	GetContext() *dexpb.StepMethodEventContext
}

func applyWaitForSnapshot(
	event stepEventWithContext,
	input *dexpb.InternalAsyncStepInputSnapshot,
) {
	request := input.GetWaitForRequest()
	switch payload := event.(type) {
	case *dexpb.StepWaitForCompletedEvent:
		payload.Input = waitEventInput(request)
	case *dexpb.StepWaitForFailedEvent:
		payload.Input = waitEventInput(request)
	}
	applyStoredStepMethodContext(event.GetContext(), request.GetContext(), request.GetStepType(), input)
}

func applyExecuteSnapshot(
	event stepEventWithContext,
	input *dexpb.InternalAsyncStepInputSnapshot,
) {
	request := input.GetExecuteRequest()
	switch payload := event.(type) {
	case *dexpb.StepExecuteCompletedEvent:
		payload.Input = executeEventInput(request)
	case *dexpb.StepExecuteFailedEvent:
		payload.Input = executeEventInput(request)
	}
	applyStoredStepMethodContext(event.GetContext(), request.GetContext(), request.GetStepType(), input)
}

func applyStoredStepMethodContext(
	context *dexpb.StepMethodEventContext,
	storedContext *dexpb.Context,
	stepType string,
	input *dexpb.InternalAsyncStepInputSnapshot,
) {
	context.StepExecutionId = storedContext.GetStepExecutionId()
	context.FromStepExecutionId = storedContext.GetFromStepExecutionId()
	context.StepType = stepType
	context.MethodOptions = input.GetMethodOptions()
}
