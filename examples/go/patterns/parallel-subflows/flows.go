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
	"hash/fnv"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	DefaultConcurrency  = 10
	MaxBufferedRequests = 100
)

var (
	RequestChannel     = dex.DefineChannel[string]("RequestChannel")
	Stopped            = dex.DefineAttribute[bool]("Stopped")
	CurrSubFlowNum     = dex.DefineAttribute[int]("CurrSubFlowNum")
	SubFlowCompletedCh = dex.DefineChannel[bool]("SubFlowCompletedCh")
	AllDoneCh          = dex.DefineChannel[bool]("AllDoneCh")
)

type ParentInput struct {
	Requests    []string `json:"requests"`
	Concurrency int      `json:"concurrency"`
}

type SubmitRequestInput struct {
	Request   string   `json:"request"`
	ParentIDs []string `json:"parentIds"`
}

type ExampleSubFlow struct{ dex.FlowDefaults }

func NewExampleSubFlow() *ExampleSubFlow { return &ExampleSubFlow{} }

func (*ExampleSubFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(doWorkStep{})}
}

func (*ExampleSubFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type doWorkStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (doWorkStep) GetStepType() string { return "DoWorkStep" }

func (doWorkStep) Execute(_ dex.Context, request string) (*dex.StepDecision, error) {
	time.Sleep(time.Duration(50+len(request)%10*50) * time.Millisecond)
	return dex.GracefulComplete(request), nil
}

type BasicParentFlow struct {
	dex.FlowDefaults
	exampleFlow *ExampleSubFlow
}

func NewBasicParentFlow(exampleFlow *ExampleSubFlow) *BasicParentFlow {
	return &BasicParentFlow{exampleFlow: exampleFlow}
}

func (flow *BasicParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(subFlowsStep{exampleFlow: flow.exampleFlow})}
}

func (*BasicParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type subFlowsStep struct {
	dex.StepDefaults
	exampleFlow *ExampleSubFlow
}

func (subFlowsStep) GetStepType() string { return "SubFlowsStep" }

func (step subFlowsStep) WaitFor(_ dex.Context, requests []string) (*dex.Wait, error) {
	conditions := make([]dex.Condition, 0, len(requests))
	for _, request := range requests {
		conditions = append(conditions, dex.SubFlow(step.exampleFlow, request))
	}
	return dex.AllOf(conditions...), nil
}

func (subFlowsStep) Execute(_ dex.Context, _ []string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

type WaitForHalfParentFlow struct {
	dex.FlowDefaults
	getClient   func() *dex.Client
	exampleFlow *ExampleSubFlow
}

func NewWaitForHalfParentFlow(
	getClient func() *dex.Client,
	exampleFlow *ExampleSubFlow,
) *WaitForHalfParentFlow {
	return &WaitForHalfParentFlow{getClient: getClient, exampleFlow: exampleFlow}
}

func (flow *WaitForHalfParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(waitForHalfInitStep{}),
		dex.DefineStep(subFlowStep{getClient: flow.getClient, exampleFlow: flow.exampleFlow}),
		dex.DefineStep(waitSubFlowsStep{}),
	}
}

func (*WaitForHalfParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{SubFlowCompletedCh, AllDoneCh}}
}

type waitForHalfInitStep struct {
	dex.StepDefaultsNoWaitFor[[]string]
}

func (waitForHalfInitStep) GetStepType() string { return "InitStep" }

func (waitForHalfInitStep) Execute(_ dex.Context, requests []string) (*dex.StepDecision, error) {
	if len(requests) == 0 {
		return dex.GracefulComplete(nil), nil
	}
	movements := make([]dex.StepMovement, 0, len(requests)+1)
	movements = append(movements, dex.MovementOf(waitSubFlowsStep{}, len(requests)))
	for _, request := range requests {
		movements = append(movements, dex.MovementOf(subFlowStep{}, request))
	}
	return dex.GoToMany(movements...), nil
}

type subFlowStep struct {
	dex.StepDefaults
	getClient   func() *dex.Client
	exampleFlow *ExampleSubFlow
}

func (subFlowStep) GetStepType() string { return "SubFlowStep" }

func (step subFlowStep) WaitFor(_ dex.Context, request string) (*dex.Wait, error) {
	return dex.AnyOf(dex.SubFlow(step.exampleFlow, request), AllDoneCh.ForOne()), nil
}

