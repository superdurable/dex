// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package lints

import (
	"context"
	"errors"

	"github.com/superdurable/dex/sdk-go/dex"
)

const statusAwaitingReview = "awaiting_review"

var (
	DraftStatus = dex.DefineAttribute[string]("draft-status")
	Recipients  = dex.DefineAttribute[[]string]("recipients")
	Decisions   = dex.DefineChannel[string]("review-decisions")
)

type DraftReviewFlow struct {
	dex.FlowDefaults
	client *dex.Client
}

func StartDraftReview(ctx context.Context, client *dex.Client, reviewID string) error {
	_, err := client.StartFlow(ctx, &DraftReviewFlow{}, reviewID, reviewID, dex.StartFlowOptions{
		AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
		RequestID:      &reviewID,
	})
	return err
}

func (flow *DraftReviewFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(startRegistry{client: flow.client}),
		dex.DefineStep(snapshotRecipients{client: flow.client}),
		dex.DefineStep(snapshotKnownRecipients{client: flow.client}),
		dex.DefineStep(awaitReview{}),
	}
}

func (flow *DraftReviewFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.ApproveDraft, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Approve",
				dex.WhenAttributeMatches(DraftStatus, dex.AttributeMatchEqual(statusAwaitingReview)),
				dex.ActionRequiresPermission("draft.review"),
			),
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(DraftStatus)},
		}),
		dex.DefineRPC(flow.RejectDraft, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Reject",
				dex.WhenAttributeMatches(DraftStatus, dex.AttributeMatchEqual(statusAwaitingReview)),
				dex.ActionRequiresPermission("draft.review"),
			),
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(DraftStatus)},
		}),
	}
}

func (*DraftReviewFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{DraftStatus, Recipients},
		Channels:   []dex.ChannelDef{Decisions},
	}
}

type startRegistry struct {
	dex.StepDefaultsNoWaitFor[string]
	client *dex.Client
}

func (step startRegistry) Execute(_ dex.Context, registryID string) (*dex.StepDecision, error) {
	if err := ensureRegistry(context.Background(), step.client, registryID); err != nil {
		return nil, err
	}
	return dex.GoTo(snapshotRecipients{}, registryID), nil
}

type snapshotRecipients struct {
	dex.StepDefaultsNoWaitFor[string]
	client *dex.Client
}

func (step snapshotRecipients) Execute(ctx dex.Context, registryID string) (*dex.StepDecision, error) {
	var subscribers []string
	if err := step.client.InvokeRPC(context.Background(), registryID, (&SubscriberRegistryFlow{}).ListSubscribers, nil, &subscribers); err != nil {
		return nil, err
	}
	if err := Recipients.Set(ctx, subscribers); err != nil {
		return nil, err
	}
	return dex.GoTo(snapshotKnownRecipients{}, registryID), nil
}

type snapshotKnownRecipients struct {
	dex.StepDefaultsNoWaitFor[string]
	client *dex.Client
}

func (step snapshotKnownRecipients) Execute(ctx dex.Context, registryID string) (*dex.StepDecision, error) {
	subscribers, err := readRegistry(context.Background(), step.client, registryID)
	if err != nil {
		return nil, err
	}
	if err := Recipients.Set(ctx, subscribers); err != nil {
		return nil, err
	}
	if err := DraftStatus.Set(ctx, statusAwaitingReview); err != nil {
		return nil, err
	}
	return dex.GoTo(awaitReview{}, registryID), nil
}

type awaitReview struct {
	dex.StepDefaults
}

func (awaitReview) WaitFor(_ dex.Context, _ string) (*dex.Wait, error) {
	return dex.Until(Decisions.ForOne()), nil
}

func (awaitReview) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	decisions, err := Decisions.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(decisions), nil
}

func (*DraftReviewFlow) ApproveDraft(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := Decisions.Publish(ctx, "approve"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*DraftReviewFlow) RejectDraft(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := requireAwaitingReview(ctx); err != nil {
		return nil, err
	}
	if err := DraftStatus.Set(ctx, "rejected"); err != nil {
		return nil, err
	}
	if err := Decisions.Publish(ctx, "reject"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func requireAwaitingReview(ctx dex.Context) error {
	status, err := DraftStatus.Get(ctx)
	if err != nil {
		return err
	}
	if status != statusAwaitingReview {
		return errors.New("the draft is no longer awaiting review")
	}
	return nil
}
