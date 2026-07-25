// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package interpreter

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/interpreter/config"
	"github.com/superdurable/iwf/service/interpreter/cont"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
)

type StepExecutionCounter struct {
	ctx                  interfaces.UnifiedContext
	provider             interfaces.WorkflowProvider
	configer             *config.FlowConfiger
	continueAsNewCounter *cont.ContinueAsNewCounter

	// For creating stepExecutionId: count the stepId for how many times that have been started
	stepTypeStartedCounts map[string]int32
	// For system search attribute ActiveStepId: keep counting the stateIds that are executing based on the ExecutingStateIdMode
	stepTypeActiveCounts map[string]int32
	// For "dead ends": count the total pending states
	totalActiveCount int32
}

func NewStepExecutionCounter(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	configer *config.FlowConfiger,
	continueAsNewCounter *cont.ContinueAsNewCounter,
) *StepExecutionCounter {
	return &StepExecutionCounter{
		ctx:                   ctx,
		provider:              provider,
		configer:              configer,
		continueAsNewCounter:  continueAsNewCounter,
		stepTypeStartedCounts: map[string]int32{},
		stepTypeActiveCounts:  map[string]int32{},
	}
}

func RebuildStepExecutionCounter(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	configer *config.FlowConfiger,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	counterInfo *iwfpb.StepExecutionCounterInfo,
) *StepExecutionCounter {
	return &StepExecutionCounter{
		ctx:                   ctx,
		provider:              provider,
		configer:              configer,
		continueAsNewCounter:  continueAsNewCounter,
		stepTypeStartedCounts: counterInfo.GetStepTypeStartedCount(),
		stepTypeActiveCounts:  counterInfo.GetStepTypeCurrentlyExecutingCount(),
		totalActiveCount:      counterInfo.GetTotalCurrentlyExecutingCount(),
	}
}

func (e *StepExecutionCounter) Dump() *iwfpb.StepExecutionCounterInfo {
	return &iwfpb.StepExecutionCounterInfo{
		StepTypeStartedCount:            e.stepTypeStartedCounts,
		StepTypeCurrentlyExecutingCount: e.stepTypeActiveCounts,
		TotalCurrentlyExecutingCount:    e.totalActiveCount,
	}
}

func (e *StepExecutionCounter) CreateNextExecutionId(stepType string) string {
	e.stepTypeStartedCounts[stepType]++
	id := e.stepTypeStartedCounts[stepType]
	return fmt.Sprintf("%v-%v", stepType, id)
}

func (e *StepExecutionCounter) MarkStepTypeActiveIfNotYet(
	requests []StepRequest,
) error {
	needsUpdateSA := false
	numOfNew := int32(0)
	for _, request := range requests {
		if request.IsResumeRequest() {
			continue
		}
		step := request.GetStepStartRequest()
		if e.shouldTrackActiveStep(step) {
			if e.increaseStepIdActiveCounts(step) {
				needsUpdateSA = true
			}
		}
		numOfNew++
	}
	e.totalActiveCount += numOfNew

	if needsUpdateSA {
		return e.refreshActiveStepIdSearchAttribute()
	}

	return nil
}

func (e *StepExecutionCounter) MarkStepExecutionCompleted(
	currentStep *iwfpb.StepMovement,
	nextSteps []*iwfpb.StepMovement,
) error {
	e.totalActiveCount--

	options := currentStep.GetStepOptions()
	skipWaitFor := options.GetSkipWaitFor()
	e.continueAsNewCounter.IncExecutedStepExecution(skipWaitFor)

	if !e.shouldTrackActiveStep(currentStep) {
		return nil
	}

	e.decreaseStepIdActiveCounts(currentStep)

	enabledForAll := e.configer.EffectiveActiveStepSearchMode() ==
		iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL
	shouldSkipRefresh := determineIfShouldSkipRefreshOnCompleted(nextSteps, enabledForAll)
	if shouldSkipRefresh {
		return nil
	}
	return e.refreshActiveStepIdSearchAttribute()
}

func (e *StepExecutionCounter) increaseStepIdActiveCounts(s *iwfpb.StepMovement) bool {
	e.stepTypeActiveCounts[s.StepType]++
	// first time the stateId show up
	return e.stepTypeActiveCounts[s.StepType] == 1
}

func (e *StepExecutionCounter) decreaseStepIdActiveCounts(step *iwfpb.StepMovement) {
	e.stepTypeActiveCounts[step.GetStepType()]--
	if e.stepTypeActiveCounts[step.GetStepType()] == 0 {
		delete(e.stepTypeActiveCounts, step.GetStepType())
	}
}

// as an optimization, we want to skip refreshing the search attribute
// if there are no non-closing next steps, to avoid unnecessary refreshes
func determineIfShouldSkipRefreshOnCompleted(
	nextSteps []*iwfpb.StepMovement,
	enabledForAll bool,
) bool {
	var nonClosingNextSteps []*iwfpb.StepMovement
	for _, step := range nextSteps {
		if _, ok := service.ValidClosingFlowStepType[step.GetStepType()]; !ok {
			// step is not a ValidClosingFlowStepType
			nonClosingNextSteps = append(nonClosingNextSteps, step)
		}
	}
	if enabledForAll {
		if len(nonClosingNextSteps) > 0 {
			return true
		}
	} else {
		for _, step := range nonClosingNextSteps {
			if !step.GetStepOptions().GetSkipWaitFor() {
				return true
			}
		}
	}
	return false
}

func (e *StepExecutionCounter) shouldTrackActiveStep(step *iwfpb.StepMovement) bool {
	switch e.configer.EffectiveActiveStepSearchMode() {
	case iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED:
		return false
	case iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL:
		return true
	case iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR:
		return !step.GetStepOptions().GetSkipWaitFor()
	default:
		panic("FlowConfiger returned an invalid active step search mode")
	}
}

func (e *StepExecutionCounter) GetTotalCurrentlyExecutingCount() int32 {
	return e.totalActiveCount
}

func (e *StepExecutionCounter) refreshActiveStepIdSearchAttribute() error {
	// Optimization: don't upsert SAs if currentSAsValues == stepTypeActiveCounts keys
	currentSAsValues, err := e.provider.GetSearchAttributeKeywordArray(
		e.ctx,
		service.SearchAttributeActiveStepIds,
	)
	if err != nil {
		e.provider.GetLogger(e.ctx).Error("error for GetSearchAttributes", err)
		return err
	}

	activeStepIds := DeterministicKeys(e.stepTypeActiveCounts)

	slices.Sort(currentSAsValues)
	slices.Sort(activeStepIds)
	if reflect.DeepEqual(currentSAsValues, activeStepIds) {
		return nil
	}

	return e.provider.UpsertSearchAttributes(e.ctx, map[string]interface{}{
		service.SearchAttributeActiveStepIds: activeStepIds,
	})
}
