// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package interpreter

import (
	"errors"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type StepExecutionRegistry struct {
	provider             interfaces.WorkflowProvider
	stepRequestQueue     *StepRequestQueue
	stepExecutionCounter *StepExecutionCounter
	continueAsNewer      *ContinueAsNewer
	timerProcessor       interfaces.TimerProcessor
	subFlowTracker       *SubFlowTracker
	activeExecutions     map[string]*registeredStepExecution
	canceledExecutionIDs map[string]struct{}
}

type registeredStepExecution struct {
	movement *dexpb.StepMovement
	cancel   func()
}

func NewStepExecutionRegistry(
	provider interfaces.WorkflowProvider,
	stepRequestQueue *StepRequestQueue,
	stepExecutionCounter *StepExecutionCounter,
	continueAsNewer *ContinueAsNewer,
	timerProcessor interfaces.TimerProcessor,
	subFlowTracker *SubFlowTracker,
) *StepExecutionRegistry {
	if provider == nil || stepRequestQueue == nil || stepExecutionCounter == nil ||
		continueAsNewer == nil || timerProcessor == nil || subFlowTracker == nil {
		panic("step execution registry requires non-nil dependencies")
	}
	return &StepExecutionRegistry{
		provider:             provider,
		stepRequestQueue:     stepRequestQueue,
		stepExecutionCounter: stepExecutionCounter,
		continueAsNewer:      continueAsNewer,
		timerProcessor:       timerProcessor,
		subFlowTracker:       subFlowTracker,
		activeExecutions:     map[string]*registeredStepExecution{},
		canceledExecutionIDs: map[string]struct{}{},
	}
}

func (r *StepExecutionRegistry) Register(
	ctx interfaces.UnifiedContext,
	request StepRequest,
) (interfaces.UnifiedContext, string) {
	movement := request.GetStepMovement()
	stepExecutionID := ""
	if request.IsResumeRequest() {
		stepExecutionID = request.GetStepResumeRequest().GetStepExecutionId()
	} else {
		stepExecutionID = r.stepExecutionCounter.CreateNextExecutionId(movement.GetStepType())
	}
	executionCtx, cancel := r.provider.WithCancel(ctx)
	r.activeExecutions[stepExecutionID] = &registeredStepExecution{
		movement: movement,
		cancel:   cancel,
	}
	r.continueAsNewer.TrackActiveStep(stepExecutionID, movement)
	return executionCtx, stepExecutionID
}

func (r *StepExecutionRegistry) Unregister(stepExecutionID string) {
	delete(r.activeExecutions, stepExecutionID)
	delete(r.canceledExecutionIDs, stepExecutionID)
}

func (r *StepExecutionRegistry) CancelAll(ctx interfaces.UnifiedContext) error {
	return r.doCancel(ctx, newAllStepCancellationSelector())
}

func (r *StepExecutionRegistry) CancelByStepTypesAndSiblingStepTypes(
	ctx interfaces.UnifiedContext,
	decision *dexpb.StepDecision,
	fromStepExecutionID string,
) error {
	return r.doCancel(
		ctx,
		newStepCancellationSelector(
			decision.GetCancelStepTypes(),
			decision.GetCancelSiblingStepTypes(),
			fromStepExecutionID,
		),
	)
}

func (r *StepExecutionRegistry) CancelByStepTypes(
	ctx interfaces.UnifiedContext,
	stepTypes []string,
) error {
	return r.doCancel(
		ctx,
		newStepCancellationSelector(stepTypes, nil, ""),
	)
}

// CancelAll cancels every queued and active Step execution.
func (r *StepExecutionRegistry) CancelAll(ctx interfaces.UnifiedContext) error {
	return r.doCancel(ctx, newAllStepCancellationSelector())
}

func (r *StepExecutionRegistry) doCancel(
	ctx interfaces.UnifiedContext,
	selector *stepCancellationSelector,
) error {
	if selector.isEmpty() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	queued := r.stepRequestQueue.RemoveMatching(func(request StepRequest) bool {
		return selector.matches(request.GetStepMovement())
	})

	matched := map[string]*registeredStepExecution{}
	for stepExecutionID, execution := range r.activeExecutions {
		if selector.matches(execution.movement) {
			r.canceledExecutionIDs[stepExecutionID] = struct{}{}
			matched[stepExecutionID] = execution
		}
	}
	matchedIDs := DeterministicKeys(matched)
	for _, stepExecutionID := range matchedIDs {
		delete(r.activeExecutions, stepExecutionID)
	}
	var cancelErr error
	for _, request := range queued {
		cancelErr = errors.Join(cancelErr, r.cancelQueued(ctx, request))
	}
	for _, stepExecutionID := range matchedIDs {
		cancelErr = errors.Join(
			cancelErr,
			r.cancelActive(ctx, stepExecutionID, matched[stepExecutionID]),
		)
	}
	return cancelErr
}

func (r *StepExecutionRegistry) IsCanceled(stepExecutionID string) bool {
	_, isCanceled := r.canceledExecutionIDs[stepExecutionID]
	return isCanceled
}

func (r *StepExecutionRegistry) cancelQueued(
	ctx interfaces.UnifiedContext,
	request StepRequest,
) error {
	movement := request.GetStepMovement()
	if request.IsResumeRequest() {
		stepExecutionID := request.GetStepResumeRequest().GetStepExecutionId()
		r.subFlowTracker.Unregister(stepExecutionID)
		r.continueAsNewer.RemoveStepExecutionToResume(stepExecutionID)
		if err := r.stepExecutionCounter.MarkStepExecutionCompleted(
			ctx,
			movement,
			stepExecutionID,
			nil,
		); err != nil {
			return err
		}
	} else {
		r.stepExecutionCounter.MarkQueuedStepExecutionCanceled(movement)
	}
	return nil
}

func (r *StepExecutionRegistry) cancelActive(
	ctx interfaces.UnifiedContext,
	stepExecutionID string,
	execution *registeredStepExecution,
) error {
	execution.cancel()
	r.timerProcessor.RemovePendingTimersOfStep(stepExecutionID)
	r.subFlowTracker.Unregister(stepExecutionID)
	r.continueAsNewer.RemoveStepExecutionToResume(stepExecutionID)
	r.continueAsNewer.RemoveActiveStep(stepExecutionID)
	return r.stepExecutionCounter.MarkStepExecutionCompleted(
		ctx,
		execution.movement,
		stepExecutionID,
		nil,
	)
}

type stepCancellationSelector struct {
	matchesAllSteps     bool
	globalStepTypes     map[string]bool
	siblingStepTypes    map[string]bool
	fromStepExecutionID string
}

func newAllStepCancellationSelector() *stepCancellationSelector {
	return &stepCancellationSelector{matchesAllSteps: true}
}

func newStepCancellationSelector(
	cancelStepTypes []string,
	cancelSiblingStepTypes []string,
	fromStepExecutionID string,
) *stepCancellationSelector {
	globalStepTypes := make(map[string]bool, len(cancelStepTypes))
	for _, stepType := range cancelStepTypes {
		globalStepTypes[stepType] = true
	}
	siblingStepTypes := make(
		map[string]bool,
		len(cancelSiblingStepTypes),
	)
	for _, stepType := range cancelSiblingStepTypes {
		if !globalStepTypes[stepType] {
			siblingStepTypes[stepType] = true
		}
	}
	return &stepCancellationSelector{
		globalStepTypes:     globalStepTypes,
		siblingStepTypes:    siblingStepTypes,
		fromStepExecutionID: fromStepExecutionID,
	}
}

func (s *stepCancellationSelector) isEmpty() bool {
	return !s.matchesAllSteps && len(s.globalStepTypes) == 0 && len(s.siblingStepTypes) == 0
}

func (s *stepCancellationSelector) matches(
	movement *dexpb.StepMovement,
) bool {
	stepType := movement.GetStepType()
	return s.matchesAllSteps ||
		s.globalStepTypes[stepType] ||
		(s.siblingStepTypes[stepType] &&
			movement.GetFromStepExecutionIdInternalOnly() == s.fromStepExecutionID)
}
