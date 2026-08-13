// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package interpreter

import (
	"context"
	"fmt"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
)

type subFlowReuseResolver struct {
	client uclient.UnifiedClient
}

func newSubFlowReuseResolver(client uclient.UnifiedClient) *subFlowReuseResolver {
	if client == nil {
		panic("subFlowReuseResolver requires a client")
	}
	return &subFlowReuseResolver{client: client}
}

func (r *subFlowReuseResolver) resolve(
	ctx context.Context,
	condition *dexpb.SubFlowCondition,
	workflowOptions uclient.StartWorkflowOptions,
	workflowInput *dexpb.InterpreterWorkflowInput,
) (*dexpb.StartSubFlowActivityOutput, error) {
	for {
		description, err := r.client.DescribeWorkflowExecution(ctx, condition.GetFlowId(), "", nil)
		if err != nil && !r.client.IsNotFoundError(err) {
			return nil, fmt.Errorf("describe SubFlow %q: %w", condition.GetFlowId(), err)
		}
		if err == nil {
			output, resolveErr := r.resolveExisting(ctx, condition, description)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if output != nil {
				return output, nil
			}
		}

		_, startErr := r.client.StartInterpreterWorkflow(ctx, workflowOptions, workflowInput)
		if startErr == nil {
			return &dexpb.StartSubFlowActivityOutput{
				NormalizedRequestId: condition.GetNormalizedRequestId(),
				Resolution:          dexpb.SubFlowStartResolution_SUB_FLOW_START_RESOLUTION_STARTED,
			}, nil
		}
		if !r.client.IsWorkflowAlreadyStartedError(startErr) {
			return nil, fmt.Errorf("start SubFlow %q: %w", condition.GetFlowId(), startErr)
		}
	}
}

func (r *subFlowReuseResolver) resolveExisting(
	ctx context.Context,
	condition *dexpb.SubFlowCondition,
	description *uclient.DescribeWorkflowExecutionResponse,
) (*dexpb.StartSubFlowActivityOutput, error) {
	existingRequestID := memoString(description.Memos, service.WorkflowRequestId)
	if existingRequestID == condition.GetNormalizedRequestId() {
		return r.attachOrRead(ctx, condition, description, existingRequestID)
	}

	policy := effectiveSubFlowReusePolicy(condition.GetOptions().GetReusePolicy())
	if description.Status == dexpb.FlowStatus_FLOW_STATUS_RUNNING {
		if policy == dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_ALWAYS_RESTART {
			return nil, nil
		}
		return runningSubFlowOutput(existingRequestID), nil
	}
	if policy == dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_ALWAYS_RESTART ||
		(policy == dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_RESTART_IF_PREVIOUS_EXITS_ABNORMALLY &&
			isAbnormalSubFlowStatus(description.Status)) {
		return nil, nil
	}
	return r.readTerminal(ctx, condition, description, existingRequestID)
}

func (r *subFlowReuseResolver) attachOrRead(
	ctx context.Context,
	condition *dexpb.SubFlowCondition,
	description *uclient.DescribeWorkflowExecutionResponse,
	requestID string,
) (*dexpb.StartSubFlowActivityOutput, error) {
	if description.Status == dexpb.FlowStatus_FLOW_STATUS_RUNNING {
		return runningSubFlowOutput(requestID), nil
	}
	return r.readTerminal(ctx, condition, description, requestID)
}

func (r *subFlowReuseResolver) readTerminal(
	ctx context.Context,
	condition *dexpb.SubFlowCondition,
	description *uclient.DescribeWorkflowExecutionResponse,
	requestID string,
) (*dexpb.StartSubFlowActivityOutput, error) {
	var workflowOutput dexpb.InterpreterWorkflowOutput
	resolvedRunID, flowStatus, err := r.client.GetWorkflowResult(
		ctx, &workflowOutput, condition.GetFlowId(), description.RunId,
	)
	if r.client.IsNotFoundError(err) {
		return nil, nil
	}
	result := &dexpb.FlowResult{
		FlowId:     condition.GetFlowId(),
		RunId:      resolvedRunID,
		FlowStatus: flowStatus,
		Results:    workflowOutput.GetStepCompletionOutputs(),
	}
	if result.RunId == "" {
		result.RunId = description.RunId
	}
	if err != nil {
		var errorResponse dexpb.ServiceErrorResponse
		if errorType, ok := r.client.GetIfFlowError(err, &errorResponse); ok {
			result.ErrorType = errorType
			result.ErrorMessage = errorResponse.GetDetail()
		} else if flowStatus == dexpb.FlowStatus_FLOW_STATUS_UNSPECIFIED {
			return nil, fmt.Errorf("read terminal SubFlow %q: %w", condition.GetFlowId(), err)
		} else {
			result.ErrorMessage = err.Error()
		}
	}
	return &dexpb.StartSubFlowActivityOutput{
		NormalizedRequestId: requestID,
		Resolution:          dexpb.SubFlowStartResolution_SUB_FLOW_START_RESOLUTION_ATTACHED_TERMINAL,
		TerminalResult:      result,
	}, nil
}

func runningSubFlowOutput(requestID string) *dexpb.StartSubFlowActivityOutput {
	return &dexpb.StartSubFlowActivityOutput{
		NormalizedRequestId: requestID,
		Resolution:          dexpb.SubFlowStartResolution_SUB_FLOW_START_RESOLUTION_ATTACHED_RUNNING,
	}
}

func memoString(memos map[string]*dexpb.Value, key string) string {
	value := memos[key]
	if value == nil || value.GetObjValue() == nil {
		return ""
	}
	return string(value.GetObjValue().GetPayload())
}

func effectiveSubFlowReusePolicy(policy dexpb.SubFlowReusePolicy) dexpb.SubFlowReusePolicy {
	if policy == dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_UNSPECIFIED {
		return dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_RESTART_IF_PREVIOUS_EXITS_ABNORMALLY
	}
	return policy
}

func isAbnormalSubFlowStatus(status dexpb.FlowStatus) bool {
	return status == dexpb.FlowStatus_FLOW_STATUS_FAILED ||
		status == dexpb.FlowStatus_FLOW_STATUS_TIMEOUT ||
		status == dexpb.FlowStatus_FLOW_STATUS_TERMINATED ||
		status == dexpb.FlowStatus_FLOW_STATUS_CANCELED
}
