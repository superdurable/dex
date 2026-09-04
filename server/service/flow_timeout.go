// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package service

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
)

// ResolveFlowTimeoutPolicy validates and resolves the protocol-level default.
func ResolveFlowTimeoutPolicy(
	timeoutSeconds int32,
	policy dexpb.FlowTimeoutPolicy,
) (dexpb.FlowTimeoutPolicy, error) {
	if timeoutSeconds < 0 {
		return dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED,
			fmt.Errorf("flow timeout must be non-negative")
	}
	if timeoutSeconds == 0 {
		if policy != dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED {
			return dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED,
				fmt.Errorf("flow timeout policy requires a positive timeout")
		}
		return policy, nil
	}
	switch policy {
	case dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED:
		return dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL, nil
	case dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER:
		return policy, nil
	default:
		return dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED,
			fmt.Errorf("unknown flow timeout policy")
	}
}

// ValidateFlowTimeoutHandlerOptions validates handler-only timeout configuration.
func ValidateFlowTimeoutHandlerOptions(
	timeoutSeconds int32,
	policy dexpb.FlowTimeoutPolicy,
	options *dexpb.FlowTimeoutHandlerOptions,
	minimumHeartbeatTimeout time.Duration,
) error {
	if options == nil {
		return nil
	}
	if timeoutSeconds <= 0 || policy != dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER {
		return fmt.Errorf("Flow timeout handler options require the Handler policy and a positive timeout")
	}
	if options.GetMethodTimeoutSeconds() < 0 {
		return fmt.Errorf("Flow timeout handler method timeout must be non-negative")
	}
	stepOptions := FlowTimeoutHandlerStepOptions(options)
	if err := ValidateStepOptions(stepOptions, minimumHeartbeatTimeout); err != nil {
		return fmt.Errorf("Flow timeout handler options: %w", err)
	}
	switch options.GetFailurePolicy() {
	case dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_UNSPECIFIED,
		dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_FAIL_FLOW_ON_EXECUTE_METHOD_FAILURE:
		if options.GetFailureProceedStepType() != "" || options.GetFailureProceedStepOptions() != nil {
			return fmt.Errorf("Flow timeout handler failure target requires the proceed policy")
		}
	case dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP:
		if options.GetFailureProceedStepType() == "" {
			return fmt.Errorf("Flow timeout handler proceed policy requires a failure target")
		}
	default:
		return fmt.Errorf("unknown Flow timeout handler failure policy")
	}
	return nil
}

// FlowTimeoutHandlerStepOptions maps timeout handler configuration onto Execute behavior.
func FlowTimeoutHandlerStepOptions(options *dexpb.FlowTimeoutHandlerOptions) *dexpb.StepOptions {
	if options == nil {
		return &dexpb.StepOptions{}
	}
	return &dexpb.StepOptions{
		ExecuteTimeoutSeconds:            options.GetMethodTimeoutSeconds(),
		ExecuteRetryPolicy:               options.GetRetryPolicy(),
		ExecuteFailurePolicy:             options.GetFailurePolicy(),
		ExecuteFailureProceedStepType:    options.GetFailureProceedStepType(),
		ExecuteFailureProceedStepOptions: options.GetFailureProceedStepOptions(),
		ExecuteDurabilityOverride:        options.GetDurabilityOverride(),
		ExecuteLockAttributeKeys:         options.GetLockAttributeKeys(),
		HeartbeatTimeoutSeconds:          options.GetHeartbeatTimeoutSeconds(),
		ExecuteLoadAttributeMapInstances: options.GetLoadAttributeMapInstances(),
		ExecuteLoadChannelNames:          options.GetLoadChannelNames(),
		ExecuteLoadChannelMapInstances:   options.GetLoadChannelMapInstances(),
	}
}
