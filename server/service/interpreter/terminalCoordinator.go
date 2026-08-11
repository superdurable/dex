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
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type TerminalCoordinator struct {
	provider            interfaces.WorkflowProvider
	requested           bool
	status              dexpb.FlowStatus
	cause               error
	cancelReason        string
	activeStepProducers int
	waitForProducers    bool
}

func NewTerminalCoordinator(provider interfaces.WorkflowProvider) *TerminalCoordinator {
	if provider == nil {
		panic("TerminalCoordinator requires a WorkflowProvider")
	}
	return &TerminalCoordinator{provider: provider}
}

func (c *TerminalCoordinator) Finalize(
	ctx interfaces.UnifiedContext,
	continueAsNewer *ContinueAsNewer,
	attributeSynchronizer *AttributeSynchronizer,
	cause error,
) error {
	if !c.IsRequested() {
		if cause == nil {
			c.RequestCompletion()
		} else {
			c.RequestFailure(cause)
		}
	}
	if err := c.provider.Await(ctx, func() bool {
		return c.ProducersDrained(continueAsNewer.inflightUpdateOperations)
	}); err != nil {
		return err
	}
	if err := attributeSynchronizer.FlushAndClose(ctx); err != nil {
		return err
	}
	return c.ResultError()
}

func (c *TerminalCoordinator) RequestClientStop(request *dexpb.StopFlowSignalRequest) {
	if request == nil || c.requested {
		return
	}
	switch request.GetStopType() {
	case dexpb.StopType_STOP_TYPE_CANCEL:
		c.requested = true
		c.status = dexpb.FlowStatus_FLOW_STATUS_CANCELED
		c.cancelReason = request.GetReason()
		c.waitForProducers = true
	case dexpb.StopType_STOP_TYPE_FAIL:
		reason := request.GetReason()
		if reason == "" {
			reason = "fail by client"
		}
		c.RequestFailure(c.provider.NewFlowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW,
			&dexpb.ErrorResponse{Detail: reason},
		))
	}
}

func (c *TerminalCoordinator) RequestFailure(cause error) {
	if c.requested {
		return
	}
	if cause == nil {
		cause = c.provider.NewFlowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
			&dexpb.ErrorResponse{Detail: "terminal failure has no cause"},
		)
	}
	c.requested = true
	c.status = dexpb.FlowStatus_FLOW_STATUS_FAILED
	c.cause = cause
	c.waitForProducers = true
}

func (c *TerminalCoordinator) RequestCompletion() {
	if c.requested {
		return
	}
	c.requested = true
	c.status = dexpb.FlowStatus_FLOW_STATUS_COMPLETED
	c.waitForProducers = true
}

func (c *TerminalCoordinator) RequestForceCompletion() {
	if c.requested {
		return
	}
	c.requested = true
	c.status = dexpb.FlowStatus_FLOW_STATUS_COMPLETED
}

func (c *TerminalCoordinator) ProducerStarted() {
	c.activeStepProducers++
}

func (c *TerminalCoordinator) ProducerFinished() {
	c.activeStepProducers--
	if c.activeStepProducers < 0 {
		panic("active step producer count is negative")
	}
}

func (c *TerminalCoordinator) IsRequested() bool {
	return c.requested
}

func (c *TerminalCoordinator) ProducersDrained(inflightUpdates int) bool {
	return !c.waitForProducers || (c.activeStepProducers == 0 && inflightUpdates == 0)
}

func (c *TerminalCoordinator) ResultError() error {
	if c.status == dexpb.FlowStatus_FLOW_STATUS_CANCELED {
		return c.provider.NewCanceledError(c.cancelReason)
	}
	return c.cause
}
