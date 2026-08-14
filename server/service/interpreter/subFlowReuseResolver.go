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

type SubFlowReuseResolver struct {
	client uclient.UnifiedClient
}

func NewSubFlowReuseResolver(client uclient.UnifiedClient) *SubFlowReuseResolver {
	if client == nil {
		panic("SubFlowReuseResolver requires a client")
	}
	return &SubFlowReuseResolver{client: client}
}

func (r *SubFlowReuseResolver) Resolve(
	ctx context.Context,
	condition *dexpb.SubFlowCondition,
	subFlowID string,
	workflowOptions uclient.StartWorkflowOptions,
	workflowInput *dexpb.InterpreterWorkflowInput,
) (*dexpb.StartSubFlowActivityOutput, error) {
	for {
		description, err := r.client.DescribeWorkflowExecution(ctx, subFlowID, "", nil)
		if err != nil && !r.client.IsNotFoundError(err) {
			return nil, fmt.Errorf("describe SubFlow %q: %w", subFlowID, err)
		}
		if err == nil {
			output, resolveErr := r.resolveExisting(ctx, condition, subFlowID, description)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if output != nil {
				return output, nil
			}
		}

		_, startErr := r.client.StartInterpreterWorkflow(ctx, workflowOptions, workflowInput)
		if startErr == nil {
			return &dexpb.StartSubFlowActivityOutput{}, nil
		}
		if !r.client.IsWorkflowAlreadyStartedError(startErr) {
			return nil, fmt.Errorf("start SubFlow %q: %w", subFlowID, startErr)
		}
	}
}

func (r *SubFlowReuseResolver) resolveExisting(
	ctx context.Context,
	condition *dexpb.SubFlowCondition,
	subFlowID string,
	description *uclient.DescribeWorkflowExecutionResponse,
) (*dexpb.StartSubFlowActivityOutput, error) {
	existingRequestID := memoString(description.Memos, service.WorkflowRequestId)
	if existingRequestID == condition.GetRequestId() {
		return r.attachOrRead(ctx, subFlowID, description)
	}

	policy := effectiveSubFlowReusePolicy(condition.GetOptions().GetReusePolicy())
	if description.Status == dexpb.FlowStatus_FLOW_STATUS_RUNNING {
		if policy == dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_ALWAYS_RESTART {
			return nil, nil
		}
		return &dexpb.StartSubFlowActivityOutput{}, nil
	}
	if policy == dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_ALWAYS_RESTART ||
		(policy == dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_RESTART_IF_PREVIOUS_EXITS_ABNORMALLY &&
			isAbnormalSubFlowStatus(description.Status)) {
		return nil, nil
	}
	return r.readTerminal(ctx, subFlowID, description)
}

func (r *SubFlowReuseResolver) attachOrRead(
	ctx context.Context,
	subFlowID string,
	description *uclient.DescribeWorkflowExecutionResponse,
) (*dexpb.StartSubFlowActivityOutput, error) {
	if description.Status == dexpb.FlowStatus_FLOW_STATUS_RUNNING {
		return &dexpb.StartSubFlowActivityOutput{}, nil
	}
	return r.readTerminal(ctx, subFlowID, description)
}

func (r *SubFlowReuseResolver) readTerminal(
	ctx context.Context,
	subFlowID string,
	description *uclient.DescribeWorkflowExecutionResponse,
) (*dexpb.StartSubFlowActivityOutput, error) {
	var workflowOutput dexpb.InterpreterWorkflowOutput
	_, flowStatus, err := r.client.GetWorkflowResult(
		ctx, &workflowOutput, subFlowID, description.RunId,
	)
	if r.client.IsNotFoundError(err) {
		return nil, nil
	}
	result := &dexpb.FlowResult{
		FlowStatus: flowStatus,
		Results:    workflowOutput.GetStepCompletionOutputs(),
	}
	if err != nil {
		var errorResponse dexpb.ServiceErrorResponse
		if errorType, ok := r.client.GetIfFlowError(err, &errorResponse); ok {
			result.ErrorType = errorType
			result.ErrorMessage = errorResponse.GetDetail()
		} else if flowStatus == dexpb.FlowStatus_FLOW_STATUS_UNSPECIFIED {
			return nil, fmt.Errorf("read terminal SubFlow %q: %w", subFlowID, err)
		} else {
			result.ErrorMessage = err.Error()
		}
	}
	return &dexpb.StartSubFlowActivityOutput{
		ImmediateFlowResult: result,
	}, nil
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
