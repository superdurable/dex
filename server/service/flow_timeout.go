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
