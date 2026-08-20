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

package waittypes

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var SignalA = dex.DefineChannel[string]("SignalA")
var SignalB = dex.DefineChannel[string]("SignalB")

type WaitTypesFlow struct {
	dex.FlowDefaults
}

func NewWaitTypesFlow() *WaitTypesFlow {
	return &WaitTypesFlow{}
}

func (*WaitTypesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(waitTypesStep{})}
}

func (*WaitTypesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{SignalA, SignalB}}
}

type WaitTypesInput struct {
	Mode           string
	TimeoutSeconds int
}

type waitTypesStep struct {
	dex.StepDefaults
}

func (waitTypesStep) WaitFor(_ dex.Context, input WaitTypesInput) (*dex.Wait, error) {
	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	switch input.Mode {
	case "any":
		return dex.AnyOf(
			SignalA.ForOne(dex.WithConditionID("signal")),
			dex.Timer(timeout, dex.WithConditionID("timeout")),
		), nil
	case "all":
		return dex.AllOf(
			SignalA.ForOne(dex.WithConditionID("signal-a")),
			SignalB.ForOne(dex.WithConditionID("signal-b")),
		), nil
	case "combo":
		return dex.AnyComboOf(
			dex.Combo(
				SignalA.ForOne(dex.WithConditionID("signal-a")),
				dex.Timer(timeout, dex.WithConditionID("timeout")),
			),
			dex.Combo(
				SignalB.ForOne(dex.WithConditionID("signal-b")),
			),
		), nil
	default:
		return nil, fmt.Errorf("unknown wait mode %q", input.Mode)
	}
}

func (waitTypesStep) Execute(_ dex.Context, input WaitTypesInput) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input.Mode), nil
}

func (*WaitTypesFlow) SignalA(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := SignalA.Publish(ctx, "signal-a"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*WaitTypesFlow) SignalB(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := SignalB.Publish(ctx, "signal-b"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

var _ dex.Flow = (*WaitTypesFlow)(nil)
