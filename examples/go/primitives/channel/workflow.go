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
	ApprovalMessages    = dex.DefineChannel[string]("ApprovalMessages")
	QueuedMessages      = dex.DefineChannel[string]("QueuedMessages")
	PrioritizedMessages = dex.DefineChannel[string]("PrioritizedMessages")
)

type ChannelFlow struct {
	dex.FlowDefaults
}

type QueuedMessageReference struct {
	MessageID string `json:"messageId"`
}

func NewChannelFlow() *ChannelFlow {
	return &ChannelFlow{}
}

func (*ChannelFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(channelWait{})}
}

func (flow *ChannelFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.PublishApprovalMessage, nil),
		dex.DefineRPC(flow.EnqueueChannelMessage, nil),
		dex.DefineRPC(flow.GetQueuedMessages, &dex.RPCOptions{
			LoadChannels: []dex.ChannelDef{QueuedMessages},
		}),
		dex.DefineRPC(flow.DeleteQueuedMessage, &dex.RPCOptions{
			IsTransactional: true,
			LoadChannels:    []dex.ChannelDef{QueuedMessages},
		}),
		dex.DefineRPC(flow.GetPrioritizedMessages, &dex.RPCOptions{
			LoadChannels: []dex.ChannelDef{PrioritizedMessages},
		}),
		dex.DefineRPC(flow.MoveQueuedMessageToPrioritizedMessages, &dex.RPCOptions{
			IsTransactional: true,
			LoadChannels:    []dex.ChannelDef{QueuedMessages},
		}),
	}
}

func (*ChannelFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels: []dex.ChannelDef{ApprovalMessages, QueuedMessages, PrioritizedMessages},
	}
}

type channelWait struct {
	dex.StepDefaults
}

func (channelWait) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteLoadChannels: []dex.ChannelDef{QueuedMessages},
	}
}

func (channelWait) WaitFor(_ dex.Context, input int) (*dex.Wait, error) {
	return dex.AnyOf(
		ApprovalMessages.ForOne(),
		dex.Timer(time.Duration(input)*time.Second),
	), nil
}

func (channelWait) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	pendingQueuedMessages, err := QueuedMessages.PendingMessages(ctx)
	if err != nil {
		return nil, err
	}
	if len(pendingQueuedMessages) > 0 {
		if err := QueuedMessages.Delete(ctx, pendingQueuedMessages[0].MessageID); err != nil {
			return nil, err
		}
		return dex.GracefulComplete(pendingQueuedMessages[0].Value), nil
	}
	if ctx.HasTimerFired() {
		return dex.GracefulComplete("approval timed out"), nil
	}
	approvalMessageValues, err := ApprovalMessages.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(approvalMessageValues[0]), nil
}

func (*ChannelFlow) PublishApprovalMessage(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := ApprovalMessages.Publish(ctx, "approved"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*ChannelFlow) EnqueueChannelMessage(ctx dex.Context, value string) (*dex.RPCResult[dex.None], error) {
	if err := QueuedMessages.Publish(ctx, value); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*ChannelFlow) GetQueuedMessages(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[[]dex.ChannelMessage[string]], error) {
	messages, err := QueuedMessages.PendingMessages(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[[]dex.ChannelMessage[string]]{Output: messages}, nil
}

func (*ChannelFlow) DeleteQueuedMessage(
	ctx dex.Context,
	queuedMessageReference QueuedMessageReference,
) (*dex.RPCResult[dex.None], error) {
	if err := QueuedMessages.Delete(ctx, queuedMessageReference.MessageID); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*ChannelFlow) GetPrioritizedMessages(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[[]dex.ChannelMessage[string]], error) {
	messages, err := PrioritizedMessages.PendingMessages(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[[]dex.ChannelMessage[string]]{Output: messages}, nil
}

func (*ChannelFlow) MoveQueuedMessageToPrioritizedMessages(
	ctx dex.Context,
	queuedMessageReference QueuedMessageReference,
) (*dex.RPCResult[dex.None], error) {
	messageToPrioritize, found, err := QueuedMessages.FindPendingMessage(ctx, queuedMessageReference.MessageID)
	if err != nil {
		return nil, err
	}
	if err := QueuedMessages.Delete(ctx, queuedMessageReference.MessageID); err != nil {
		return nil, err
	}
	if found {
		if err := PrioritizedMessages.Publish(ctx, messageToPrioritize.Value); err != nil {
			return nil, err
		}
	}
	return &dex.RPCResult[dex.None]{}, nil
}

var _ dex.Flow = (*ChannelFlow)(nil)
