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

package resettabletimer

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const ResetTimerChannelName = "RESET_TIMER_CHANNEL"

var (
	TimerDuration     = 5 * time.Minute
	ResetTimerChannel = dex.DefineChannel[string](ResetTimerChannelName)
)

type ResettableTimerFlow struct {
	dex.FlowDefaults
}

func NewResettableTimerFlow() *ResettableTimerFlow {
	return &ResettableTimerFlow{}
}

func (*ResettableTimerFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(resettableTimerStep{}),
		dex.DefineStep(timerExpiredStep{}),
	}
}

func (*ResettableTimerFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{ResetTimerChannel},
	}
}

func (*ResettableTimerFlow) SendResetMessage(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := ResetTimerChannel.Publish(ctx, "reset"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type resettableTimerStep struct {
	dex.StepDefaults
}

func (resettableTimerStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	return dex.AnyOf(
		dex.Timer(TimerDuration),
		ResetTimerChannel.ForOne(),
	), nil
}

func (resettableTimerStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.GoTo(timerExpiredStep{}, nil), nil
	}
	return dex.GoTo(resettableTimerStep{}, nil), nil
}

type timerExpiredStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (timerExpiredStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	fmt.Println("Timer fired; this is where we would send an email")
	return dex.GracefulComplete(nil), nil
}

var (
	_ dex.Flow                    = (*ResettableTimerFlow)(nil)
	_ dex.RPC[dex.None, dex.None] = (*ResettableTimerFlow)(nil).SendResetMessage
)
