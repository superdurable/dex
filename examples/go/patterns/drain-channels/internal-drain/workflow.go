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

package draininternal

import (
	"fmt"

	patternsservice "github.com/superdurable/dex/examples/go/patterns/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	SideStepDataChannelName  = "SideStepData"
	MainStepExecutionCounter = "main_step_execution_counter"
)

var (
	MainStepExecutionCounterAttribute = dex.DefineAttribute[int](
		MainStepExecutionCounter,
	)
	SideStepData = dex.DefineChannel[MongoDocument](SideStepDataChannelName)
)

type DrainInternalChannelFlow struct {
	dex.FlowDefaults
	service patternsservice.ServiceDependency
}

func NewDrainInternalChannelFlow(
	service patternsservice.ServiceDependency,
) *DrainInternalChannelFlow {
	return &DrainInternalChannelFlow{service: service}
}

func (flow *DrainInternalChannelFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initStep{}),
		dex.DefineStep(sideStep{service: flow.service}),
		dex.DefineStep(mainStep{service: flow.service}),
		dex.DefineStep(finalizeStep{}),
	}
}

func (*DrainInternalChannelFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{MainStepExecutionCounterAttribute},
		Channels:   []dex.ChannelDef{SideStepData},
	}
}

type initStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (initStep) Execute(
	ctx dex.Context,
	input string,
) (*dex.StepDecision, error) {
	if err := MainStepExecutionCounterAttribute.Set(ctx, 0); err != nil {
		return nil, err
	}
	return dex.GoToMany(
		dex.MovementOf(sideStep{}, nil),
		dex.MovementOf(mainStep{}, input),
	), nil
}

type sideStep struct {
	dex.StepDefaults
	service patternsservice.ServiceDependency
}

func (sideStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	return dex.Until(SideStepData.ForOne()), nil
}

func (step sideStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	documents, err := SideStepData.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(documents) == 0 {
		return nil, fmt.Errorf("no document was sent")
	}
	document := documents[0]
	if err := step.service.Upsert(document); err != nil {
		return nil, err
	}
	if document.FinalCommand {
		return dex.GracefulComplete(nil), nil
	}
	return dex.GoTo(sideStep{}, nil), nil
}

type mainStep struct {
	dex.StepDefaultsNoWaitFor[string]
	service patternsservice.ServiceDependency
}

func (step mainStep) Execute(
	ctx dex.Context,
	input string,
) (*dex.StepDecision, error) {
	executionCount, err := MainStepExecutionCounterAttribute.Get(ctx)
	if err != nil {
		return nil, err
	}
	executionCount++
	if err := MainStepExecutionCounterAttribute.Set(ctx, executionCount); err != nil {
		return nil, err
	}
	status := "ERROR"
	switch executionCount {
	case 1:
		status = "RECEIVED"
	case 2:
		status = "ACCEPTED"
	case 3:
		status = "PASSED"
	}
	if err := SideStepData.Publish(ctx, MongoDocument{
		ID:           input,
		Status:       status,
		FinalCommand: false,
	}); err != nil {
		return nil, err
	}
	step.service.ExternalAPICall(
		"external service call to process data (e.g. notify the job seeker)",
	)
	step.service.ExternalAPICall("a call to send metrics or add a log to logrepo")
	if executionCount <= 3 {
		return dex.GoTo(mainStep{}, input), nil
	}
	return dex.GoTo(finalizeStep{}, nil), nil
}

type finalizeStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (finalizeStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	if err := SideStepData.Publish(ctx, MongoDocument{
		ID:           "documentId-1",
		Status:       "FINALIZED",
		FinalCommand: true,
	}); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

var _ dex.Flow = (*DrainInternalChannelFlow)(nil)
