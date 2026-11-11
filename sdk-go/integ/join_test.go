// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

type joinScenario string

const (
	joinAllOfInReportOrder joinScenario = "all-of-in-report-order"
	joinMultipleInstances  joinScenario = "multiple-instances"
	joinPartialRounds      joinScenario = "partial-rounds"
	joinKeyIsolation       joinScenario = "key-isolation"
	joinFirstAndCancel     joinScenario = "first-and-cancel"
	joinRPCReportsCanceled joinScenario = "rpc-reports-canceled"
	joinForkTwice          joinScenario = "fork-twice"
	joinForkFromRPC        joinScenario = "fork-from-rpc"
	joinInvalidCondition   joinScenario = "invalid-condition"
	joinZeroBranches       joinScenario = "zero-branches"
	joinZeroRemaining      joinScenario = "zero-remaining"
	joinTimerDeadline      joinScenario = "timer-deadline"
)

const (
	joinInvalidForBranchesZero      = "for-branches-zero"
	joinInvalidForBranchesAboveMax  = "for-branches-above-branch-count"
	joinInvalidRemainingNegative    = "for-remaining-negative"
	joinInvalidRemainingAboveMax    = "for-remaining-above-branch-count"
	joinInvalidFirstOfZeroBranches  = "for-first-of-zero-branches"
	joinRPCForkModeStepExecutionKey = "fork"
	joinRPCForkModeExplicitKey      = "explicit-key"
	joinRPCForkModeSlashKey         = "slash-key"
)

var (
	joinResults       = dex.DefineJoin[string]("go-join-results")
	joinResultsCopy   = dex.DefineChannelMap[string]("go-join-results")
	joinBranchRelease = dex.DefineChannelMap[dex.None]("go-join-branch-release")
	joinGate          = dex.DefineChannel[dex.None]("go-join-gate")
	joinBranchWaiting = dex.DefineChannel[string]("go-join-branch-waiting")
)

type joinFlow struct {
	dex.FlowDefaults
}

// joinPlan is the scenario input that the split Step and the join Step pass along.
type joinPlan struct {
	Scenario         joinScenario   `json:"scenario"`
	BranchNames      []string       `json:"branchNames"`
	ReleasedBranches []string       `json:"releasedBranches"`
	RequiredResults  int            `json:"requiredResults"`
	RoundNumber      int            `json:"roundNumber"`
	RoundCount       int            `json:"roundCount"`
	PreviousRound    *dex.JoinRound `json:"previousRound,omitempty"`
	InvalidCondition string         `json:"invalidCondition"`
	Summary          joinSummary    `json:"summary"`
}

type joinSummary struct {
	RoundKeys        []string   `json:"roundKeys"`
	BranchCounts     []int      `json:"branchCounts"`
	BranchResults    [][]string `json:"branchResults"`
	RemainingResults [][]string `json:"remainingResults"`
	HasTimerFired    bool       `json:"hasTimerFired"`
	FailureDetail    string     `json:"failureDetail"`
}

type joinBranchInput struct {
	Round               dex.JoinRound `json:"round"`
	Name                string        `json:"name"`
	WaitsForRelease     bool          `json:"waitsForRelease"`
	ReportsInsteadOfEnd bool          `json:"reportsInsteadOfEnd"`
}

type joinStepInput struct {
	Plan  joinPlan      `json:"plan"`
	Round dex.JoinRound `json:"round"`
}

type joinChannelSizesInput struct {
	RoundKey   string `json:"roundKey"`
	BranchName string `json:"branchName"`
}

type joinChannelSizes struct {
	PendingResults  int `json:"pendingResults"`
	PendingReleases int `json:"pendingReleases"`
	WaitingBranches int `json:"waitingBranches"`
}

type joinReportInput struct {
	Round             dex.JoinRound `json:"round"`
	Result            string        `json:"result"`
	CancelsBranchStep bool          `json:"cancelsBranchStep"`
}

type joinRPCForkInput struct {
	Mode     string `json:"mode"`
	RoundKey string `json:"roundKey"`
}

