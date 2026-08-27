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

package intervention

import (
	"fmt"

	patternsservice "github.com/superdurable/dex/examples/go/patterns/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	InternalChannelCommand       = "internal_channel_command"
	ChannelCommandRetry          = "channel_command_retry"
	ChannelCommandSkip           = "channel_command_skip"
	NumberOfRetriesAttributeName = "number_of_retries"
)

var (
	DataChannel     = dex.DefineChannel[string](InternalChannelCommand)
	RetryChannel    = dex.DefineChannel[dex.None](ChannelCommandRetry)
	SkipChannel     = dex.DefineChannel[dex.None](ChannelCommandSkip)
	NumberOfRetries = dex.DefineAttribute[int](NumberOfRetriesAttributeName)
)

type ManualInterventionFlow struct {
	dex.FlowDefaults
	service patternsservice.ServiceDependency
}

func NewManualInterventionFlow(service patternsservice.ServiceDependency) *ManualInterventionFlow {
	return &ManualInterventionFlow{service: service}
}

func (*ManualInterventionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(initStep{}),
		dex.DefineStep(getDataStep{}),
		dex.DefineStep(errorStep{}),
		dex.DefineStep(finalStep{}),
	}
}

func (*ManualInterventionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{NumberOfRetries},
		Channels:   []dex.ChannelDef{DataChannel, RetryChannel, SkipChannel},
	}
}

type initStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (initStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	if err := NumberOfRetries.Set(ctx, 0); err != nil {
		return nil, err
	}
	return dex.GoTo(getDataStep{}, false), nil
}

type getDataStep struct {
	dex.StepDefaults
}

func (getDataStep) WaitFor(
	ctx dex.Context,
	isRetry bool,
) (*dex.Wait, error) {
	fmt.Println("Waiting for incoming data")
	return dex.Until(DataChannel.ForOne()), nil
}

func (getDataStep) Execute(
	ctx dex.Context,
	isRetry bool,
) (*dex.StepDecision, error) {
	if isRetry {
		retries, err := NumberOfRetries.Get(ctx)
		if err != nil {
			return nil, err
		}
		if err := NumberOfRetries.Set(ctx, retries+1); err != nil {
			return nil, err
		}
	}
	if err := pretendAPICall(ctx); err != nil {
		return dex.GoTo(errorStep{}, nil), nil
	}
	return dex.GoTo(finalStep{}, nil), nil
}

func pretendAPICall(ctx dex.Context) error {
	results, err := DataChannel.GetConditionResults(ctx)
	if err != nil {
		return err
	}
	if len(results) > 0 {
		data := results[0]
		fmt.Println("Received data result: " + data)
		if data == "failed" {
			return fmt.Errorf("non-retryable exception")
		}
	}
	return nil
}

type errorStep struct {
	dex.StepDefaults
}

func (errorStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	return dex.AnyOf(RetryChannel.ForOne(), SkipChannel.ForOne()), nil
}

func (errorStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	retryResults, err := RetryChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	retry := len(retryResults) > 0
	channelName := ChannelCommandSkip
	if retry {
		channelName = ChannelCommandRetry
	}
	fmt.Println("channel message received: " + channelName)
	if retry {
		return dex.GoTo(getDataStep{}, true), nil
	}
	return dex.GoTo(finalStep{}, nil), nil
}

type finalStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (finalStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	retries, err := NumberOfRetries.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(
		fmt.Sprintf("Workflow Completed. Number of retries: %d", retries),
	), nil
}

var _ dex.Flow = (*ManualInterventionFlow)(nil)
