// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package loaded

import "github.com/superdurable/dex/sdk-go/dex"

const ownerInstance = "owner"

var (
	Subscribers = dex.DefineAttributeMap[string]("subscribers")
	Profiles    = dex.DefineAttributeMap[string]("profiles")
	Contacts    = dex.DefineAttributeMap[string]("contacts")
	ReleaseIDs  = dex.DefineAttributeMap[string]("release-ids")
	Approvals   = dex.DefineChannel[string]("approvals")
	Retries     = dex.DefineChannel[string]("retries")
	Replies     = dex.DefineChannelMap[string]("replies")
)

var ownerSubscriberLoads = []dex.AttributeMapLoad{Subscribers.Load(ownerInstance)}

type DeliveryInput struct {
	Recipients []string `json:"recipients"`
}

type LoadedReadsFlow struct {
	dex.FlowDefaults
}

func (*LoadedReadsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(collectRecipients{}),
		dex.DefineStep(deliverMessages{}),
		dex.DefineStep(pollRetries{}),
		dex.DefineStep(drainRetries{}),
		dex.DefineStep(recordDeliveryFailure{}),
	}
}

func (flow *LoadedReadsFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetOwner, &dex.RPCOptions{LoadAttributeMapInstances: ownerSubscriberLoads}),
		dex.DefineRPC(flow.GetProfile, nil),
		dex.DefineRPC(flow.GetContact, nil),
		dex.DefineRPC(flow.RecordApproval, nil),
	}
}

func (*LoadedReadsFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Subscribers, Profiles, Contacts, ReleaseIDs},
		Channels:   []dex.ChannelDef{Approvals, Retries, Replies},
	}
}

type collectRecipients struct {
	dex.StepDefaultsNoWaitFor[DeliveryInput]
}

func (collectRecipients) GetStepOptions() *dex.StepOptions {
	return wholeMapOptions(Subscribers)
}

func (collectRecipients) Execute(ctx dex.Context, input DeliveryInput) (*dex.StepDecision, error) {
	for _, key := range Subscribers.AllInstanceKeys(ctx) {
		if _, err := Subscribers.Get(ctx, key); err != nil {
			return nil, err
		}
		input.Recipients = append(input.Recipients, key)
	}
	return dex.GoTo(deliverMessages{}, input, dex.WithStepOptions(&dex.StepOptions{
		ExecuteLoadChannelMaps: []dex.ChannelDef{Replies},
	})), nil
}

type deliverMessages struct {
	dex.StepDefaultsNoWaitFor[DeliveryInput]
}

func (deliverMessages) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteFailure: dex.ProceedToOnExecuteFailure(recordDeliveryFailure{}, &dex.StepOptions{
			ExecuteLoadChannels: []dex.ChannelDef{Approvals},
		}),
	}
}

func (deliverMessages) Execute(ctx dex.Context, input DeliveryInput) (*dex.StepDecision, error) {
	for _, recipient := range input.Recipients {
		if _, err := Replies.PendingMessages(ctx, recipient); err != nil {
			return nil, err
		}
		if err := recordRelease(ctx, ReleaseIDs, recipient); err != nil {
			return nil, err
		}
	}
	return dex.GoTo(pollRetries{}, len(input.Recipients)), nil
}

type pollRetries struct {
	dex.StepDefaultsNoWaitFor[int]
}

func (pollRetries) Execute(ctx dex.Context, attempts int) (*dex.StepDecision, error) {
	for attempt := 0; ; attempt++ {
		if attempt >= attempts {
			return dex.GoTo(drainRetries{}, attempt, dex.WithStepOptions(retryLoads)), nil
		}
		if Retries.Size(ctx) == 0 {
			return dex.GracefulComplete(attempt), nil
		}
	}
}

var retryLoads = &dex.StepOptions{ExecuteLoadChannels: []dex.ChannelDef{Retries}}

type drainRetries struct {
	dex.StepDefaultsNoWaitFor[int]
}

func (drainRetries) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	retries, err := Retries.PendingMessages(ctx)
	if err != nil {
		return nil, err
	}
	for _, retry := range retries {
		if err := Retries.Delete(ctx, retry.MessageID); err != nil {
			return nil, err
		}
	}
	return dex.GracefulComplete(len(retries)), nil
}

type recordDeliveryFailure struct {
	dex.StepDefaultsNoWaitFor[DeliveryInput]
}

func (recordDeliveryFailure) Execute(ctx dex.Context, _ DeliveryInput) (*dex.StepDecision, error) {
	approvals, err := Approvals.PendingMessages(ctx)
	if err != nil {
		return nil, err
	}
	return dex.ForceFail("delivery failed with pending approvals: " + string(rune('0'+len(approvals)))), nil
}

func (*LoadedReadsFlow) HandleTimeout(ctx dex.Context) (*dex.StepDecision, error) {
	return dex.GracefulComplete(Subscribers.MapSize(ctx)), nil
}

func (*LoadedReadsFlow) GetOwner(ctx dex.Context, _ dex.None) (*dex.RPCResult[string], error) {
	owner, err := Subscribers.Get(ctx, ownerInstance)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: owner}, nil
}

func (*LoadedReadsFlow) GetProfile(ctx dex.Context, email string) (*dex.RPCResult[string], error) {
	profile, err := Profiles.Get(ctx, email)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: profile}, nil
}

// dex:invocation-load attribute-map:contacts
func (*LoadedReadsFlow) GetContact(ctx dex.Context, name string) (*dex.RPCResult[string], error) {
	contact, err := Contacts.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: contact}, nil
}

func (*LoadedReadsFlow) RecordApproval(ctx dex.Context, approval string) (*dex.RPCResult[int], error) {
	if err := Approvals.Publish(ctx, approval); err != nil {
		return nil, err
	}
	if err := Approvals.Delete(ctx, approval); err != nil {
		return nil, err
	}
	if err := Subscribers.Set(ctx, approval, approval); err != nil {
		return nil, err
	}
	if err := Subscribers.Delete(ctx, ownerInstance); err != nil {
		return nil, err
	}
	pending := Approvals.Size(ctx) + Replies.Size(ctx, approval) + Replies.MapSize(ctx) + len(Replies.AllInstanceKeys(ctx))
	return &dex.RPCResult[int]{Output: pending}, nil
}