func (joinFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(joinSplitStep{}),
		dex.DefineStep(joinBranchStep{}),
		dex.DefineStep(joinCancelableBranchStep{}),
		dex.DefineStep(joinGateStep{}),
		dex.DefineStep(joinStep{}),
		dex.DefineStep(joinFinishStep{}),
		dex.DefineStep(joinSplitRecoveryStep{}),
	}
}

func (flow joinFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.ReleaseJoinBranch, nil),
		dex.DefineRPC(flow.OpenJoinGate, nil),
		dex.DefineRPC(flow.GetJoinChannelSizes, nil),
		dex.DefineRPC(flow.ReportJoinBranchResult, nil),
		dex.DefineRPC(flow.ForkJoinFromRPC, nil),
	}
}

func (joinFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{joinResults, joinBranchRelease, joinGate, joinBranchWaiting}}
}

func (joinFlow) ReleaseJoinBranch(ctx dex.Context, branchName string) (*dex.RPCResult[dex.None], error) {
	if err := joinBranchRelease.Publish(ctx, branchName, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (joinFlow) OpenJoinGate(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := joinGate.Publish(ctx, nil); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// GetJoinChannelSizes reads a round's pending results through the Join's ChannelMap.
func (joinFlow) GetJoinChannelSizes(
	ctx dex.Context,
	input joinChannelSizesInput,
) (*dex.RPCResult[joinChannelSizes], error) {
	return &dex.RPCResult[joinChannelSizes]{Output: joinChannelSizes{
		PendingResults:  joinResults.ChannelMap().Size(ctx, input.RoundKey),
		PendingReleases: joinBranchRelease.Size(ctx, input.BranchName),
		WaitingBranches: joinBranchWaiting.Size(ctx),
	}}, nil
}

func (joinFlow) ReportJoinBranchResult(
	ctx dex.Context,
	input joinReportInput,
) (*dex.RPCResult[dex.None], error) {
	if err := joinResults.ReportBranchResult(ctx, input.Round, input.Result); err != nil {
		return nil, err
	}
	result := &dex.RPCResult[dex.None]{}
	if input.CancelsBranchStep {
		result.CancelSteps(joinCancelableBranchStep{})
	}
	return result, nil
}

// ForkJoinFromRPC returns the fork error text as output so the test can assert it.
func (joinFlow) ForkJoinFromRPC(ctx dex.Context, input joinRPCForkInput) (*dex.RPCResult[string], error) {
	plan := joinPlan{Scenario: joinForkFromRPC, BranchNames: []string{"X", "Y"}, RoundNumber: 1, RoundCount: 1}
	branchBuilders := joinBranchBuilders(plan)
	joinBuilder := func(round dex.JoinRound) dex.StepMovement {
		return dex.MovementOf(joinStep{}, joinStepInput{Plan: plan, Round: round})
	}
	var movements []dex.StepMovement
	var err error
	switch input.Mode {
	case joinRPCForkModeStepExecutionKey:
		movements, err = joinResults.Fork(ctx, branchBuilders, joinBuilder)
	case joinRPCForkModeExplicitKey, joinRPCForkModeSlashKey:
		movements, err = joinResults.ForkWithRoundKey(input.RoundKey, branchBuilders, joinBuilder)
	default:
		return nil, fmt.Errorf("unknown RPC fork mode %q", input.Mode)
	}
	if err != nil {
		return &dex.RPCResult[string]{Output: err.Error()}, nil
	}
	return &dex.RPCResult[string]{NextSteps: movements}, nil
}

// joinSplitStep forks one round. From round two it first resets the previous round.
type joinSplitStep struct {
	dex.StepDefaults
}

func (joinSplitStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:   &dex.RetryPolicy{MaximumAttempts: 1},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(joinSplitRecoveryStep{}, nil),
	}
}

func (joinSplitStep) WaitFor(_ dex.Context, plan joinPlan) (*dex.Wait, error) {
	if plan.PreviousRound == nil {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(joinResults.ForRemainingBranches(*plan.PreviousRound, plan.RequiredResults)), nil
}

func (joinSplitStep) Execute(ctx dex.Context, plan joinPlan) (*dex.StepDecision, error) {
	if plan.PreviousRound != nil {
		remainingResults, err := joinResults.RemainingBranchResults(ctx, *plan.PreviousRound)
		if err != nil {
			return nil, err
		}
		plan.Summary.RemainingResults = append(plan.Summary.RemainingResults, nonNilStrings(remainingResults))
	}
	if plan.Scenario == joinForkFromRPC {
		return dex.DeadEnd(), nil
	}
	joinBuilder := func(round dex.JoinRound) dex.StepMovement {
		input := joinStepInput{Plan: plan, Round: round}
		if plan.Scenario == joinAllOfInReportOrder {
			return dex.MovementOf(joinGateStep{}, input)
		}
		return dex.MovementOf(joinStep{}, input)
	}
	movements, err := joinResults.Fork(ctx, joinBranchBuilders(plan), joinBuilder)
	if err != nil {
		return nil, err
	}
	if plan.Scenario == joinForkTwice {
		if _, err := joinResults.Fork(ctx, joinBranchBuilders(plan), joinBuilder); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("the second Fork of one Join in one invocation succeeded")
	}
	return dex.GoToMany(movements...), nil
}

func joinBranchBuilders(plan joinPlan) []func(dex.JoinRound) dex.StepMovement {
	builders := make([]func(dex.JoinRound) dex.StepMovement, 0, len(plan.BranchNames))
	for _, name := range plan.BranchNames {
		builders = append(builders, func(round dex.JoinRound) dex.StepMovement {
			input := joinBranchInput{
				Round:               round,
				Name:                name,
				WaitsForRelease:     slices.Contains(plan.ReleasedBranches, name),
				ReportsInsteadOfEnd: plan.Scenario == joinMultipleInstances,
			}
			if plan.Scenario == joinRPCReportsCanceled && name == "B" {
				return dex.MovementOf(joinCancelableBranchStep{}, input)
			}
			return dex.MovementOf(joinBranchStep{}, input)
		})
	}
	return builders
}

type joinBranchStep struct {
	dex.StepDefaults
}

func (joinBranchStep) WaitFor(_ dex.Context, input joinBranchInput) (*dex.Wait, error) {
	if !input.WaitsForRelease {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(joinBranchRelease.ForOne(input.Name)), nil
}

func (joinBranchStep) Execute(ctx dex.Context, input joinBranchInput) (*dex.StepDecision, error) {
	if input.ReportsInsteadOfEnd {
		if err := joinResults.ReportBranchResult(ctx, input.Round, input.Name); err != nil {
			return nil, err
		}
		return dex.DeadEnd(), nil
	}
	return joinResults.EndBranch(ctx, input.Round, input.Name)
}

// joinCancelableBranchStep never receives its release; an RPC cancels it.
type joinCancelableBranchStep struct {
	dex.StepDefaults
}

// WaitFor marks the branch as waiting in the same commit that registers its wait.
func (joinCancelableBranchStep) WaitFor(ctx dex.Context, input joinBranchInput) (*dex.Wait, error) {
	if err := joinBranchWaiting.Publish(ctx, input.Name); err != nil {
		return nil, err
	}
	return dex.Until(joinBranchRelease.ForOne(input.Name)), nil
}

func (joinCancelableBranchStep) Execute(ctx dex.Context, input joinBranchInput) (*dex.StepDecision, error) {
	return joinResults.EndBranch(ctx, input.Round, input.Name)
}

// joinGateStep delays the join Step until the test opens the gate.
type joinGateStep struct {
	dex.StepDefaults
}

func (joinGateStep) WaitFor(dex.Context, joinStepInput) (*dex.Wait, error) {
	return dex.Until(joinGate.ForOne()), nil
}

func (joinGateStep) Execute(_ dex.Context, input joinStepInput) (*dex.StepDecision, error) {
	return dex.GoTo(joinStep{}, input), nil
}

type joinStep struct {
	dex.StepDefaults
}

func (joinStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForRetry:   &dex.RetryPolicy{MaximumAttempts: 1},
		WaitForFailure: dex.ProceedOnWaitForFailure,
	}
}

func (joinStep) WaitFor(_ dex.Context, input joinStepInput) (*dex.Wait, error) {
	round := input.Round
	switch input.Plan.Scenario {
	case joinPartialRounds:
		return dex.Until(joinResults.ForBranches(round, input.Plan.RequiredResults)), nil
	case joinFirstAndCancel:
		return dex.AllOf(joinResults.ForFirstBranch(round), joinGate.ForOne()), nil
	case joinTimerDeadline:
		return dex.AnyOf(joinResults.ForAllBranches(round), dex.Timer(24*time.Hour)), nil
	case joinInvalidCondition:
		return dex.Until(invalidJoinCondition(input.Plan.InvalidCondition, round)), nil
	default:
		return dex.Until(joinResults.ForAllBranches(round)), nil
	}
}

func invalidJoinCondition(invalidCondition string, round dex.JoinRound) dex.Condition {
	switch invalidCondition {
	case joinInvalidForBranchesZero:
		return joinResults.ForBranches(round, 0)
	case joinInvalidForBranchesAboveMax:
		return joinResults.ForBranches(round, round.BranchCount+1)
	case joinInvalidRemainingNegative:
		return joinResults.ForRemainingBranches(round, -1)
	case joinInvalidRemainingAboveMax:
		return joinResults.ForRemainingBranches(round, round.BranchCount+1)
	default:
		return joinResults.ForFirstBranch(round)
	}
}

func (joinStep) Execute(ctx dex.Context, input joinStepInput) (*dex.StepDecision, error) {
	plan := input.Plan
	if ctx.WaitForMethodFailed() {
		plan.Summary.FailureDetail = ctx.RecoveryError().Detail
		return dex.ForceComplete(plan.Summary), nil
	}
	branchResults, err := joinResults.BranchResults(ctx, input.Round)
	if err != nil {
		return nil, err
	}
	plan.Summary.RoundKeys = append(plan.Summary.RoundKeys, input.Round.RoundKey)
	plan.Summary.BranchCounts = append(plan.Summary.BranchCounts, input.Round.BranchCount)
	plan.Summary.BranchResults = append(plan.Summary.BranchResults, nonNilStrings(branchResults))
	plan.Summary.HasTimerFired = ctx.HasTimerFired()
	switch {
	case plan.Scenario == joinFirstAndCancel:
		return dex.GoTo(joinFinishStep{}, plan.Summary).CancelSiblingSteps(joinBranchStep{}), nil
	case plan.Scenario == joinRPCReportsCanceled, plan.Scenario == joinKeyIsolation:
		return dex.GoTo(joinFinishStep{}, plan.Summary), nil
	case plan.RoundNumber < plan.RoundCount:
		nextPlan := plan
		nextPlan.RoundNumber++
		nextPlan.PreviousRound = &input.Round
		return dex.GoTo(joinSplitStep{}, nextPlan), nil
	default:
		return dex.ForceComplete(plan.Summary), nil
	}
}

// joinFinishStep keeps the Flow open until the test has read the pending results.
type joinFinishStep struct {
	dex.StepDefaults
}

func (joinFinishStep) WaitFor(dex.Context, joinSummary) (*dex.Wait, error) {
	return dex.Until(joinGate.ForOne()), nil
}

func (joinFinishStep) Execute(_ dex.Context, summary joinSummary) (*dex.StepDecision, error) {
	return dex.ForceComplete(summary), nil
}

type joinSplitRecoveryStep struct {
	dex.StepDefaultsNoWaitFor[joinPlan]
}

func (joinSplitRecoveryStep) Execute(ctx dex.Context, plan joinPlan) (*dex.StepDecision, error) {
	plan.Summary.FailureDetail = ctx.RecoveryError().Detail
	return dex.ForceComplete(plan.Summary), nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// joinDuplicateNameFlow registers a Join and a ChannelMap under one name.
type joinDuplicateNameFlow struct {
	dex.FlowDefaults
}

func (joinDuplicateNameFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(joinSplitRecoveryStep{})}
}

func (joinDuplicateNameFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{joinResults, joinResultsCopy}}
}

func TestJoinAllOfCountsResultsReportedBeforeTheJoinWaits(t *testing.T) {
	flowID := startJoinFlow(t, joinPlan{
		Scenario:         joinAllOfInReportOrder,
		BranchNames:      []string{"A", "B", "C"},
		ReleasedBranches: []string{"A", "B", "C"},
	})
	for _, branch := range []struct {
		name            string
		executionNumber int32
	}{{"A", 1}, {"C", 3}, {"B", 2}} {
		invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, branch.name, nil)
		waitForJoinStep(t, flowID, joinBranchStep{}, branch.executionNumber)
	}
	var sizes joinChannelSizes
	invokeJoinRPC(t, flowID, joinFlow{}.GetJoinChannelSizes, joinChannelSizesInput{RoundKey: "joinSplitStep-1", BranchName: "A"}, &sizes)
	require.Equal(t, 3, sizes.PendingResults)
	invokeJoinRPC(t, flowID, joinFlow{}.OpenJoinGate, nil, nil)

	summary := waitForJoinSummary(t, flowID)
	require.Equal(t, []string{"joinSplitStep-1"}, summary.RoundKeys)
	require.Equal(t, []int{3}, summary.BranchCounts)
	require.Equal(t, [][]string{{"A", "C", "B"}}, summary.BranchResults)
}

func TestJoinMultipleInstancesCountsTheMappedInputs(t *testing.T) {
	reviewers := []string{"reviewer-1", "reviewer-2", "reviewer-3", "reviewer-4"}
	flowID := startJoinFlow(t, joinPlan{Scenario: joinMultipleInstances, BranchNames: reviewers})

	summary := waitForJoinSummary(t, flowID)
	require.Equal(t, []int{len(reviewers)}, summary.BranchCounts)
	require.Len(t, summary.BranchResults, 1)
	require.ElementsMatch(t, reviewers, summary.BranchResults[0])
}

func TestJoinPartialRoundsResetBeforeTheNextRoundStarts(t *testing.T) {
	flowID := startJoinFlow(t, joinPlan{
		Scenario:         joinPartialRounds,
		BranchNames:      []string{"A", "B", "C"},
		ReleasedBranches: []string{"A", "B", "C"},
		RequiredResults:  2,
		RoundNumber:      1,
		RoundCount:       2,
	})
	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "A", nil)
	waitForJoinStep(t, flowID, joinBranchStep{}, 1)
	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "B", nil)
	waitForJoinStep(t, flowID, joinStep{}, 1)

	// Round two's split waits for round one's last result, so this release stays pending.
	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "A", nil)
	var sizes joinChannelSizes
	invokeJoinRPC(t, flowID, joinFlow{}.GetJoinChannelSizes, joinChannelSizesInput{RoundKey: "joinSplitStep-1", BranchName: "A"}, &sizes)
	require.Equal(t, joinChannelSizes{PendingResults: 0, PendingReleases: 1}, sizes)

	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "C", nil)
	waitForJoinStep(t, flowID, joinSplitStep{}, 2)
	// Round two's branch A consumed the early release; it is the fourth branch execution.
	waitForJoinStep(t, flowID, joinBranchStep{}, 4)
	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "C", nil)

	summary := waitForJoinSummary(t, flowID)
	require.Equal(t, []string{"joinSplitStep-1", "joinSplitStep-2"}, summary.RoundKeys)
	require.Equal(t, []int{3, 3}, summary.BranchCounts)
	require.Equal(t, [][]string{{"A", "B"}, {"A", "C"}}, summary.BranchResults)
	require.Equal(t, [][]string{{"C"}}, summary.RemainingResults)
}

