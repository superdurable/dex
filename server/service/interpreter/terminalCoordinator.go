// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import "github.com/superdurable/dex/service/interpreter/interfaces"

type TerminalCoordinator struct {
	provider        interfaces.WorkflowProvider
	ctx             interfaces.UnifiedContext
	continueAsNewer *ContinueAsNewer
	attributeSyncer *AttributeSynchronizer
	forceComplete   *bool
}

func NewTerminalCoordinator(
	provider interfaces.WorkflowProvider,
	ctx interfaces.UnifiedContext,
	continueAsNewer *ContinueAsNewer,
	attributeSyncer *AttributeSynchronizer,
	forceComplete *bool,
) *TerminalCoordinator {
	if provider == nil || ctx == nil || continueAsNewer == nil || attributeSyncer == nil || forceComplete == nil {
		panic("TerminalCoordinator requires non-nil dependencies")
	}
	return &TerminalCoordinator{
		provider:        provider,
		ctx:             ctx,
		continueAsNewer: continueAsNewer,
		attributeSyncer: attributeSyncer,
		forceComplete:   forceComplete,
	}
}

func (c *TerminalCoordinator) CoordinateAndFinalizeError(retErr error) error {
	if c.provider.IsContinueAsNewError(retErr) {
		return retErr
	}
	if err := c.provider.Await(c.ctx, func() bool {
		return *c.forceComplete ||
			(c.attributeSyncer.ProducersDrained() && c.continueAsNewer.inflightUpdateOperations == 0)
	}); err != nil {
		return err
	}
	if err := c.attributeSyncer.FlushAndClose(c.ctx); err != nil {
		return err
	}
	return retErr
}
