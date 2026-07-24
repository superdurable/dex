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
	"sort"

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

	stepTypeStartedCounts            map[string]int32
	stepTypeCurrentlyExecutingCounts map[string]int32
	totalCurrentlyExecutingCount     int32
}

func NewStepExecutionCounter(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	configer *config.FlowConfiger,
	continueAsNewCounter *cont.ContinueAsNewCounter,
) *StepExecutionCounter {
	if provider == nil {
		panic("StepExecutionCounter requires a WorkflowProvider")
	}
	if configer == nil || continueAsNewCounter == nil {
		panic("StepExecutionCounter requires config and continue-as-new counter")
	}
	return &StepExecutionCounter{
		ctx:                              ctx,
		provider:                         provider,
		configer:                         configer,
		continueAsNewCounter:             continueAsNewCounter,
		stepTypeStartedCounts:            map[string]int32{},
		stepTypeCurrentlyExecutingCounts: map[string]int32{},
	}
}

func RebuildStepExecutionCounter(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	configer *config.FlowConfiger,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	counterInfo *iwfpb.StepExecutionCounterInfo,
) *StepExecutionCounter {
	if counterInfo == nil {
		panic("StepExecutionCounter restore requires counter info")
	}
	counter := NewStepExecutionCounter(ctx, provider, configer, continueAsNewCounter)
	counter.stepTypeStartedCounts = counterInfo.GetStepTypeStartedCount()
	counter.stepTypeCurrentlyExecutingCounts = counterInfo.GetStepTypeCurrentlyExecutingCount()
	counter.totalCurrentlyExecutingCount = counterInfo.GetTotalCurrentlyExecutingCount()
	counter.validateCounts()
	return counter
}

func (e *StepExecutionCounter) validateCounts() {
	if e.totalCurrentlyExecutingCount < 0 {
		panic("negative total currently executing count")
	}
	for stepType, count := range e.stepTypeStartedCounts {
		if stepType == "" || count < 0 {
			panic("invalid restored step started count")
		}
	}
	var trackedCount int32
	for stepType, count := range e.stepTypeCurrentlyExecutingCounts {
		if stepType == "" || count <= 0 {
			panic("invalid restored active step count")
		}
		trackedCount += count
	}
	if trackedCount > e.totalCurrentlyExecutingCount {
		panic("active step count exceeds total executing count")
	}
}

func (e *StepExecutionCounter) Dump() *iwfpb.StepExecutionCounterInfo {
	return &iwfpb.StepExecutionCounterInfo{
		StepTypeStartedCount:            e.stepTypeStartedCounts,
		StepTypeCurrentlyExecutingCount: e.stepTypeCurrentlyExecutingCounts,
		TotalCurrentlyExecutingCount:    e.totalCurrentlyExecutingCount,
	}
}

func (e *StepExecutionCounter) CreateNextExecutionId(stepType string) string {
	if stepType == "" {
		panic("step execution ID requires a step type")
	}
	e.stepTypeStartedCounts[stepType]++
	return fmt.Sprintf("%s-%d", stepType, e.stepTypeStartedCounts[stepType])
}

func (e *StepExecutionCounter) MarkStepTypeExecutingIfNotYet(
	requests []StepRequest,
) error {
	additions := make(map[string]int32)
	var newExecutions int32
	for _, request := range requests {
		if request.IsResumeRequest() {
			continue
		}
		step := request.GetStepStartRequest()
		newExecutions++
		if e.shouldTrackActiveStep(step) {
			additions[step.GetStepType()]++
		}
	}
	if newExecutions == 0 {
		return nil
	}

	if activeStepSetChangesOnAdd(e.stepTypeCurrentlyExecutingCounts, additions) {
		activeStepTypes := activeStepTypesAfterAdd(e.stepTypeCurrentlyExecutingCounts, additions)
		if err := e.upsertActiveStepTypes(activeStepTypes); err != nil {
			return err
		}
	}
	for stepType, count := range additions {
		e.stepTypeCurrentlyExecutingCounts[stepType] += count
	}
	e.totalCurrentlyExecutingCount += newExecutions
	return nil
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

func activeStepSetChangesOnAdd(current, additions map[string]int32) bool {
	for stepType, count := range additions {
		if count > 0 && current[stepType] == 0 {
			return true
		}
	}
	return false
}

func activeStepTypesAfterAdd(current, additions map[string]int32) []string {
	stepTypes := make([]string, 0, len(current)+len(additions))
	for stepType := range current {
		stepTypes = append(stepTypes, stepType)
	}
	for stepType, count := range additions {
		if count > 0 && current[stepType] == 0 {
			stepTypes = append(stepTypes, stepType)
		}
	}
	sort.Strings(stepTypes)
	return stepTypes
}

func (e *StepExecutionCounter) MarkStepExecutionCompleted(
	step *iwfpb.StepMovement,
	nextSteps []*iwfpb.StepMovement,
) error {
	if step == nil || step.GetStepType() == "" {
		panic("step completion requires a movement")
	}
	if e.totalCurrentlyExecutingCount <= 0 {
		panic("step execution count underflow")
	}

	stepType := step.GetStepType()
	var trackedCount int32
	if e.shouldTrackActiveStep(step) {
		trackedCount = e.stepTypeCurrentlyExecutingCounts[stepType]
		if trackedCount == 0 {
			panic("active step execution count underflow")
		}
	}
	if trackedCount == 1 &&
		!determineIfShouldSkipRefreshOnCompleted(
			nextSteps,
			e.configer.EffectiveActiveStepSearchMode() ==
				iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL,
		) {
		activeStepTypes := activeStepTypesWithout(e.stepTypeCurrentlyExecutingCounts, stepType)
		if err := e.upsertActiveStepTypes(activeStepTypes); err != nil {
			return err
		}
	}

	e.totalCurrentlyExecutingCount--
	e.continueAsNewCounter.IncExecutedStepExecution(
		step.GetStepOptions().GetSkipWaitFor(),
	)
	if trackedCount > 1 {
		e.stepTypeCurrentlyExecutingCounts[stepType]--
	} else if trackedCount == 1 {
		delete(e.stepTypeCurrentlyExecutingCounts, stepType)
	}
	return nil
}

func determineIfShouldSkipRefreshOnCompleted(
	nextSteps []*iwfpb.StepMovement,
	enabledForAll bool,
) bool {
	for _, step := range nextSteps {
		if service.ValidClosingFlowStepType[step.GetStepType()] {
			continue
		}
		if enabledForAll || !step.GetStepOptions().GetSkipWaitFor() {
			return true
		}
	}
	return false
}

func activeStepTypesWithout(current map[string]int32, removedStepType string) []string {
	stepTypes := make([]string, 0, len(current))
	for stepType := range current {
		if stepType != removedStepType {
			stepTypes = append(stepTypes, stepType)
		}
	}
	sort.Strings(stepTypes)
	return stepTypes
}

func (e *StepExecutionCounter) upsertActiveStepTypes(stepTypes []string) error {
	if err := e.provider.UpsertSearchAttributes(e.ctx, map[string]interface{}{
		service.SearchAttributeExecutingStateIds: stepTypes,
	}); err != nil {
		return fmt.Errorf("upsert active step search attribute: %w", err)
	}
	return nil
}

func (e *StepExecutionCounter) GetTotalCurrentlyExecutingCount() int32 {
	return e.totalCurrentlyExecutingCount
}