func TestJoinResultsUnderAnotherRoundKeyDoNotSatisfyTheRound(t *testing.T) {
	flowID := startJoinFlow(t, joinPlan{
		Scenario:         joinKeyIsolation,
		BranchNames:      []string{"A", "B"},
		ReleasedBranches: []string{"A", "B"},
	})
	otherRound := dex.JoinRound{RoundKey: "joinSplitStep-99", BranchCount: 2}
	invokeJoinRPC(t, flowID, joinFlow{}.ReportJoinBranchResult, joinReportInput{Round: otherRound, Result: "other-1"}, nil)
	invokeJoinRPC(t, flowID, joinFlow{}.ReportJoinBranchResult, joinReportInput{Round: otherRound, Result: "other-2"}, nil)
	var sizes joinChannelSizes
	invokeJoinRPC(t, flowID, joinFlow{}.GetJoinChannelSizes, joinChannelSizesInput{RoundKey: otherRound.RoundKey, BranchName: "A"}, &sizes)
	require.Equal(t, 2, sizes.PendingResults)

	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "B", nil)
	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "A", nil)
	waitForJoinStep(t, flowID, joinStep{}, 1)
	// The join consumed its own round only; the other round's results are still pending.
	invokeJoinRPC(t, flowID, joinFlow{}.GetJoinChannelSizes, joinChannelSizesInput{RoundKey: otherRound.RoundKey, BranchName: "A"}, &sizes)
	require.Equal(t, 2, sizes.PendingResults)
	invokeJoinRPC(t, flowID, joinFlow{}.GetJoinChannelSizes, joinChannelSizesInput{RoundKey: "joinSplitStep-1", BranchName: "A"}, &sizes)
	require.Equal(t, 0, sizes.PendingResults)
	invokeJoinRPC(t, flowID, joinFlow{}.OpenJoinGate, nil, nil)
	summary := waitForJoinSummary(t, flowID)
	require.Len(t, summary.BranchResults, 1)
	require.ElementsMatch(t, []string{"A", "B"}, summary.BranchResults[0])
}

