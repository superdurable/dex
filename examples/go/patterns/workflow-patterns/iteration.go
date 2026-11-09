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

package workflowpatterns

import (
	"github.com/superdurable/dex/sdk-go/dex"
)

// SubmissionRoute tells ArbitraryCyclesFlow whether a draft already exists.
type SubmissionRoute struct {
	HasDraft bool `json:"hasDraft"`
}

// ArticleRevision carries the edit round and the Steps run so far.
type ArticleRevision struct {
	Revision int      `json:"revision"`
	Trail    []string `json:"trail"`
}

// ArbitraryCyclesFlow implements WCP-10 Arbitrary Cycles: the draft and edit cycle
// has two entry points, chosen by RouteSubmission.
type ArbitraryCyclesFlow struct{ dex.FlowDefaults }

func (*ArbitraryCyclesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(RouteSubmission{}),
		dex.DefineStep(DraftArticle{}),
		dex.DefineStep(EditArticle{}),
		dex.DefineStep(PublishArticle{}),
	}
}

func (*ArbitraryCyclesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type RouteSubmission struct {
	dex.StepDefaultsNoWaitFor[SubmissionRoute]
}

func (RouteSubmission) Execute(_ dex.Context, route SubmissionRoute) (*dex.StepDecision, error) {
	revision := ArticleRevision{Revision: 0, Trail: []string{"RouteSubmission"}}
	if route.HasDraft {
		return dex.GoTo(EditArticle{}, revision), nil
	}
	return dex.GoTo(DraftArticle{}, revision), nil
}

type DraftArticle struct {
	dex.StepDefaultsNoWaitFor[ArticleRevision]
}

func (DraftArticle) Execute(_ dex.Context, revision ArticleRevision) (*dex.StepDecision, error) {
	return dex.GoTo(EditArticle{}, ArticleRevision{
		Revision: revision.Revision,
		Trail:    appendTrail(revision.Trail, "DraftArticle"),
	}), nil
}

type EditArticle struct {
	dex.StepDefaultsNoWaitFor[ArticleRevision]
}

func (EditArticle) Execute(_ dex.Context, revision ArticleRevision) (*dex.StepDecision, error) {
	trail := appendTrail(revision.Trail, "EditArticle")
	if revision.Revision >= 1 {
		return dex.GoTo(PublishArticle{}, trail), nil
	}
	return dex.GoTo(DraftArticle{}, ArticleRevision{Revision: revision.Revision + 1, Trail: trail}), nil
}

type PublishArticle struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (PublishArticle) Execute(_ dex.Context, trail []string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(appendTrail(trail, "PublishArticle")), nil
}

// BatchProgress counts the batches left and processed.
type BatchProgress struct {
	RemainingBatches int `json:"remainingBatches"`
	ProcessedBatches int `json:"processedBatches"`
}

// StructuredLoopFlow implements WCP-21 Structured Loop: ProcessBatches tests its
// condition first, then moves to itself or exits.
type StructuredLoopFlow struct{ dex.FlowDefaults }

func (*StructuredLoopFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(ProcessBatches{}),
		dex.DefineStep(FinishBatches{}),
	}
}

func (*StructuredLoopFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type ProcessBatches struct {
	dex.StepDefaultsNoWaitFor[BatchProgress]
}

func (ProcessBatches) Execute(_ dex.Context, progress BatchProgress) (*dex.StepDecision, error) {
	if progress.RemainingBatches == 0 {
		return dex.GoTo(FinishBatches{}, progress.ProcessedBatches), nil
	}
	return dex.GoTo(ProcessBatches{}, BatchProgress{
		RemainingBatches: progress.RemainingBatches - 1,
		ProcessedBatches: progress.ProcessedBatches + 1,
	}), nil
}

type FinishBatches struct{ dex.StepDefaultsNoWaitFor[int] }

func (FinishBatches) Execute(_ dex.Context, processedBatches int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(processedBatches), nil
}

// FactorialRecursionFlow implements WCP-22 Recursion: ComputeFactorial waits for a
// SubFlow of its own Flow type.
type FactorialRecursionFlow struct{ dex.FlowDefaults }

func (flow *FactorialRecursionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(ComputeFactorial{factorialRecursionFlow: flow})}
}

func (*FactorialRecursionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type ComputeFactorial struct {
	dex.StepDefaults
	factorialRecursionFlow *FactorialRecursionFlow
}

func (step ComputeFactorial) WaitFor(_ dex.Context, n int) (*dex.Wait, error) {
	if n <= 1 {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(dex.SubFlow(step.factorialRecursionFlow, n-1)), nil
}

func (ComputeFactorial) Execute(ctx dex.Context, n int) (*dex.StepDecision, error) {
	if n <= 1 {
		return dex.GracefulComplete(1), nil
	}
	result, err := dex.SubFlowResult(ctx)
	if err != nil {
		return nil, err
	}
	var smallerFactorial int
	if err := result.DecodeSingleOutput(&smallerFactorial); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(n * smallerFactorial), nil
}

var (
	_ dex.Flow = (*ArbitraryCyclesFlow)(nil)
	_ dex.Flow = (*StructuredLoopFlow)(nil)
	_ dex.Flow = (*FactorialRecursionFlow)(nil)
)
