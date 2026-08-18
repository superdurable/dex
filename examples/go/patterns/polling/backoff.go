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

package polling

import (
	"fmt"
	"time"

	patternsservice "github.com/superdurable/dex/examples/go/patterns/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

type BackoffPollingFlow struct {
	dex.FlowDefaults
	service patternsservice.ServiceDependency
}

func NewBackoffPollingFlow(service patternsservice.ServiceDependency) *BackoffPollingFlow {
	return &BackoffPollingFlow{service: service}
}

func (flow *BackoffPollingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(readExternalDepStep{service: flow.service}),
		dex.DefineStep(pollingCompleteStep{}),
	}
}

func (*BackoffPollingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type readExternalDepStep struct {
	dex.DefaultStepType
	dex.NoWaitFor[dex.None]
	service patternsservice.ServiceDependency
}

func (readExternalDepStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    3 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    60 * time.Second,
			MaximumAttempts:    5,
			TotalDuration:      3600 * time.Second,
		},
	}
}

func (step readExternalDepStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	result, err := step.service.AttemptExternalAPICall("Read for BackoffPollingFlow")
	if err != nil {
		return nil, err
	}
	return dex.GoTo(pollingCompleteStep{}, result), nil
}

type pollingCompleteStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (pollingCompleteStep) Execute(
	ctx dex.Context,
	externalDataInput string,
) (*dex.StepDecision, error) {
	fmt.Printf(
		"Executing final state to complete the workflow: (%s)\n",
		externalDataInput,
	)
	return dex.GracefulComplete(externalDataInput), nil
}

var _ dex.Flow = (*BackoffPollingFlow)(nil)