func TestJoinFirstBranchCancelsSiblingBranchesAndKeepsLateResults(t *testing.T) {
	flowID := startJoinFlow(t, joinPlan{
		Scenario:         joinFirstAndCancel,
		BranchNames:      []string{"A", "B", "C"},
		ReleasedBranches: []string{"A", "B", "C"},
	})
	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "A", nil)
	waitForJoinStep(t, flowID, joinBranchStep{}, 1)
	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "B", nil)
	waitForJoinStep(t, flowID, joinBranchStep{}, 2)
	invokeJoinRPC(t, flowID, joinFlow{}.OpenJoinGate, nil, nil)
	waitForJoinStep(t, flowID, joinStep{}, 1)

	// Branch C was canceled, so its release stays pending and only B's late result remains.
	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "C", nil)
	var sizes joinChannelSizes
	invokeJoinRPC(t, flowID, joinFlow{}.GetJoinChannelSizes, joinChannelSizesInput{RoundKey: "joinSplitStep-1", BranchName: "C"}, &sizes)
	require.Equal(t, joinChannelSizes{PendingResults: 1, PendingReleases: 1}, sizes)
	invokeJoinRPC(t, flowID, joinFlow{}.OpenJoinGate, nil, nil)

	summary := waitForJoinSummary(t, flowID)
	require.Equal(t, [][]string{{"A"}}, summary.BranchResults)
}

