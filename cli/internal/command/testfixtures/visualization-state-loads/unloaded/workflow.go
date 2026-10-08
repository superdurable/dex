// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package unloaded

import "github.com/superdurable/dex/sdk-go/dex"

var (
	Subscribers = dex.DefineAttributeMap[string]("subscribers")
	Settings    = dex.DefineAttributeMap[string]("settings")
	Decisions   = dex.DefineChannel[string]("decisions")
	Replies     = dex.DefineChannelMap[string]("replies")
)

type DeliveryInput struct {
	Recipients []string `json:"recipients"`
}

type UnloadedReadsFlow struct {
	dex.FlowDefaults
}

func (*UnloadedReadsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(snapshotRecipients{}),
		dex.DefineStep(notifyRecipients{}),
		dex.DefineStep(awaitReply{}),
	}
}

func (flow *UnloadedReadsFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetSubscriber, nil),
		dex.DefineRPC(flow.GetLocale, &dex.RPCOptions{
			LoadAttributeMapInstances: []dex.AttributeMapLoad{Settings.Load("theme")},
		}),
		dex.DefineRPC(flow.ListDecisions, nil),
		dex.DefineRPC(flow.GetContact, nil),
	}
}

func (*UnloadedReadsFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Subscribers, Settings},
		Channels:   []dex.ChannelDef{Decisions, Replies},
	}
}

type snapshotRecipients struct {
	dex.StepDefaultsNoWaitFor[DeliveryInput]
}

func (snapshotRecipients) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteLoadAttributeMapInstances: []dex.AttributeMapLoad{Subscribers.Load("owner")},
	}
}

func (snapshotRecipients) Execute(ctx dex.Context, input DeliveryInput) (*dex.StepDecision, error) {
	input.Recipients = Subscribers.AllInstanceKeys(ctx)
	return dex.GoTo(notifyRecipients{}, input), nil
}

type notifyRecipients struct {
	dex.StepDefaultsNoWaitFor[DeliveryInput]
}

func (notifyRecipients) Execute(ctx dex.Context, input DeliveryInput) (*dex.StepDecision, error) {
	for _, recipient := range input.Recipients {
		if _, err := deliveryAddress(ctx, Subscribers, recipient); err != nil {
			return nil, err
		}
	}
	return dex.GoTo(awaitReply{}, "owner"), nil
}

type awaitReply struct {
	dex.StepDefaults
}

func (awaitReply) WaitFor(ctx dex.Context, recipient string) (*dex.Wait, error) {
	if _, _, err := Replies.FindPendingMessage(ctx, recipient, "first"); err != nil {
		return nil, err
	}
	return dex.Until(Replies.ForOne(recipient)), nil
}

func (awaitReply) Execute(_ dex.Context, _ string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

func (*UnloadedReadsFlow) GetSubscriber(ctx dex.Context, recipient string) (*dex.RPCResult[string], error) {
	address, err := Subscribers.Get(ctx, recipient)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: address}, nil
}

func (*UnloadedReadsFlow) GetLocale(ctx dex.Context, _ dex.None) (*dex.RPCResult[string], error) {
	locale, err := Settings.Get(ctx, "locale")
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: locale}, nil
}

// dex:invocation-load attribute-map:contacts
func (*UnloadedReadsFlow) GetContact(ctx dex.Context, name string) (*dex.RPCResult[string], error) {
	contact, err := Settings.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: contact}, nil
}

func (*UnloadedReadsFlow) ListDecisions(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[[]dex.ChannelMessage[string]], error) {
	messages, err := Decisions.PendingMessages(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[[]dex.ChannelMessage[string]]{Output: messages}, nil
}
