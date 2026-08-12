// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package service

import "github.com/superdurable/dex/gen/dexpb"

const StartingStepFromStepExecutionId = "__start__"

const rpcStepSourcePrefix = "__rpc/"

// GetFromStepExecutionIdForRPC returns the reserved source for an RPC.
func GetFromStepExecutionIdForRPC(rpcName string) string {
	return rpcStepSourcePrefix + rpcName
}

// SetFromStepExecutionIDForStepDecision overwrites worker-provided movement sources.
func SetFromStepExecutionIDForStepDecision(decision *dexpb.StepDecision, source string) {
	for _, movement := range decision.GetNextSteps() {
		if movement != nil {
			movement.FromStepExecutionIdInternalOnly = source
			movement.RecoveryErrorInternalOnly = nil
		}
	}
}
