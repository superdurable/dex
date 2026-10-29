// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package common

import "github.com/superdurable/dex/sdk-go/gen/dexpb"

func GracefulCompleteDecision(closeInput *dexpb.Value) *dexpb.CloseDecision {
	return &dexpb.CloseDecision{
		CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
		CloseInput:        closeInput,
	}
}

func ForceCompleteDecision(closeInput *dexpb.Value) *dexpb.CloseDecision {
	return &dexpb.CloseDecision{
		CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE,
		CloseInput:        closeInput,
	}
}

func ForceFailDecision(closeInput *dexpb.Value) *dexpb.CloseDecision {
	return &dexpb.CloseDecision{
		CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL,
		CloseInput:        closeInput,
	}
}

func DeadEndDecision() *dexpb.CloseDecision {
	return &dexpb.CloseDecision{
		CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END,
	}
}
