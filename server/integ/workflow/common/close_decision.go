// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package common

import "github.com/superdurable/dex/gen/dexpb"

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