func TestJoinRPCReportsABranchResult(t *testing.T) {
	// The RPC cancels branch B before the test releases it, so only the RPC reports for B.
	flowID := startJoinFlow(t, joinPlan{
		Scenario:         joinRPCReportsCanceled,
		BranchNames:      []string{"A", "B"},
		ReleasedBranches: []string{"A"},
	})
	round := dex.JoinRound{RoundKey: "joinSplitStep-1", BranchCount: 2}
	var sizes joinChannelSizes
	var err error
	require.Eventually(t, func() bool {
		var polled joinChannelSizes
		err = integClient.InvokeRPC(integrationContext(t), flowID, joinFlow{}.GetJoinChannelSizes, joinChannelSizesInput{RoundKey: round.RoundKey, BranchName: "B"}, &polled)
		return err == nil && polled.WaitingBranches == 1
	}, 30*time.Second, 50*time.Millisecond, "branch B never reached its wait: %v", err)
	invokeJoinRPC(t, flowID, joinFlow{}.ReportJoinBranchResult, joinReportInput{
		Round:             round,
		Result:            "B-canceled",
		CancelsBranchStep: true,
	}, nil)
	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "A", nil)
	waitForJoinStep(t, flowID, joinStep{}, 1)

	// A waiting branch B would consume this release at once; it stays pending only after cancellation.
	invokeJoinRPC(t, flowID, joinFlow{}.ReleaseJoinBranch, "B", nil)
	invokeJoinRPC(t, flowID, joinFlow{}.GetJoinChannelSizes, joinChannelSizesInput{RoundKey: round.RoundKey, BranchName: "B"}, &sizes)
	require.Equal(t, joinChannelSizes{PendingResults: 0, PendingReleases: 1, WaitingBranches: 1}, sizes)
	invokeJoinRPC(t, flowID, joinFlow{}.OpenJoinGate, nil, nil)

	summary := waitForJoinSummary(t, flowID)
	require.Equal(t, [][]string{{"B-canceled", "A"}}, summary.BranchResults)
}

