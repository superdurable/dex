// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import (
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

const globalChangeId = "global"

// StartingVersionV1 is the reset baseline for the rewritten interpreter. All
// historical version branches were removed; running flows always execute the
// latest behavior. Future determinism-affecting changes add a new constant here
// and gate on it via GlobalVersioner.
const StartingVersionV1 = 1

const DeterministicStepActivityIDVersion = 2

const MaxOfAllVersions = DeterministicStepActivityIDVersion

// GlobalVersioner is the forward hook for determinism-safe interpreter changes.
// See https://stackoverflow.com/questions/73941723 for the pattern.
type GlobalVersioner struct {
	workflowProvider interfaces.WorkflowProvider
	ctx              interfaces.UnifiedContext
	version          int
}

func NewGlobalVersioner(
	workflowProvider interfaces.WorkflowProvider, ctx interfaces.UnifiedContext,
) *GlobalVersioner {
	version := workflowProvider.GetVersion(ctx, globalChangeId, 0, MaxOfAllVersions)
	return &GlobalVersioner{
		workflowProvider: workflowProvider,
		ctx:              ctx,
		version:          version,
	}
}

func (v *GlobalVersioner) UsesDeterministicStepActivityIDs() bool {
	return v.version >= DeterministicStepActivityIDVersion
}
