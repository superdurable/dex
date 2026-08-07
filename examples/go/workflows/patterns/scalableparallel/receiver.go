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

package scalableparallel

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/superdurable/dex/sdk-go/dex"
)

type RequestReceiverFlow struct {
	dex.FlowDefaults
	getClient  func() *dex.Client
	parentFlow *ParentFlow
}

func NewRequestReceiverFlow(
	getClient func() *dex.Client,
	parentFlow *ParentFlow,
) *RequestReceiverFlow {
	return &RequestReceiverFlow{getClient: getClient, parentFlow: parentFlow}
}

func (flow *RequestReceiverFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(requestStep{
			getClient:  flow.getClient,
			parentFlow: flow.parentFlow,
		}),
	}
}

func (*RequestReceiverFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type requestStep struct {
	dex.StepDefaultsNoWaitFor[int]
	getClient  func() *dex.Client
	parentFlow *ParentFlow
}

func (step requestStep) Execute(
	_ dex.Context,
	numberOfChildWfs int,
) (*dex.StepDecision, error) {
	batch := generateTasks(numberOfChildWfs)
	randSuffix := rand.Intn(NumParentWorkflows) + 1
	parentWorkflowID := fmt.Sprintf("parent_workflow_%d", randSuffix)

	client := step.getClient()
	if client == nil {
		return nil, fmt.Errorf("dex client is not available")
	}
	var success bool
	err := client.InvokeRPC(
		context.Background(),
		parentWorkflowID,
		step.parentFlow.Enqueue,
		batch,
		&success,
		dex.InvokeOptions{},
	)
	if err != nil {
		var dexErr *dex.Error
		if errors.As(err, &dexErr) && dexErr.SubStatus == dex.ErrorFlowNotFound {
			flowTimeout := time.Hour
			_, startErr := client.StartFlow(
				context.Background(),
				step.parentFlow,
				parentWorkflowID,
				batch,
				dex.StartFlowOptions{Timeout: &flowTimeout},
			)
			if startErr != nil {
				return nil, startErr
			}
		} else {
			return nil, err
		}
	} else if !success {
		return nil, newEnqueueFailedError("Enqueue failed, retry in next attempt")
	}
	return dex.GracefulComplete(nil), nil
}

func generateTasks(numberOfChildWfs int) BatchEnqueueRequest {
	uuids := make([]string, 0, numberOfChildWfs)
	for index := 0; index < numberOfChildWfs; index++ {
		uuids = append(uuids, uuid.NewString())
	}
	return BatchEnqueueRequest{List: uuids}
}

var _ dex.Flow = (*RequestReceiverFlow)(nil)
