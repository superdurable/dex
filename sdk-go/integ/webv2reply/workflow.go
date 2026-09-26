// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package webv2reply

import (
	"errors"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	StatusAwaitingDecision = "awaiting-reply-approval"
	StatusReplySent        = "reply-sent"
	DecidePermission       = "reply.decide"
)

var ReplyDecisions = dex.DefineChannel[string]("reply-decisions")

var (
	replyThread = dex.DefineAttribute[string]("reply-thread")
	replyStatus = dex.DefineAttribute[string]("reply-status")
	replyDraft  = dex.DefineAttribute[string]("reply-draft")
)

type Input struct {
	Thread string `json:"thread"`
}

type Flow struct {
	dex.FlowDefaults
}

func StartStepType() string {
	return dex.GetFinalStepType[Input](awaitDecision{})
}

func (*Flow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(awaitDecision{}),
		dex.DefineStep(sendReply{}),
		dex.DefineStep(closeThread{}),
	}
}

func (*Flow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{replyThread, replyStatus, replyDraft},
		Channels:   []dex.ChannelDef{ReplyDecisions},
	}
}

func (flow *Flow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
		dex.DefineRPC(flow.ApproveDecision, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Send reply",
				dex.WhenAttributeMatches(replyStatus, dex.AttributeMatchEqual(StatusAwaitingDecision)),
				dex.ActionRequiresPermission(DecidePermission),
			),
		}),
	}
}

// dex:field attribute-key:reply-thread value-type:string editable:false description:"Thread" ui-slot:title
// dex:field attribute-key:reply-status value-type:string editable:false description:"Reply status" ui-slot:status
func (*Flow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	thread, err := optionalAttribute(ctx, replyThread)
	if err != nil {
		return nil, err
	}
	status, err := optionalAttribute(ctx, replyStatus)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"reply-thread": thread,
		"reply-status": status,
	}}, nil
}

// dex:field attribute-key:reply-thread value-type:string editable:false description:"Thread" ui-slot:title
// dex:field attribute-key:reply-draft value-type:string editable:true description:"Reply draft"
func (*Flow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	thread, err := optionalAttribute(ctx, replyThread)
	if err != nil {
		return nil, err
	}
	draft, err := optionalAttribute(ctx, replyDraft)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"reply-thread": thread,
		"reply-draft":  draft,
	}}, nil
}

func (*Flow) ApproveDecision(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := ReplyDecisions.Publish(ctx, StatusReplySent); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Wait for an operator to approve the reply."
type awaitDecision struct {
	dex.StepDefaults
}

func (awaitDecision) WaitFor(ctx dex.Context, input Input) (*dex.Wait, error) {
	if err := replyThread.Set(ctx, input.Thread); err != nil {
		return nil, err
	}
	if err := replyStatus.Set(ctx, StatusAwaitingDecision); err != nil {
		return nil, err
	}
	return dex.AllOf(ReplyDecisions.ForOne()), nil
}

func (awaitDecision) Execute(ctx dex.Context, _ Input) (*dex.StepDecision, error) {
	decisions, err := ReplyDecisions.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(sendReply{}, decisions[0]), nil
}

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Record that the approved reply was sent."
type sendReply struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (sendReply) Execute(ctx dex.Context, decision string) (*dex.StepDecision, error) {
	if err := replyStatus.Set(ctx, decision); err != nil {
		return nil, err
	}
	return dex.GoTo(closeThread{}, decision), nil
}

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Close the thread after the reply is sent."
type closeThread struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (closeThread) Execute(_ dex.Context, decision string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(decision), nil
}

func optionalAttribute(ctx dex.Context, attribute dex.Attribute[string]) (any, error) {
	value, err := attribute.Get(ctx)
	if err == nil {
		return value, nil
	}
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		return nil, nil
	}
	return nil, err
}
