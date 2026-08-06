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
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type StepExecutionCounter struct {
	ctx                  interfaces.UnifiedContext
	provider             interfaces.WorkflowProvider
	configer             *config.FlowConfiger
	continueAsNewCounter *cont.ContinueAsNewCounter
	indexSynchronizer    *IndexSynchronizer

	// For creating stepExecutionId: count the stepId for how many times that have been started
	stepTypeStartedCounts map[string]int32
	// For system search attribute ActiveStepTypes: keep counting the step types that are executing based on ActiveStepSearchMode
	stepTypeActiveCounts map[string]int32
	// For waitForStepExecution features, works together with stepTypeStartedCounts:
	// 1. if waitForStepExecutionId number > startedCount, it means that the step is not started yet
	// 2. if <= or not existing in stepTypeStartedCounts, meaning it hasn't started, then check if the number existing in stepActiveExecutionNums
	stepActiveExecutionNums map[string][]int32
	// For "dead ends": count the total pending states
	totalActiveCount int32
}

func NewStepExecutionCounter(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	configer *config.FlowConfiger,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	indexSynchronizer *IndexSynchronizer,
) *StepExecutionCounter {
	return &StepExecutionCounter{
		ctx:                     ctx,
		provider:                provider,
		configer:                configer,
		continueAsNewCounter:    continueAsNewCounter,
		indexSynchronizer:       indexSynchronizer,
		stepTypeStartedCounts:   map[string]int32{},
		stepTypeActiveCounts:    map[string]int32{},
		stepActiveExecutionNums: map[string][]int32{},
	}
}

func RebuildStepExecutionCounter(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	configer *config.FlowConfiger,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	counterInfo *dexpb.StepExecutionCounterInfo,
	indexSynchronizer *IndexSynchronizer,
) *StepExecutionCounter {
	stepTypeStartedCounts := counterInfo.GetStepTypeStartedCount()
	if stepTypeStartedCounts == nil {
		stepTypeStartedCounts = map[string]int32{}
	}
	stepTypeActiveCounts := counterInfo.GetStepTypeCurrentlyExecutingCount()
	if stepTypeActiveCounts == nil {
		stepTypeActiveCounts = map[string]int32{}
	}
	return &StepExecutionCounter{
		ctx:                   ctx,
		provider:              provider,
		configer:              configer,
		continueAsNewCounter:  continueAsNewCounter,
		indexSynchronizer:     indexSynchronizer,
		stepTypeStartedCounts: stepTypeStartedCounts,
		stepTypeActiveCounts:  stepTypeActiveCounts,
		stepActiveExecutionNums: stepActiveExecutionNumsFromProto(
			counterInfo.GetStepActiveExecutionNums(),
		),
		totalActiveCount: counterInfo.GetTotalCurrentlyExecutingCount(),
	}
}

func stepActiveExecutionNumsFromProto(
	values map[string]*dexpb.StepExecutionNumbers,
) map[string][]int32 {
	result := make(map[string][]int32, len(values))
	for stepType, executionNumbers := range values {
		if executionNumbers == nil {
			panic("step active execution numbers are nil")
		}
		result[stepType] = executionNumbers.GetNumbers()
	}
	return result
}

func (e *StepExecutionCounter) Dump() *dexpb.StepExecutionCounterInfo {
	return &dexpb.StepExecutionCounterInfo{
		StepTypeStartedCount:            e.stepTypeStartedCounts,
		StepTypeCurrentlyExecutingCount: e.stepTypeActiveCounts,
		TotalCurrentlyExecutingCount:    e.totalActiveCount,
		StepActiveExecutionNums:         stepActiveExecutionNumsToProto(e.stepActiveExecutionNums),
	}
}

func stepActiveExecutionNumsToProto(
	values map[string][]int32,
) map[string]*dexpb.StepExecutionNumbers {
	result := make(map[string]*dexpb.StepExecutionNumbers, len(values))
	for stepType, executionNumbers := range values {
		result[stepType] = &dexpb.StepExecutionNumbers{Numbers: executionNumbers}
	}
	return result
}

func (e *StepExecutionCounter) CreateNextExecutionId(stepType string) string {
	e.stepTypeStartedCounts[stepType]++
	executionNumber := e.stepTypeStartedCounts[stepType]
	e.stepActiveExecutionNums[stepType] = append(
		e.stepActiveExecutionNums[stepType],
		executionNumber,
	)
	return formatStepExecutionId(stepType, executionNumber)
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
		return e.refreshActiveStepTypeSearchAttribute()
	}

	return nil
}

func (e *StepExecutionCounter) MarkStepExecutionCompleted(
	currentStep *dexpb.StepMovement,
	stepExecutionId string,
	nextSteps []*dexpb.StepMovement,
) error {
	e.totalActiveCount--
	e.removeStepActiveExecutionNum(
		currentStep.GetStepType(),
		parseStepExecutionNumber(currentStep.GetStepType(), stepExecutionId),
	)

	options := currentStep.GetStepOptions()
	skipWaitFor := options.GetSkipWaitFor()
	e.continueAsNewCounter.IncExecutedStepExecution(skipWaitFor)

	if !e.shouldTrackActiveStep(currentStep) {
		return nil
	}

	e.decreaseStepIdActiveCounts(currentStep)

	enabledForAll := e.configer.EffectiveActiveStepSearchMode() ==
		dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL
	shouldSkipRefresh := determineIfShouldSkipRefreshOnCompleted(nextSteps, enabledForAll)
	if shouldSkipRefresh {
		return nil
	}
	return e.refreshActiveStepTypeSearchAttribute()
}

