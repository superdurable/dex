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
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	RetryChannelName = "manual-recovery-retry"
	SkipChannelName  = "manual-recovery-skip"
)

var (
	RetryChannel = dex.DefineChannel[dex.None](RetryChannelName)
	SkipChannel  = dex.DefineChannel[dex.None](SkipChannelName)
)

type ManualRecoveryFlow struct {
	dex.FlowDefaults
}

func NewManualRecoveryFlow() *ManualRecoveryFlow {
	return &ManualRecoveryFlow{}
}

func (*ManualRecoveryFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(doWorkStep{}),
		dex.DefineStep(manualStep{}),
	}
}

func (*ManualRecoveryFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{RetryChannel, SkipChannel},
	}
}

type doWorkStep struct {
	dex.StepDefaultsNoWaitFor[bool]
}

func (doWorkStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    4 * time.Second,
			MaximumAttempts:    4,
		},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(manualStep{}, nil),
	}
}

func (doWorkStep) Execute(
	_ dex.Context,
	shouldFail bool,
) (*dex.StepDecision, error) {
	if shouldFail {
		return nil, fmt.Errorf("work failed")
	}
	return dex.GracefulComplete("work completed"), nil
}

type manualStep struct {
	dex.StepDefaults
}

func (manualStep) WaitFor(
	_ dex.Context,
	_ bool,
) (*dex.Wait, error) {
	return dex.AnyOf(RetryChannel.ForOne(), SkipChannel.ForOne()), nil
}

func (manualStep) Execute(
	ctx dex.Context,
	_ bool,
) (*dex.StepDecision, error) {
	retryResults, err := RetryChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(retryResults) > 0 {
		return dex.GoTo(doWorkStep{}, false), nil
	}
	return dex.ForceFail("manual recovery skipped"), nil
}

var _ dex.Flow = (*ManualRecoveryFlow)(nil)