func (step subFlowStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	result, err := dex.SubFlowResult(ctx)
	if err != nil {
		return nil, err
	}
	if result.Status != dex.FlowRunning {
		if err := SubFlowCompletedCh.Publish(ctx, true); err != nil {
			return nil, err
		}
		return dex.GracefulComplete(nil), nil
	}
	client := step.getClient()
	if client == nil {
		return nil, fmt.Errorf("dex client is not available")
	}
	flowID, err := dex.SubFlowID(ctx)
	if err != nil {
		return nil, err
	}
	if err := client.StopFlow(context.Background(), flowID, dex.StopOptions{
		Type: dex.CancelFlow, Reason: "enough SubFlows completed",
	}); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type waitSubFlowsStep struct{ dex.StepDefaults }

func (waitSubFlowsStep) GetStepType() string { return "WaitSubFlowsStep" }

func (waitSubFlowsStep) WaitFor(_ dex.Context, total int) (*dex.Wait, error) {
	return dex.Until(SubFlowCompletedCh.ForN((total + 1) / 2)), nil
}

func (waitSubFlowsStep) Execute(ctx dex.Context, total int) (*dex.StepDecision, error) {
	remaining := total - (total+1)/2
	for index := 0; index < remaining; index++ {
		if err := AllDoneCh.Publish(ctx, true); err != nil {
			return nil, err
		}
	}
	return dex.GracefulComplete(nil), nil
}

type AdvancedLongLiveParentFlow struct {
	dex.FlowDefaults
	exampleFlow *ExampleSubFlow
}

func NewAdvancedLongLiveParentFlow(exampleFlow *ExampleSubFlow) *AdvancedLongLiveParentFlow {
	return &AdvancedLongLiveParentFlow{exampleFlow: exampleFlow}
}

func (flow *AdvancedLongLiveParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(longLiveInitStep{}),
		dex.DefineStep(longLiveHandleRequestStep{}),
		dex.DefineStep(longLiveHandleSubFlowStep{exampleFlow: flow.exampleFlow}),
	}
}

func (*AdvancedLongLiveParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{Stopped}, Channels: []dex.ChannelDef{RequestChannel}}
}

func (*AdvancedLongLiveParentFlow) SendRequest(ctx dex.Context, request string) (*dex.RPCResult[bool], error) {
	if RequestChannel.Size(ctx) >= MaxBufferedRequests {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if err := RequestChannel.Publish(ctx, request); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

func (*AdvancedLongLiveParentFlow) Stop(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := Stopped.Set(ctx, true); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type longLiveInitStep struct {
	dex.StepDefaultsNoWaitFor[ParentInput]
}

func (longLiveInitStep) GetStepType() string { return "InitStep" }

func (longLiveInitStep) Execute(ctx dex.Context, input ParentInput) (*dex.StepDecision, error) {
	for _, request := range input.Requests {
		if err := RequestChannel.Publish(ctx, request); err != nil {
			return nil, err
		}
	}
	if err := Stopped.Set(ctx, false); err != nil {
		return nil, err
	}
	return dex.GoToMany(longLiveWorkerMovements(concurrency(input.Concurrency))...), nil
}

type longLiveHandleRequestStep struct{ dex.StepDefaults }

func (longLiveHandleRequestStep) GetStepType() string { return "HandleRequestStep" }

func (longLiveHandleRequestStep) WaitFor(_ dex.Context, _ dex.None) (*dex.Wait, error) {
	return dex.Until(RequestChannel.ForOne()), nil
}

func (longLiveHandleRequestStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	requests, err := RequestChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(longLiveHandleSubFlowStep{}, requests[0]), nil
}

type longLiveHandleSubFlowStep struct {
	dex.StepDefaults
	exampleFlow *ExampleSubFlow
}

func (longLiveHandleSubFlowStep) GetStepType() string { return "HandleSubFlowStep" }

func (step longLiveHandleSubFlowStep) WaitFor(_ dex.Context, request string) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.exampleFlow, request)), nil
}

func (longLiveHandleSubFlowStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	stopped, err := Stopped.Get(ctx)
	if err != nil {
		return nil, err
	}
	if stopped {
		return dex.GracefulComplete(nil), nil
	}
	return dex.GoTo(longLiveHandleRequestStep{}, nil), nil
}

type AdvancedShortLiveParentFlow struct {
	dex.FlowDefaults
	exampleFlow *ExampleSubFlow
}

func NewAdvancedShortLiveParentFlow(exampleFlow *ExampleSubFlow) *AdvancedShortLiveParentFlow {
	return &AdvancedShortLiveParentFlow{exampleFlow: exampleFlow}
}

func (flow *AdvancedShortLiveParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(shortLiveInitStep{}),
		dex.DefineStep(shortLiveHandleRequestStep{}),
		dex.DefineStep(shortLiveHandleSubFlowStep{exampleFlow: flow.exampleFlow}),
	}
}

