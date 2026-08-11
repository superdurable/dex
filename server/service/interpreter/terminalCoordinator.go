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

type terminalKind int

const (
	terminalNone terminalKind = iota
	terminalCancel
	terminalFailure
	terminalForceCompletion
)

type TerminalCoordinator struct {
	provider        interfaces.WorkflowProvider
	ctx             interfaces.UnifiedContext
	continueAsNewer *ContinueAsNewer
	attributeSyncer *AttributeSynchronizer
	kind            terminalKind
	resultErr       error
}

func NewTerminalCoordinator(
	provider interfaces.WorkflowProvider,
	ctx interfaces.UnifiedContext,
	continueAsNewer *ContinueAsNewer,
	attributeSyncer *AttributeSynchronizer,
) *TerminalCoordinator {
	if provider == nil || ctx == nil || continueAsNewer == nil || attributeSyncer == nil {
		panic("TerminalCoordinator requires non-nil dependencies")
	}
	return &TerminalCoordinator{
		provider:        provider,
		ctx:             ctx,
		continueAsNewer: continueAsNewer,
		attributeSyncer: attributeSyncer,
	}
}

func (c *TerminalCoordinator) CoordinateAndFinalizeError(retErr error) error {
	if c.provider.IsContinueAsNewError(retErr) {
		return retErr
	}
	forceCompletion := c.kind == terminalForceCompletion
	if c.kind == terminalCancel || c.kind == terminalFailure {
		retErr = c.resultErr
	}
	if err := c.provider.Await(c.ctx, func() bool {
		return forceCompletion ||
			(c.attributeSyncer.ProducersDrained() && c.continueAsNewer.inflightUpdateOperations == 0)
	}); err != nil {
		return err
	}
	if err := c.attributeSyncer.FlushAndClose(c.ctx); err != nil {
		return err
	}
	return retErr
}

func (c *TerminalCoordinator) requestClientStop(request *dexpb.StopFlowSignalRequest) {
	if request == nil || c.IsRequested() {
		return
	}
	switch request.GetStopType() {
	case dexpb.StopType_STOP_TYPE_CANCEL:
		c.kind = terminalCancel
		c.resultErr = c.provider.NewCanceledError(request.GetReason())
	case dexpb.StopType_STOP_TYPE_FAIL:
		reason := request.GetReason()
		if reason == "" {
			reason = "fail by client"
		}
		c.requestFailure(c.provider.NewFlowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW,
			&dexpb.ErrorResponse{Detail: reason},
		))
	}
}

func (c *TerminalCoordinator) requestFailure(cause error) {
	if c.IsRequested() {
		return
	}
	if cause == nil {
		cause = c.provider.NewFlowError(
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL,
			&dexpb.ErrorResponse{Detail: "terminal failure has no cause"},
		)
	}
	c.kind = terminalFailure
	c.resultErr = cause
}

func (c *TerminalCoordinator) requestForceCompletion() {
	if c.IsRequested() {
		return
	}
	c.kind = terminalForceCompletion
}

func (c *TerminalCoordinator) IsRequested() bool {
	return c.kind != terminalNone
}
