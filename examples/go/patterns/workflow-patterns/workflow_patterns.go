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

// Package workflowpatterns implements the WCP-1 to WCP-43 control-flow patterns as Dex Flows.
package workflowpatterns

import (
	"errors"
	"slices"

	"github.com/superdurable/dex/sdk-go/dex"
)

// WorkflowPatternFlows holds one instance of every Workflow Patterns Flow.
type WorkflowPatternFlows struct {
	SequenceFlow                                  *SequenceFlow
	ParallelSplitSynchronizationFlow              *ParallelSplitSynchronizationFlow
	ExclusiveChoiceSimpleMergeFlow                *ExclusiveChoiceSimpleMergeFlow
	MultiChoiceSynchronizingMergeFlow             *MultiChoiceSynchronizingMergeFlow
	MultiMergeFlow                                *MultiMergeFlow
	GeneralSynchronizingMergeFlow                 *GeneralSynchronizingMergeFlow
	StructuredPartialJoinFlow                     *StructuredPartialJoinFlow
	BlockingPartialJoinFlow                       *BlockingPartialJoinFlow
	CancellingPartialJoinFlow                     *CancellingPartialJoinFlow
	GeneralizedAndJoinFlow                        *GeneralizedAndJoinFlow
	ThreadSplitMergeFlow                          *ThreadSplitMergeFlow
	MultipleInstancesWithoutSynchronizationFlow   *MultipleInstancesWithoutSynchronizationFlow
	DesignTimeMultipleInstancesFlow               *DesignTimeMultipleInstancesFlow
	RunTimeMultipleInstancesFlow                  *RunTimeMultipleInstancesFlow
	NoPriorKnowledgeMultipleInstancesFlow         *NoPriorKnowledgeMultipleInstancesFlow
	StaticPartialJoinForMultipleInstancesFlow     *StaticPartialJoinForMultipleInstancesFlow
	CancellingPartialJoinForMultipleInstancesFlow *CancellingPartialJoinForMultipleInstancesFlow
	DynamicPartialJoinForMultipleInstancesFlow    *DynamicPartialJoinForMultipleInstancesFlow
	DeferredChoiceFlow                            *DeferredChoiceFlow
	InterleavedParallelRoutingFlow                *InterleavedParallelRoutingFlow
	MilestoneFlow                                 *MilestoneFlow
	CriticalSectionFlow                           *CriticalSectionFlow
	InterleavedRoutingFlow                        *InterleavedRoutingFlow
	CancelTaskFlow                                *CancelTaskFlow
	CancelCaseFlow                                *CancelCaseFlow
	BackgroundCheckFlow                           *BackgroundCheckFlow
	CancelRegionFlow                              *CancelRegionFlow
	CancelMultipleInstanceTaskFlow                *CancelMultipleInstanceTaskFlow
	CompleteMultipleInstanceTaskFlow              *CompleteMultipleInstanceTaskFlow
	ArbitraryCyclesFlow                           *ArbitraryCyclesFlow
	StructuredLoopFlow                            *StructuredLoopFlow
	FactorialRecursionFlow                        *FactorialRecursionFlow
	ImplicitTerminationFlow                       *ImplicitTerminationFlow
	ExplicitTerminationFlow                       *ExplicitTerminationFlow
	TransientTriggerFlow                          *TransientTriggerFlow
	PersistentTriggerFlow                         *PersistentTriggerFlow
}

// NewWorkflowPatternFlows constructs every Workflow Patterns Flow; getClient may return nil until bootstrap completes.
func NewWorkflowPatternFlows(getClient func() *dex.Client) *WorkflowPatternFlows {
	backgroundCheckFlow := &BackgroundCheckFlow{}
	return &WorkflowPatternFlows{
		SequenceFlow:                                  &SequenceFlow{},
		ParallelSplitSynchronizationFlow:              &ParallelSplitSynchronizationFlow{},
		ExclusiveChoiceSimpleMergeFlow:                &ExclusiveChoiceSimpleMergeFlow{},
		MultiChoiceSynchronizingMergeFlow:             &MultiChoiceSynchronizingMergeFlow{},
		MultiMergeFlow:                                &MultiMergeFlow{},
		GeneralSynchronizingMergeFlow:                 &GeneralSynchronizingMergeFlow{},
		StructuredPartialJoinFlow:                     &StructuredPartialJoinFlow{},
		BlockingPartialJoinFlow:                       &BlockingPartialJoinFlow{},
		CancellingPartialJoinFlow:                     &CancellingPartialJoinFlow{},
		GeneralizedAndJoinFlow:                        &GeneralizedAndJoinFlow{},
		ThreadSplitMergeFlow:                          &ThreadSplitMergeFlow{},
		MultipleInstancesWithoutSynchronizationFlow:   &MultipleInstancesWithoutSynchronizationFlow{},
		DesignTimeMultipleInstancesFlow:               &DesignTimeMultipleInstancesFlow{},
		RunTimeMultipleInstancesFlow:                  &RunTimeMultipleInstancesFlow{},
		NoPriorKnowledgeMultipleInstancesFlow:         &NoPriorKnowledgeMultipleInstancesFlow{},
		StaticPartialJoinForMultipleInstancesFlow:     &StaticPartialJoinForMultipleInstancesFlow{},
		CancellingPartialJoinForMultipleInstancesFlow: &CancellingPartialJoinForMultipleInstancesFlow{},
		DynamicPartialJoinForMultipleInstancesFlow:    &DynamicPartialJoinForMultipleInstancesFlow{},
		DeferredChoiceFlow:                            &DeferredChoiceFlow{},
		InterleavedParallelRoutingFlow:                &InterleavedParallelRoutingFlow{},
		MilestoneFlow:                                 &MilestoneFlow{},
		CriticalSectionFlow:                           &CriticalSectionFlow{},
		InterleavedRoutingFlow:                        &InterleavedRoutingFlow{},
		CancelTaskFlow:                                &CancelTaskFlow{},
		CancelCaseFlow:                                NewCancelCaseFlow(getClient, backgroundCheckFlow),
		BackgroundCheckFlow:                           backgroundCheckFlow,
		CancelRegionFlow:                              &CancelRegionFlow{},
		CancelMultipleInstanceTaskFlow:                &CancelMultipleInstanceTaskFlow{},
		CompleteMultipleInstanceTaskFlow:              &CompleteMultipleInstanceTaskFlow{},
		ArbitraryCyclesFlow:                           &ArbitraryCyclesFlow{},
		StructuredLoopFlow:                            &StructuredLoopFlow{},
		FactorialRecursionFlow:                        &FactorialRecursionFlow{},
		ImplicitTerminationFlow:                       &ImplicitTerminationFlow{},
		ExplicitTerminationFlow:                       &ExplicitTerminationFlow{},
		TransientTriggerFlow:                          &TransientTriggerFlow{},
		PersistentTriggerFlow:                         &PersistentTriggerFlow{},
	}
}