func (*AdvancedShortLiveParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{CurrSubFlowNum}, Channels: []dex.ChannelDef{RequestChannel}}
}

func (*AdvancedShortLiveParentFlow) SendRequest(ctx dex.Context, request string) (*dex.RPCResult[bool], error) {
	if RequestChannel.Size(ctx) >= MaxBufferedRequests {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if err := RequestChannel.Publish(ctx, request); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

type shortLiveInitStep struct {
	dex.StepDefaultsNoWaitFor[ParentInput]
}

func (shortLiveInitStep) GetStepType() string { return "InitStep" }

func (shortLiveInitStep) Execute(ctx dex.Context, input ParentInput) (*dex.StepDecision, error) {
	for _, request := range input.Requests {
		if err := RequestChannel.Publish(ctx, request); err != nil {
			return nil, err
		}
	}
	if err := CurrSubFlowNum.Set(ctx, 0); err != nil {
		return nil, err
	}
	return dex.GoToMany(shortLiveWorkerMovements(concurrency(input.Concurrency))...), nil
}

type shortLiveHandleRequestStep struct{ dex.StepDefaults }

func (shortLiveHandleRequestStep) GetStepType() string { return "HandleRequestStep" }

func (shortLiveHandleRequestStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(CurrSubFlowNum)}}
}

func (shortLiveHandleRequestStep) WaitFor(_ dex.Context, _ dex.None) (*dex.Wait, error) {
	return dex.Until(RequestChannel.ForOne()), nil
}

func (shortLiveHandleRequestStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	requests, err := RequestChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	current, err := CurrSubFlowNum.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := CurrSubFlowNum.Set(ctx, current+1); err != nil {
		return nil, err
	}
	return dex.GoTo(shortLiveHandleSubFlowStep{}, requests[0]), nil
}

type shortLiveHandleSubFlowStep struct {
	dex.StepDefaults
	exampleFlow *ExampleSubFlow
}

func (shortLiveHandleSubFlowStep) GetStepType() string { return "HandleSubFlowStep" }

func (shortLiveHandleSubFlowStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(CurrSubFlowNum)}}
}

func (step shortLiveHandleSubFlowStep) WaitFor(_ dex.Context, request string) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.exampleFlow, request)), nil
}

func (shortLiveHandleSubFlowStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	current, err := CurrSubFlowNum.Get(ctx)
	if err != nil {
		return nil, err
	}
	current--
	if err := CurrSubFlowNum.Set(ctx, current); err != nil {
		return nil, err
	}
	if current == 0 {
		return dex.ForceCompleteIfChannelsEmpty(
			nil,
			[]dex.ChannelDef{RequestChannel},
			dex.MovementOf(shortLiveHandleRequestStep{}, nil),
		), nil
	}
	return dex.GoTo(shortLiveHandleRequestStep{}, nil), nil
}

func longLiveWorkerMovements(count int) []dex.StepMovement {
	movements := make([]dex.StepMovement, 0, count)
	for index := 0; index < count; index++ {
		movements = append(movements, dex.MovementOf(longLiveHandleRequestStep{}, nil))
	}
	return movements
}

func shortLiveWorkerMovements(count int) []dex.StepMovement {
	movements := make([]dex.StepMovement, 0, count)
	for index := 0; index < count; index++ {
		movements = append(movements, dex.MovementOf(shortLiveHandleRequestStep{}, nil))
	}
	return movements
}

func concurrency(configured int) int {
	if configured > 0 {
		return configured
	}
	return DefaultConcurrency
}

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
	if err == nil {
		return true, nil
	}
	var alreadyStarted *dex.FlowAlreadyStartedError
	if !errors.As(err, &alreadyStarted) {
		return false, err
	}
	return invokeRequest(ctx, client, parentFlow, parentID, request)
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
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(request))
	return int(hash.Sum32()) % partitions
}

var (
	_ dex.Flow                    = (*ExampleSubFlow)(nil)
	_ dex.Flow                    = (*BasicParentFlow)(nil)
	_ dex.Flow                    = (*WaitForHalfParentFlow)(nil)
	_ dex.Flow                    = (*AdvancedLongLiveParentFlow)(nil)
	_ dex.Flow                    = (*AdvancedShortLiveParentFlow)(nil)
	_ dex.Flow                    = (*SubmitRequestFlow)(nil)
	_ dex.RPC[string, bool]       = (*AdvancedLongLiveParentFlow)(nil).SendRequest
	_ dex.RPC[dex.None, dex.None] = (*AdvancedLongLiveParentFlow)(nil).Stop
	_ dex.RPC[string, bool]       = (*AdvancedShortLiveParentFlow)(nil).SendRequest
)
