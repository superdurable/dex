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

package channel

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	Approval = dex.DefineChannel[string]("Approval")
	Queued   = dex.DefineChannel[string]("Queued")
	Moved    = dex.DefineChannel[string]("Moved")
)

type ChannelFlow struct {
	dex.FlowDefaults
}

type MoveMessage struct {
	MessageID string `json:"messageId"`
	Value     string `json:"value"`
}

func NewChannelFlow() *ChannelFlow {
	return &ChannelFlow{}
}

func (*ChannelFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(channelWaitStep{})}
}

func (*ChannelFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{Approval, Queued, Moved},
	}
}

type channelWaitStep struct {
	dex.StepDefaults
}

func (channelWaitStep) WaitFor(_ dex.Context, input int) (*dex.Wait, error) {
	return dex.AnyOf(
		Approval.ForOne(),
		dex.Timer(time.Duration(input)*time.Second),
	), nil
}

func (channelWaitStep) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.GracefulComplete("approval timed out"), nil
	}
	results, err := Approval.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(results[0]), nil
}

func (*ChannelFlow) Approve(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := Approval.Publish(ctx, "approved"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*ChannelFlow) Move(ctx dex.Context, message MoveMessage) (*dex.RPCResult[dex.None], error) {
	if err := Queued.Delete(ctx, message.MessageID); err != nil {
		return nil, err
	}
	if err := Moved.Publish(ctx, message.Value); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

var _ dex.Flow = (*ChannelFlow)(nil)