func TestJoinForkTwiceInOneInvocationFailsTheStep(t *testing.T) {
	flowID := startJoinFlow(t, joinPlan{Scenario: joinForkTwice, BranchNames: []string{"A"}})

	summary := waitForJoinSummary(t, flowID)
	require.Contains(t, summary.FailureDetail, `dex: Join "go-join-results" was already forked in this invocation`)
}

func TestJoinForkFromRPCRequiresAnExplicitRoundKey(t *testing.T) {
	flowID := startJoinFlow(t, joinPlan{Scenario: joinForkFromRPC})

	var forkError string
	invokeJoinRPC(t, flowID, joinFlow{}.ForkJoinFromRPC, joinRPCForkInput{Mode: joinRPCForkModeStepExecutionKey}, &forkError)
	require.Equal(t, `dex: Join "go-join-results" Fork requires a Step execution ID; an RPC uses ForkWithRoundKey`, forkError)
	invokeJoinRPC(t, flowID, joinFlow{}.ForkJoinFromRPC, joinRPCForkInput{Mode: joinRPCForkModeSlashKey, RoundKey: "round/1"}, &forkError)
	require.Equal(t, `dex: Join "go-join-results" round key "round/1" must not contain '/'`, forkError)
	invokeJoinRPC(t, flowID, joinFlow{}.ForkJoinFromRPC, joinRPCForkInput{Mode: joinRPCForkModeExplicitKey, RoundKey: "rpc-round-1"}, &forkError)
	require.Empty(t, forkError)

	summary := waitForJoinSummary(t, flowID)
	require.Equal(t, []string{"rpc-round-1"}, summary.RoundKeys)
	require.Equal(t, []int{2}, summary.BranchCounts)
	require.Len(t, summary.BranchResults, 1)
	require.ElementsMatch(t, []string{"X", "Y"}, summary.BranchResults[0])
}

