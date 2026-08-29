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

package inactivenesstrackertimer

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const ActiveChannelName = "Active"

var (
	TrackerDuration = 5 * time.Minute
	ActiveChannel   = dex.DefineChannel[dex.None](ActiveChannelName)
)

type InactivenessTrackerFlow struct {
	dex.FlowDefaults
}

func NewInactivenessTrackerFlow() *InactivenessTrackerFlow {
	return &InactivenessTrackerFlow{}
}

func (*InactivenessTrackerFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(trackerStep{}),
		dex.DefineStep(processInactivenessStep{}),
	}
}

func (*InactivenessTrackerFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{ActiveChannel}}
}

func (*InactivenessTrackerFlow) RecordActivity(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := ActiveChannel.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type trackerStep struct {
	dex.StepDefaults
}

func (trackerStep) WaitFor(
	ctx dex.Context,
	_ dex.None,
) (*dex.Wait, error) {
	return dex.AnyOf(
		dex.Timer(TrackerDuration),
		ActiveChannel.ForOne(),
	), nil
}

func (trackerStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.GoTo(processInactivenessStep{}, nil), nil
	}
	return dex.GoTo(trackerStep{}, nil), nil
}

type processInactivenessStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (processInactivenessStep) Execute(
	ctx dex.Context,
	_ dex.None,
) (*dex.StepDecision, error) {
	fmt.Println("No activity arrived before the timer fired")
	return dex.GracefulComplete(nil), nil
}

var (
	_ dex.Flow                    = (*InactivenessTrackerFlow)(nil)
	_ dex.RPC[dex.None, dex.None] = (*InactivenessTrackerFlow)(nil).RecordActivity
)
