// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package interpreter

import "github.com/superdurable/dex/service/interpreter/interfaces"

type TerminalCoordinator struct {
	provider          interfaces.WorkflowProvider
	ctx               interfaces.UnifiedContext
	continueAsNewer   *ContinueAsNewer
	attributeSyncer   *AttributeSynchronizer
	signalReceiver    *SignalReceiver
	forceComplete     *bool
	startedFinalizing bool
}

func NewTerminalCoordinator(
	provider interfaces.WorkflowProvider,
	ctx interfaces.UnifiedContext,
	continueAsNewer *ContinueAsNewer,
	attributeSyncer *AttributeSynchronizer,
	signalReceiver *SignalReceiver,
	forceComplete *bool,
) *TerminalCoordinator {
	if provider == nil || ctx == nil || continueAsNewer == nil || attributeSyncer == nil || signalReceiver == nil || forceComplete == nil {
		panic("TerminalCoordinator requires non-nil dependencies")
	}
	return &TerminalCoordinator{
		provider:        provider,
		ctx:             ctx,
		continueAsNewer: continueAsNewer,
		attributeSyncer: attributeSyncer,
		signalReceiver:  signalReceiver,
		forceComplete:   forceComplete,
	}
}

func (c *TerminalCoordinator) CoordinateAndFinalizeError(retErr error) error {
	if c.provider.IsContinueAsNewError(retErr) {
		return retErr
	}
	c.startedFinalizing = true
	if err := c.provider.Await(c.ctx, func() bool {
		return *c.forceComplete ||
			(c.attributeSyncer.ProducersDrained() && c.continueAsNewer.inflightUpdateOperations == 0)
	}); err != nil {
		return err
	}
	for {
		if err := c.attributeSyncer.FlushAndClose(c.ctx); err != nil {
			return err
		}
		c.signalReceiver.DrainAllReceivedButUnprocessedSignals(c.ctx)
		if stopBySignal, stopErr := c.signalReceiver.GetIfStopFlowRequested(); stopBySignal {
			retErr = stopErr
		}
		if len(c.attributeSyncer.PendingItems()) == 0 {
			return retErr
		}
	}
}

func (c *TerminalCoordinator) HasStartedFinalizing() bool {
	return c.startedFinalizing
}
