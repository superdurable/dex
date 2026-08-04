// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package temporal

import (
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"go.temporal.io/sdk/workflow"

	// TODO(cretz): Remove when tagged
	_ "go.temporal.io/sdk/contrib/tools/workflowcheck/determinism"
)

func (iw *InterpreterWorker) Engine(
	ctx workflow.Context,
	input *dexpb.InterpreterWorkflowInput,
) (*dexpb.InterpreterWorkflowOutput, error) {
	return iw.workflow.StartEngineFlow(
		interfaces.NewUnifiedContext(ctx),
		newTemporalWorkflowProvider(),
		input,
	)
}

func (iw *InterpreterWorker) BlobStoreCleanup(ctx workflow.Context, storeId string) (int, error) {
	return iw.workflow.BlobStoreCleanup(
		interfaces.NewUnifiedContext(ctx),
		newTemporalWorkflowProvider(),
		storeId,
	)
}
