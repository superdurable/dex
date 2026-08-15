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
	provider              interfaces.WorkflowProvider
	continueAsNewer       *ContinueAsNewer
	attributeSyncer       *AttributeSynchronizer
	signalReceiver        *SignalReceiver
	stepExecutionRegistry *StepExecutionRegistry
	startedFinalizing     bool
}

func NewTerminalCoordinator(
	provider interfaces.WorkflowProvider,
	continueAsNewer *ContinueAsNewer,
	attributeSyncer *AttributeSynchronizer,
	signalReceiver *SignalReceiver,
	stepExecutionRegistry *StepExecutionRegistry,
) *TerminalCoordinator {
	if provider == nil || continueAsNewer == nil || attributeSyncer == nil ||
		signalReceiver == nil || stepExecutionRegistry == nil {
		panic("TerminalCoordinator requires non-nil dependencies")
	}
	return &TerminalCoordinator{
		provider:              provider,
		continueAsNewer:       continueAsNewer,
		attributeSyncer:       attributeSyncer,
		signalReceiver:        signalReceiver,
		stepExecutionRegistry: stepExecutionRegistry,
	}
}

func (c *TerminalCoordinator) CoordinateAndFinalizeError(
	ctx interfaces.UnifiedContext,
	retErr error,
) error {
	if c.provider.IsContinueAsNewError(retErr) {
		return retErr
	}
	c.startedFinalizing = true
	if err := c.stepExecutionRegistry.CancelAll(ctx); err != nil {
		return err
	}
	if err := c.provider.Await(ctx, func() bool {
		return c.attributeSyncer.ProducersDrained() && c.continueAsNewer.inflightUpdateOperations == 0
	}); err != nil {
		return err
	}
	for {
		if err := c.attributeSyncer.FlushAndClose(ctx); err != nil {
			return err
		}
		c.signalReceiver.DrainAllReceivedButUnprocessedSignals(ctx)
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