func TestJoinInvalidConditionsFailWaitSerialization(t *testing.T) {
	for _, testCase := range []struct {
		invalidCondition string
		branchNames      []string
		expectedDetail   string
	}{
		{joinInvalidForBranchesZero, []string{"A", "B"}, `dex: Join "go-join-results" count 0 must be between 1 and the branch count 2`},
		{joinInvalidForBranchesAboveMax, []string{"A", "B"}, `dex: Join "go-join-results" count 3 must be between 1 and the branch count 2`},
		{joinInvalidRemainingNegative, []string{"A", "B"}, `dex: Join "go-join-results" received count -1 must be between 0 and the branch count 2`},
		{joinInvalidRemainingAboveMax, []string{"A", "B"}, `dex: Join "go-join-results" received count 3 must be between 0 and the branch count 2`},
		{joinInvalidFirstOfZeroBranches, nil, `dex: Join "go-join-results" count 1 must be between 1 and the branch count 0`},
	} {
		t.Run(testCase.invalidCondition, func(t *testing.T) {
			flowID := startJoinFlow(t, joinPlan{
				Scenario:         joinInvalidCondition,
				BranchNames:      testCase.branchNames,
				InvalidCondition: testCase.invalidCondition,
			})
			summary := waitForJoinSummary(t, flowID)
			require.Contains(t, summary.FailureDetail, testCase.expectedDetail)
		})
	}
}

