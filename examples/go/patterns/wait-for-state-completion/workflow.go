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

package waitforstatecompletion

import (
	"encoding/json"

	patternsservice "github.com/superdurable/dex/examples/go/workflows/patterns/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

const JobSeekerDataAttributeName = "job_seeker_data"

var JobSeekerDataAttribute = dex.DefineAttribute[JobSeekerData](JobSeekerDataAttributeName)

type JobSeekerData struct {
	ID     int
	Name   string
	Resume string
	Email  string
}

type WaitForStateCompletionFlow struct {
	dex.FlowDefaults
	service patternsservice.ServiceDependency
}

func NewWaitForStateCompletionFlow(
	service patternsservice.ServiceDependency,
) *WaitForStateCompletionFlow {
	return &WaitForStateCompletionFlow{service: service}
}

func (flow *WaitForStateCompletionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(persistDataStep{service: flow.service}),
		dex.DefineStep(updateExternalSystemStep{service: flow.service}),
	}
}

func (*WaitForStateCompletionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{JobSeekerDataAttribute},
	}
}

func (*WaitForStateCompletionFlow) GetJobSeekerData(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[JobSeekerData], error) {
	data, err := JobSeekerDataAttribute.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[JobSeekerData]{Output: data}, nil
}

type persistDataStep struct {
	dex.StepDefaultsNoWaitFor[JobSeekerData]
	service patternsservice.ServiceDependency
}

func (persistDataStep) GetStepType() string {
	return "PersistData"
}

func (step persistDataStep) Execute(
	ctx dex.Context,
	input JobSeekerData,
) (*dex.StepDecision, error) {
	if err := step.service.Upsert(input); err != nil {
		return nil, err
	}
	if err := JobSeekerDataAttribute.Set(ctx, input); err != nil {
		return nil, err
	}
	return dex.GoTo(updateExternalSystemStep{}, input), nil
}

type updateExternalSystemStep struct {
	dex.StepDefaultsNoWaitFor[JobSeekerData]
	service patternsservice.ServiceDependency
}

func (step updateExternalSystemStep) Execute(
	_ dex.Context,
	input JobSeekerData,
) (*dex.StepDecision, error) {
	serialized, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	step.service.UpdateExternalSystem(string(serialized))
	return dex.GracefulComplete(nil), nil
}

var (
	_ dex.Flow                         = (*WaitForStateCompletionFlow)(nil)
	_ dex.RPC[dex.None, JobSeekerData] = (*WaitForStateCompletionFlow)(nil).GetJobSeekerData
)
