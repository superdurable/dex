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

package parallelsubflows

import (
	"context"
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

type SubmitRequestFlow struct {
	dex.FlowDefaults
	getClient  func() *dex.Client
	parentFlow *AdvancedShortLiveParentFlow
}

func NewSubmitRequestFlow(
	getClient func() *dex.Client,
	parentFlow *AdvancedShortLiveParentFlow,
) *SubmitRequestFlow {
	return &SubmitRequestFlow{getClient: getClient, parentFlow: parentFlow}
}

func (flow *SubmitRequestFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(submitStep{
		getClient: flow.getClient, parentFlow: flow.parentFlow,
	})}
}

func (*SubmitRequestFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type submitStep struct {
	dex.StepDefaultsNoWaitFor[SubmitRequestInput]
	getClient  func() *dex.Client
	parentFlow *AdvancedShortLiveParentFlow
}

func (submitStep) GetStepType() string { return "SubmitStep" }

func (step submitStep) Execute(_ dex.Context, input SubmitRequestInput) (*dex.StepDecision, error) {
	if len(input.ParentIDs) == 0 {
		return nil, fmt.Errorf("at least one parent Flow ID is required")
	}
	client := step.getClient()
	if client == nil {
		return nil, fmt.Errorf("dex client is not available")
	}
	parentID := input.ParentIDs[partition(input.Request, len(input.ParentIDs))]
	accepted, err := enqueueRequest(
		context.Background(), client, step.parentFlow, parentID, input.Request,
	)
	if err != nil {
		return nil, err
	}
	if !accepted {
		return nil, fmt.Errorf("parent %s rejected the request", parentID)
	}
	return dex.GracefulComplete(parentID), nil
}

func enqueueRequest(
	ctx context.Context,
	client *dex.Client,
	parentFlow *AdvancedShortLiveParentFlow,
	parentID string,
	request string,
) (bool, error) {
	accepted, err := invokeRequest(ctx, client, parentFlow, parentID, request)
	if err == nil {
		return accepted, nil
	}
	var inactive *dex.FlowNotActiveError
	if !errors.As(err, &inactive) {
		return false, err
	}
	_, err = client.StartFlow(ctx, parentFlow, parentID, ParentInput{
		Requests: []string{request}, Concurrency: DefaultConcurrency,
	}, dex.StartFlowOptions{IDReusePolicy: dex.IDReuseAllowIfNotRunning})
	if err != nil {
		return false, err
	}
	return true, nil
}

func invokeRequest(
	ctx context.Context,
	client *dex.Client,
	parentFlow *AdvancedShortLiveParentFlow,
	parentID string,
	request string,
) (bool, error) {
	var accepted bool
	err := client.InvokeRPC(
		ctx, parentID, parentFlow.SendRequest, request, &accepted, dex.InvokeOptions{},
	)
	return accepted, err
}

func partition(request string, partitions int) int {
	hash := uint32(2_166_136_261)
	for _, value := range []byte(request) {
		hash ^= uint32(value)
		hash *= 16_777_619
	}
	return int(hash) % partitions
}

var _ dex.Flow = (*SubmitRequestFlow)(nil)