func (e *StepExecutionCounter) removeStepActiveExecutionNum(
	stepType string,
	stepExecutionNumber int32,
) {
	executionNumbers := e.stepActiveExecutionNums[stepType]
	index := slices.Index(executionNumbers, stepExecutionNumber)
	if index < 0 {
		panic("completed step execution is not active")
	}
	executionNumbers = append(executionNumbers[:index], executionNumbers[index+1:]...)
	if len(executionNumbers) == 0 {
		delete(e.stepActiveExecutionNums, stepType)
		return
	}
	e.stepActiveExecutionNums[stepType] = executionNumbers
}

func (e *StepExecutionCounter) IsStepExecutionCompleted(
	stepType string,
	stepExecutionNumber int32,
) bool {
	if stepExecutionNumber <= 0 {
		panic("step execution number must be positive")
	}
	if e.stepTypeStartedCounts[stepType] < stepExecutionNumber {
		return false
	}
	return !slices.Contains(
		e.stepActiveExecutionNums[stepType],
		stepExecutionNumber,
	)
}

func (e *StepExecutionCounter) IsStepExecutionActive(stepExeId string) bool {
	stepType, stepExecutionNumber := splitStepExecutionId(stepExeId)
	return !e.IsStepExecutionCompleted(stepType, stepExecutionNumber)
}

func (e *StepExecutionCounter) increaseStepIdActiveCounts(s *dexpb.StepMovement) bool {
	e.stepTypeActiveCounts[s.StepType]++
	// first time the stateId show up
	return e.stepTypeActiveCounts[s.StepType] == 1
}

func (e *StepExecutionCounter) decreaseStepIdActiveCounts(step *dexpb.StepMovement) {
	e.stepTypeActiveCounts[step.GetStepType()]--
	if e.stepTypeActiveCounts[step.GetStepType()] == 0 {
		delete(e.stepTypeActiveCounts, step.GetStepType())
	}
}

// as an optimization, we want to skip refreshing the search attribute
// if there are next steps, to avoid unnecessary refreshes
func determineIfShouldSkipRefreshOnCompleted(
	nextSteps []*dexpb.StepMovement,
	enabledForAll bool,
) bool {
	if enabledForAll {
		if len(nextSteps) > 0 {
			return true
		}
	} else {
		for _, step := range nextSteps {
			if !step.GetStepOptions().GetSkipWaitFor() {
				return true
			}
		}
	}
	return false
}

func (e *StepExecutionCounter) shouldTrackActiveStep(step *dexpb.StepMovement) bool {
	switch e.configer.EffectiveActiveStepSearchMode() {
	case dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED:
		return false
	case dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL:
		return true
	case dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR:
		return !step.GetStepOptions().GetSkipWaitFor()
	default:
		panic("FlowConfiger returned an invalid active step search mode")
	}
}

func (e *StepExecutionCounter) GetTotalCurrentlyExecutingCount() int32 {
	return e.totalActiveCount
}

func (e *StepExecutionCounter) refreshActiveStepTypeSearchAttribute() error {
	if e.indexSynchronizer != nil {
		e.indexSynchronizer.UpdateActiveStepTypes(DeterministicKeys(e.stepTypeActiveCounts))
		return nil
	}
	// Optimization: don't upsert SAs if currentSAsValues == stepTypeActiveCounts keys
	currentSAsValues, err := e.provider.GetSearchAttributeKeywordArray(
		e.ctx,
		service.SearchAttributeActiveStepTypes,
	)
	if err != nil {
		e.provider.GetLogger(e.ctx).Error("error for GetSearchAttributes", err)
		return err
	}

	activeStepTypes := DeterministicKeys(e.stepTypeActiveCounts)

	slices.Sort(currentSAsValues)
	slices.Sort(activeStepTypes)
	if reflect.DeepEqual(currentSAsValues, activeStepTypes) {
		return nil
	}

	return e.provider.UpsertSearchAttributes(e.ctx, map[string]interface{}{
		service.SearchAttributeActiveStepTypes: activeStepTypes,
	})
}

func formatStepExecutionId(stepType string, stepExecutionNumber int32) string {
	return fmt.Sprintf("%v-%v", stepType, stepExecutionNumber)
}

func splitStepExecutionId(stepExeId string) (stepType string, stepExecutionNumber int32) {
	idx := strings.LastIndex(stepExeId, "-")
	if idx <= 0 || idx == len(stepExeId)-1 {
		panic("step execution ID has an invalid format")
	}
	stepType = stepExeId[:idx]
	return stepType, parseStepExecutionNumber(stepType, stepExeId)
}

func parseStepExecutionNumber(stepType, stepExecutionId string) int32 {
	prefix := stepType + "-"
	if !strings.HasPrefix(stepExecutionId, prefix) {
		panic("step execution ID does not match step type")
	}
	number, err := strconv.ParseInt(
		strings.TrimPrefix(stepExecutionId, prefix),
		10,
		32,
	)
	if err != nil || number <= 0 {
		panic("step execution ID has an invalid execution number")
	}
	return int32(number)
}
