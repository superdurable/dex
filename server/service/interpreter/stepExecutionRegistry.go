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

type stepExecutionRegistry struct {
	provider             interfaces.WorkflowProvider
	stepRequestQueue     *StepRequestQueue
	stepExecutionCounter *StepExecutionCounter
	continueAsNewer      *ContinueAsNewer
	timerProcessor       interfaces.TimerProcessor
	persistenceManager   *PersistenceManager
	subFlowTracker       *SubFlowTracker
	activeExecutions     map[string]*registeredStepExecution
	canceledExecutionIDs map[string]struct{}
}

type registeredStepExecution struct {
	movement   *dexpb.StepMovement
	cancel     func()
	lockedKeys []string
}

type stepCancellationSelector struct {
	globalStepTypes     map[string]bool
	siblingStepTypes    map[string]bool
	fromStepExecutionID string
}

func newStepExecutionRegistry(
	provider interfaces.WorkflowProvider,
	stepRequestQueue *StepRequestQueue,
	stepExecutionCounter *StepExecutionCounter,
	continueAsNewer *ContinueAsNewer,
	timerProcessor interfaces.TimerProcessor,
	persistenceManager *PersistenceManager,
	subFlowTracker *SubFlowTracker,
) *stepExecutionRegistry {
	if provider == nil || stepRequestQueue == nil || stepExecutionCounter == nil ||
		continueAsNewer == nil || timerProcessor == nil || persistenceManager == nil ||
		subFlowTracker == nil {
		panic("step execution registry requires non-nil dependencies")
	}
	return &stepExecutionRegistry{
		provider:             provider,
		stepRequestQueue:     stepRequestQueue,
		stepExecutionCounter: stepExecutionCounter,
		continueAsNewer:      continueAsNewer,
		timerProcessor:       timerProcessor,
		persistenceManager:   persistenceManager,
		subFlowTracker:       subFlowTracker,
		activeExecutions:     map[string]*registeredStepExecution{},
		canceledExecutionIDs: map[string]struct{}{},
	}
}

func (r *stepExecutionRegistry) CancelSelected(
	decision *dexpb.StepDecision,
	fromStepExecutionID string,
) error {
	return r.cancelSelected(newStepCancellationSelector(
		decision.GetCancelStepTypes(),
		decision.GetCancelSiblingStepTypes(),
		fromStepExecutionID,
	))
}

func (r *stepExecutionRegistry) CancelStepTypes(stepTypes []string) error {
	return r.cancelSelected(newStepCancellationSelector(stepTypes, nil, ""))
}

func (r *stepExecutionRegistry) cancelSelected(selector *stepCancellationSelector) error {
	if selector.isEmpty() {
		return nil
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
		cancelErr = errors.Join(cancelErr, r.cancelQueued(request))
	}
	for _, stepExecutionID := range matchedIDs {
		cancelErr = errors.Join(
			cancelErr,
			r.cancelActive(stepExecutionID, matched[stepExecutionID]),
		)
	}
	return cancelErr
}

func (r *stepExecutionRegistry) Start(
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

func (r *stepExecutionRegistry) Unregister(stepExecutionID string) {
	delete(r.activeExecutions, stepExecutionID)
	delete(r.canceledExecutionIDs, stepExecutionID)
}

func (r *stepExecutionRegistry) TrackLockedKeys(stepExecutionID string, keys []string) {
	if execution := r.activeExecutions[stepExecutionID]; execution != nil {
		execution.lockedKeys = keys
	}
}

func (r *stepExecutionRegistry) IsCanceled(stepExecutionID string) bool {
	_, isCanceled := r.canceledExecutionIDs[stepExecutionID]
	return isCanceled
}

func (r *stepExecutionRegistry) cancelQueued(
	request StepRequest,
) error {
	movement := request.GetStepMovement()
	if request.IsResumeRequest() {
		stepExecutionID := request.GetStepResumeRequest().GetStepExecutionId()
		r.subFlowTracker.Unregister(stepExecutionID)
		r.continueAsNewer.RemoveStepExecutionToResume(stepExecutionID)
		if err := r.stepExecutionCounter.MarkStepExecutionCompleted(
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

func (r *stepExecutionRegistry) cancelActive(
	stepExecutionID string,
	execution *registeredStepExecution,
) error {
	execution.cancel()
	r.persistenceManager.UnlockKeys(execution.lockedKeys)
	r.timerProcessor.RemovePendingTimersOfStep(stepExecutionID)
	r.subFlowTracker.Unregister(stepExecutionID)
	r.continueAsNewer.RemoveStepExecutionToResume(stepExecutionID)
	r.continueAsNewer.RemoveActiveStep(stepExecutionID)
	return r.stepExecutionCounter.MarkStepExecutionCompleted(
		execution.movement,
		stepExecutionID,
		nil,
	)
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
	return len(s.globalStepTypes) == 0 && len(s.siblingStepTypes) == 0
}

func (s *stepCancellationSelector) matches(
	movement *dexpb.StepMovement,
) bool {
	stepType := movement.GetStepType()
	return s.globalStepTypes[stepType] ||
		(s.siblingStepTypes[stepType] &&
			movement.GetFromStepExecutionIdInternalOnly() == s.fromStepExecutionID)
}