func TestJoinZeroCountConditionsAreSatisfiedImmediately(t *testing.T) {
	t.Run("zero branches", func(t *testing.T) {
		flowID := startJoinFlow(t, joinPlan{Scenario: joinZeroBranches, RoundNumber: 1, RoundCount: 2})
		summary := waitForJoinSummary(t, flowID)
		require.Equal(t, []int{0, 0}, summary.BranchCounts)
		require.Equal(t, [][]string{{}, {}}, summary.BranchResults)
		require.Equal(t, [][]string{{}}, summary.RemainingResults)
	})
	t.Run("no remaining results", func(t *testing.T) {
		flowID := startJoinFlow(t, joinPlan{
			Scenario:        joinZeroRemaining,
			BranchNames:     []string{"A", "B"},
			RequiredResults: 2,
			RoundNumber:     1,
			RoundCount:      2,
		})
		summary := waitForJoinSummary(t, flowID)
		require.Equal(t, []string{"joinSplitStep-1", "joinSplitStep-2"}, summary.RoundKeys)
		require.Equal(t, [][]string{{}}, summary.RemainingResults)
	})
}

func TestJoinDuplicateNameWithChannelMapFailsRegistration(t *testing.T) {
	_, err := dex.NewRegistry([]dex.Flow{joinDuplicateNameFlow{}})
	require.ErrorContains(t, err, `duplicate channel "go-join-results"`)
}

func TestJoinWithTimerDeadlineLetsTheTimerWin(t *testing.T) {
	flowID := startJoinFlow(t, joinPlan{
		Scenario:         joinTimerDeadline,
		BranchNames:      []string{"A", "B"},
		ReleasedBranches: []string{"B"},
	})
	waitForJoinStep(t, flowID, joinBranchStep{}, 1)
	ctx := integrationContext(t)
	var err error
	require.Eventually(t, func() bool {
		err = integClient.SkipTimer(
			ctx,
			flowID,
			dex.StepExecutionID{StepType: dex.GetFinalStepType(joinStep{})},
			dex.TimerID{Index: ptr.Any(int32(0))},
		)
		return err == nil
	}, 20*time.Second, 100*time.Millisecond, "SkipTimer failed: %v", err)

	summary := waitForJoinSummary(t, flowID)
	require.True(t, summary.HasTimerFired)
	require.Equal(t, [][]string{{}}, summary.BranchResults)
}

func startJoinFlow(t *testing.T, plan joinPlan) string {
	t.Helper()
	flowID := newFlowID(t, "join-"+string(plan.Scenario))
	_, err := integClient.StartFlow(integrationContext(t), joinFlow{}, flowID, plan, dex.StartFlowOptions{})
	require.NoError(t, err)
	return flowID
}

func invokeJoinRPC[IN, OUT any](t *testing.T, flowID string, rpc dex.RPC[IN, OUT], input IN, output *OUT) {
	t.Helper()
	if output == nil {
		output = new(OUT)
	}
	require.NoError(t, integClient.InvokeRPC(integrationContext(t), flowID, rpc, input, output))
}

func waitForJoinStep[IN any](t *testing.T, flowID string, step dex.Step[IN], executionNumber int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(integrationContext(t), 30*time.Second)
	defer cancel()
	require.NoError(t, integClient.WaitForStepCompletion(
		ctx,
		flowID,
		dex.StepExecutionID{StepType: dex.GetFinalStepType(step), ExecutionNumber: ptr.Any(executionNumber)},
		dex.WaitForStepCompletionOptions{RequestID: uuid.NewString()},
	))
}

func waitForJoinSummary(t *testing.T, flowID string) joinSummary {
	t.Helper()
	result := waitForFlow(t, flowID, true)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, 1)
	var summary joinSummary
	require.NoError(t, result.Completions[0].Output.Decode(&summary))
	return summary
}