// AllFlows returns every Workflow Patterns Flow for Registry construction.
func (flows *WorkflowPatternFlows) AllFlows() []dex.Flow {
	return []dex.Flow{
		flows.SequenceFlow,
		flows.ParallelSplitSynchronizationFlow,
		flows.ExclusiveChoiceSimpleMergeFlow,
		flows.MultiChoiceSynchronizingMergeFlow,
		flows.MultiMergeFlow,
		flows.GeneralSynchronizingMergeFlow,
		flows.StructuredPartialJoinFlow,
		flows.BlockingPartialJoinFlow,
		flows.CancellingPartialJoinFlow,
		flows.GeneralizedAndJoinFlow,
		flows.ThreadSplitMergeFlow,
		flows.MultipleInstancesWithoutSynchronizationFlow,
		flows.DesignTimeMultipleInstancesFlow,
		flows.RunTimeMultipleInstancesFlow,
		flows.NoPriorKnowledgeMultipleInstancesFlow,
		flows.StaticPartialJoinForMultipleInstancesFlow,
		flows.CancellingPartialJoinForMultipleInstancesFlow,
		flows.DynamicPartialJoinForMultipleInstancesFlow,
		flows.DeferredChoiceFlow,
		flows.InterleavedParallelRoutingFlow,
		flows.MilestoneFlow,
		flows.CriticalSectionFlow,
		flows.InterleavedRoutingFlow,
		flows.CancelTaskFlow,
		flows.CancelCaseFlow,
		flows.BackgroundCheckFlow,
		flows.CancelRegionFlow,
		flows.CancelMultipleInstanceTaskFlow,
		flows.CompleteMultipleInstanceTaskFlow,
		flows.ArbitraryCyclesFlow,
		flows.StructuredLoopFlow,
		flows.FactorialRecursionFlow,
		flows.ImplicitTerminationFlow,
		flows.ExplicitTerminationFlow,
		flows.TransientTriggerFlow,
		flows.PersistentTriggerFlow,
	}
}

// TaskInterval records when one task ran, in Unix epoch milliseconds.
type TaskInterval struct {
	Task             string `json:"task"`
	StartedAtMillis  int64  `json:"startedAtMillis"`
	FinishedAtMillis int64  `json:"finishedAtMillis"`
}

// appendTrail returns a copy of trail with stepType appended.
func appendTrail(trail []string, stepType string) []string {
	extended := make([]string, 0, len(trail)+1)
	extended = append(extended, trail...)
	return append(extended, stepType)
}

// sortedCopy returns a sorted, never-nil copy of values.
func sortedCopy[T int | string](values []T) []T {
	sorted := make([]T, 0, len(values))
	sorted = append(sorted, values...)
	slices.Sort(sorted)
	return sorted
}

// getAttributeOrZero returns the Attribute value, or the zero value while it is unset.
func getAttributeOrZero[T any](ctx dex.Context, attribute dex.Attribute[T]) (T, error) {
	value, err := attribute.Get(ctx)
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		var zero T
		return zero, nil
	}
	return value, err
}

// openGate releases count instances held by a concurrency gate.
func openGate(ctx dex.Context, gate dex.Channel[string], count int) (*dex.RPCResult[dex.None], error) {
	if err := publishMessages(ctx, gate, "opened", count); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// publishMessages publishes value to channel count times.
func publishMessages(ctx dex.Context, channel dex.Channel[string], value string, count int) error {
	for range count {
		if err := channel.Publish(ctx, value); err != nil {
			return err
		}
	}
	return nil
}
